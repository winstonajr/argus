package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/monitor"
	"argus/internal/storage"
	"argus/internal/web"
)

func main() {
	configPathFlag := flag.String("config", "config.yaml", "Caminho para o arquivo de configuração YAML")
	flag.Parse()

	configPath := *configPathFlag
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

	// Pipe check results and state transitions into SQLite
	store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
		db.AsyncSaveCheck(sysID, kind, res)
	})
	store.SubscribeEvents(func(e monitor.ChangeEvent) {
		db.AsyncSaveEvent(e)
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

	// Graceful shutdown handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("[INFO] Sinal recebido (%v); encerrando Argus com segurança...", sig)
	case err := <-serverErrCh:
		log.Fatalf("[FATAL] Erro no servidor HTTP: %v", err)
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
