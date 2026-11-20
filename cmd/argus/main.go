package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/monitor"
	"argus/internal/notify"
	"argus/internal/storage"
	"argus/internal/web"
)

var version = "dev"

func initLogger() {
	if strings.EqualFold(os.Getenv("ARGUS_LOG_FORMAT"), "json") {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}
}

func main() {
	initLogger()

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
			slog.Info("config.yaml não encontrado; usando config.example.yaml de demonstração")
		}
	}

	slog.Info("Argus iniciando", "version", version, "config", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("erro ao carregar configuração", "err", err)
		os.Exit(1)
	}

	if cfg.Server.Timezone != "" {
		loc, locErr := time.LoadLocation(cfg.Server.Timezone)
		if locErr != nil {
			slog.Error("erro ao carregar timezone", "timezone", cfg.Server.Timezone, "err", locErr)
			os.Exit(1)
		}
		time.Local = loc
		slog.Info("timezone configurado", "timezone", time.Local.String())
	}

	db, err := storage.Open(cfg.Storage.Path, cfg.Storage.RetentionChecks, cfg.Storage.RetentionEvents)
	if err != nil {
		slog.Error("erro ao inicializar banco SQLite", "err", err)
		os.Exit(1)
	}
	slog.Info("banco SQLite pronto", "path", cfg.Storage.Path)

	store := monitor.NewStore(cfg)

	hydrateCtx, hydrateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	checksByKey, _ := db.GetRecentChecksBatch(hydrateCtx, 362)
	eventsByKey, _ := db.GetLatestEventTimesBatch(hydrateCtx)
	for _, sys := range cfg.Systems {
		if strings.TrimSpace(sys.URL) != "" {
			key := sys.ID + ":dns"
			if checks := checksByKey[key]; len(checks) > 0 {
				store.HydrateCheck(sys.ID, "dns", checks, eventsByKey[key])
			}
		}
		if strings.TrimSpace(sys.IP) != "" {
			key := sys.ID + ":ip"
			if checks := checksByKey[key]; len(checks) > 0 {
				store.HydrateCheck(sys.ID, "ip", checks, eventsByKey[key])
			}
		}
	}
	if overrides, err := db.GetMaintenanceOverrides(hydrateCtx); err == nil {
		for sysID, enabled := range overrides {
			store.SetMaintenance(sysID, enabled)
		}
	}
	hydrateCancel()

	store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
		db.AsyncSaveCheck(sysID, kind, res)
	})
	notifier := notify.New(cfg.Alerts)
	if notifier.Enabled() {
		slog.Info("central de notificações ativada", "cooldown", cfg.Alerts.Cooldown)
	}

	store.SubscribeEvents(func(e monitor.ChangeEvent) {
		db.AsyncSaveEvent(e)
		notifier.HandleEvent(e)
		slog.Info("mudança de estado",
			"system", e.SystemName,
			"kind", e.Kind,
			"from", e.FromStatus,
			"to", e.ToStatus,
			"diagnosis", e.Diagnosis,
			"detail", e.Detail,
		)
	})

	httpChecker := checker.NewHTTPChecker()
	pingChecker := checker.NewPingChecker()

	scheduler := monitor.NewScheduler(cfg, store, httpChecker, pingChecker)
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	scheduler.Start(rootCtx)
	slog.Info("scheduler de monitoramento iniciado", "systems", len(cfg.Systems))

	webServer, err := web.NewServer(cfg, store, db)
	if err != nil {
		slog.Error("erro ao inicializar servidor web", "err", err)
		os.Exit(1)
	}
	webServer.SetCheckTrigger(scheduler.CheckNow)
	webServer.SetNotifier(notifier)

	httpSrv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           webServer,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		slog.Info("Argus ouvindo", "addr", cfg.Server.Addr, "lang", cfg.Server.Lang)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

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

	var debounceCh <-chan time.Time

	reloadConfig := func(reason string) {
		slog.Info("recarregando configuração", "reason", reason, "path", configPath)
		newCfg, reloadErr := config.Load(configPath)
		if reloadErr != nil {
			slog.Warn("falha ao recarregar configuração (mantendo atual)", "err", reloadErr)
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
		slog.Info("configuração recarregada", "systems", len(newCfg.Systems))
	}

loop:
	for {
		select {
		case sig := <-sigCh:
			slog.Info("sinal recebido; encerrando Argus com segurança", "signal", sig)
			break loop
		case <-reloadCh:
			reloadConfig("SIGHUP recebido")
		case <-configWatchTicker.C:
			info, err := os.Stat(configPath)
			if err != nil {
				continue
			}
			if !info.ModTime().Equal(lastCfgMod) || info.Size() != lastCfgSize {
				lastCfgMod = info.ModTime()
				lastCfgSize = info.Size()
				debounceCh = time.After(150 * time.Millisecond)
			}
		case <-debounceCh:
			debounceCh = nil
			if latest, statErr := os.Stat(configPath); statErr == nil {
				lastCfgMod = latest.ModTime()
				lastCfgSize = latest.Size()
			}
			reloadConfig("Alteração detectada no arquivo de configuração")
		case err := <-serverErrCh:
			slog.Error("erro fatal no servidor HTTP", "err", err)
			os.Exit(1)
		}
	}

	slog.Info("parando scheduler")
	rootCancel()
	scheduler.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("erro no shutdown do servidor HTTP", "err", err)
	}

	slog.Info("gravando pendências e fechando banco SQLite")
	if err := db.Close(); err != nil {
		slog.Warn("erro ao fechar SQLite", "err", err)
	}

	slog.Info("Argus finalizado com sucesso")
}
