package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/monitor"
	"argus/internal/notify"
	"argus/internal/storage"
	"argus/internal/web"
)

func main() {
	configPathFlag := flag.String("config", "config.yaml", "Caminho para o arquivo de configuração YAML")
	flag.Parse()

	configPath := *configPathFlag
	if configPath == "config.yaml" {
		if envPath := os.Getenv("ARGUS_CONFIG_PATH"); envPath != "" {
			configPath = envPath
		} else if envPath := os.Getenv("ARGUS_CONFIG"); envPath != "" {
			configPath = envPath
		}
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) && configPath == "config.yaml" {
		if _, errEx := os.Stat("config.example.yaml"); errEx == nil {
			configPath = "config.example.yaml"
			log.Printf("[INFO] config.yaml não encontrado; usando config.example.yaml de demonstração")
		}
	}

	log.Printf("[INFO] Argus iniciando com configuração: %s", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("[FATAL] Erro ao carregar configuração: %v", err)
	}

	// Initialize SQLite Database
	db, err := storage.Open(cfg.Storage.Path, cfg.Storage.RetentionChecks, cfg.Storage.RetentionEvents)
	if err != nil {
		log.Fatalf("[FATAL] Erro ao inicializar banco SQLite: %v", err)
	}
	log.Printf("[INFO] Banco SQLite pronto em %s", cfg.Storage.Path)

	// Initialize In-Memory Store
	store := monitor.NewStore(cfg)

	// Hydrate initial state from SQLite history
	hydrateCtx, hydrateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	for _, sys := range cfg.Systems {
		if strings.TrimSpace(sys.URL) != "" {
			if checks, err := db.GetRecentChecks(hydrateCtx, sys.ID, "dns", 120); err == nil && len(checks) > 0 {
				lastEv, _ := db.GetLatestEventTime(hydrateCtx, sys.ID, "dns")
				store.HydrateCheck(sys.ID, "dns", checks, lastEv)
			}
		}
		if strings.TrimSpace(sys.IP) != "" {
			if checks, err := db.GetRecentChecks(hydrateCtx, sys.ID, "ip", 120); err == nil && len(checks) > 0 {
				lastEv, _ := db.GetLatestEventTime(hydrateCtx, sys.ID, "ip")
				store.HydrateCheck(sys.ID, "ip", checks, lastEv)
			}
		}
	}
	if overrides, err := db.GetMaintenanceOverrides(hydrateCtx); err == nil {
		for sysID, enabled := range overrides {
			store.SetMaintenance(sysID, enabled)
		}
	}
	hydrateCancel()

	// Pipe check results and state transitions into SQLite
	store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
		db.AsyncSaveCheck(sysID, kind, res)
	})
	notifier := notify.New(cfg.Alerts)
	if notifier.Enabled() {
		log.Printf("[INFO] Central de notificações ativada (cooldown: %v)", cfg.Alerts.Cooldown)
	}

	store.SubscribeEvents(func(e monitor.ChangeEvent) {
		db.AsyncSaveEvent(e)
		notifier.HandleEvent(e)
		log.Printf("[EVENT] %s (%s) mudou de %s para %s — Diagnóstico: %s (%s)",
			e.SystemName, e.Kind, e.FromStatus, e.ToStatus, e.Diagnosis, e.Detail)
	})

	// Initialize Checkers & Scheduler
	httpChecker := checker.NewHTTPChecker()
	pingChecker := checker.NewPingChecker()

	scheduler := monitor.NewScheduler(cfg, store, httpChecker, pingChecker)
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	scheduler.Start(rootCtx)
	log.Printf("[INFO] Scheduler de monitoramento iniciado para %d sistemas", len(cfg.Systems))

	// Initialize Web Server
	webServer, err := web.NewServer(cfg, store, db)
	if err != nil {
		log.Fatalf("[FATAL] Erro ao inicializar servidor web: %v", err)
	}
	webServer.SetCheckTrigger(scheduler.CheckNow)
	webServer.SetNotifier(notifier)

	httpSrv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           webServer,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Server runner
	serverErrCh := make(chan error, 1)
	go func() {
		log.Printf("[INFO] Argus ouvindo em http://localhost%s (ou %s)", cfg.Server.Addr, cfg.Server.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	// Graceful shutdown & SIGHUP hot-reload handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)

	var lastCfgMod time.Time
	var lastCfgSize int64
	if info, err := os.Stat(configPath); err == nil {
		lastCfgMod = info.ModTime()
		lastCfgSize = info.Size()
	}

	configWatchTicker := time.NewTicker(5 * time.Second)
	defer configWatchTicker.Stop()

	reloadConfig := func(reason string) {
		log.Printf("[INFO] %s; recarregando configuração de %s...", reason, configPath)
		newCfg, reloadErr := config.Load(configPath)
		if reloadErr != nil {
			log.Printf("[WARN] Falha ao recarregar configuração (mantendo atual): %v", reloadErr)
			return
		}
		rootCancel()
		scheduler.Stop()
		store.ReloadConfig(newCfg)
		ovCtx, ovCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if overrides, err := db.GetMaintenanceOverrides(ovCtx); err == nil {
			for sysID, enabled := range overrides {
				store.SetMaintenance(sysID, enabled)
			}
		}
		ovCancel()
		notifier.UpdateConfig(newCfg.Alerts)
		webServer.UpdateConfig(newCfg)
		rootCtx, rootCancel = context.WithCancel(context.Background())
		scheduler = monitor.NewScheduler(newCfg, store, httpChecker, pingChecker)
		scheduler.Start(rootCtx)
		webServer.SetCheckTrigger(scheduler.CheckNow)
		log.Printf("[INFO] Configuração recarregada (%d sistemas)", len(newCfg.Systems))
	}

loop:
	for {
		select {
		case sig := <-sigCh:
			log.Printf("[INFO] Sinal recebido (%v); encerrando Argus com segurança...", sig)
			break loop
		case <-reloadCh:
			reloadConfig("SIGHUP recebido")
		case <-configWatchTicker.C:
			info, err := os.Stat(configPath)
			if err != nil {
				continue
			}
			if !info.ModTime().Equal(lastCfgMod) || info.Size() != lastCfgSize {
				time.Sleep(150 * time.Millisecond)
				if latest, statErr := os.Stat(configPath); statErr == nil {
					lastCfgMod = latest.ModTime()
					lastCfgSize = latest.Size()
				}
				reloadConfig("Alteração detectada no arquivo de configuração")
			}
		case err := <-serverErrCh:
			log.Fatalf("[FATAL] Erro no servidor HTTP: %v", err)
		}
	}

	// 1. Stop scheduler and check loops
	log.Printf("[INFO] Parando scheduler...")
	rootCancel()
	scheduler.Stop()

	// 2. Shutdown HTTP server gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[WARN] Erro no shutdown do servidor HTTP: %v", err)
	}

	// 3. Flush & Close SQLite database
	log.Printf("[INFO] Gravando pendências e fechando banco SQLite...")
	if err := db.Close(); err != nil {
		log.Printf("[WARN] Erro ao fechar SQLite: %v", err)
	}

	log.Printf("[INFO] Argus finalizado com sucesso.")
}
