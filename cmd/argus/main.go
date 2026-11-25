package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
		for sysID, ov := range overrides {
			store.SetMaintenanceUntil(sysID, ov.Enabled, ov.Until)
		}
	}
	hydrateCancel()

	store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
		db.AsyncSaveCheck(sysID, kind, res)
	})
	notifier := notify.New(cfg.Alerts)
	notifier.SetSystems(cfg.Systems)
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
	notifier.StartEscalationLoop(rootCtx, func(sysID string) bool {
		snap, ok := store.GetSystem(sysID)
		return ok && !snap.InMaintenance && snap.BlockedByID == ""
	})
	go runSLAReportLoop(rootCtx, cfg.Alerts.SLAReport, store, db, notifier)
	slog.Info("scheduler de monitoramento iniciado", "systems", len(cfg.Systems))

	webServer, err := web.NewServer(cfg, store, db)
	if err != nil {
		slog.Error("erro ao inicializar servidor web", "err", err)
		rootCancel()
		scheduler.Stop()
		_ = db.Close()
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
		if newCfg.Server.Timezone != "" {
			if loc, locErr := time.LoadLocation(newCfg.Server.Timezone); locErr == nil {
				time.Local = loc
			}
		}
		rootCancel()
		scheduler.Stop()
		store.ReloadConfig(newCfg)
		ovCtx, ovCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if overrides, err := db.GetMaintenanceOverrides(ovCtx); err == nil {
			for sysID, ov := range overrides {
				store.SetMaintenanceUntil(sysID, ov.Enabled, ov.Until)
			}
		}
		ovCancel()
		notifier.UpdateConfig(newCfg.Alerts)
		notifier.SetSystems(newCfg.Systems)
		webServer.UpdateConfig(newCfg)
		rootCtx, rootCancel = context.WithCancel(context.Background())
		scheduler = monitor.NewScheduler(newCfg, store, httpChecker, pingChecker)
		scheduler.Start(rootCtx)
		notifier.StartEscalationLoop(rootCtx, func(sysID string) bool {
			snap, ok := store.GetSystem(sysID)
			return ok && !snap.InMaintenance && snap.BlockedByID == ""
		})
		go runSLAReportLoop(rootCtx, newCfg.Alerts.SLAReport, store, db, notifier)
		webServer.SetCheckTrigger(scheduler.CheckNow)
		slog.Info("configuração recarregada", "systems", len(newCfg.Systems))
	}

	exitCode := 0

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
			exitCode = 1
			break loop
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
	notifier.Close(shutdownCtx)

	slog.Info("gravando pendências e fechando banco SQLite")
	if err := db.Close(); err != nil {
		slog.Warn("erro ao fechar SQLite", "err", err)
	}

	slog.Info("Argus finalizado com sucesso")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func runSLAReportLoop(ctx context.Context, cfg config.SLAReportConfig, store *monitor.Store, db *storage.DB, notifier *notify.Notifier) {
	if cfg.Schedule == "" || db == nil || notifier == nil {
		return
	}
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastSentKey string
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if !matchesSLASchedule(cfg, now) {
				continue
			}
			key := now.Format("2006-01-02-15-04")
			if key == lastSentKey {
				continue
			}
			lastSentKey = key
			title, summary := buildSLAReport(ctx, cfg.Schedule, store, db, now)
			if err := notifier.SendSLAReport(ctx, title, summary); err != nil {
				slog.Warn("falha ao enviar relatório periódico de SLA", "err", err)
			}
		}
	}
}

func matchesSLASchedule(cfg config.SLAReportConfig, now time.Time) bool {
	if cfg.Schedule == "" {
		return false
	}
	if now.Format("15:04") != cfg.Hour {
		return false
	}
	switch cfg.Schedule {
	case "weekly":
		return strings.EqualFold(now.Weekday().String()[:3], cfg.Day)
	case "monthly":
		var dayNum int
		_, _ = fmt.Sscanf(cfg.Day, "%d", &dayNum)
		return now.Day() == dayNum
	default:
		return false
	}
}

func buildSLAReport(ctx context.Context, schedule string, store *monitor.Store, db *storage.DB, now time.Time) (string, string) {
	window := 7 * 24 * time.Hour
	periodLabel := "7 dias"
	if schedule == "monthly" {
		window = 30 * 24 * time.Hour
		periodLabel = "30 dias"
	}
	from := now.Add(-window).UTC()
	to := now.UTC()

	systems := store.GetAllSystems()
	var totalUptime float64
	var uptimeCount int
	var totalMTTR time.Duration
	var mttrCount int
	var topIncidents []string
	var expiringSSL []string

	for _, sys := range systems {
		stats, err := db.GetSystemUptimeStats(ctx, sys.ID, from, to)
		if err == nil {
			u := stats.DNSUptime
			if sys.IP != "" && sys.URL == "" {
				u = stats.IPUptime
			}
			totalUptime += u
			uptimeCount++
			if stats.MTTR > 0 {
				totalMTTR += stats.MTTR
				mttrCount++
			}
			if stats.IncidentCount > 0 {
				topIncidents = append(topIncidents, fmt.Sprintf("%s (%d)", sys.Name, stats.IncidentCount))
			}
		}
		if sys.DNS.Configured && sys.DNS.LastResult.HasTLS && sys.DNS.LastResult.TLSDaysLeft <= sys.DNS.TLSWarnDays {
			expiringSSL = append(expiringSSL, fmt.Sprintf("%s (%dd)", sys.Name, sys.DNS.LastResult.TLSDaysLeft))
		}
	}

	avgUptime := 100.0
	if uptimeCount > 0 {
		avgUptime = totalUptime / float64(uptimeCount)
	}
	avgMTTR := "0s"
	if mttrCount > 0 {
		avgMTTR = (totalMTTR / time.Duration(mttrCount)).Truncate(time.Second).String()
	}

	title := fmt.Sprintf("Relatório SLA Argus (%s)", periodLabel)
	var lines []string
	lines = append(lines, fmt.Sprintf("Sistemas monitorados: %d", len(systems)))
	lines = append(lines, fmt.Sprintf("Disponibilidade média: %.2f%%", avgUptime))
	lines = append(lines, fmt.Sprintf("MTTR médio: %s", avgMTTR))
	if len(topIncidents) > 0 {
		if len(topIncidents) > 5 {
			topIncidents = topIncidents[:5]
		}
		lines = append(lines, "Sistemas com incidentes: "+strings.Join(topIncidents, ", "))
	} else {
		lines = append(lines, "Nenhum incidente registrado no período.")
	}
	if len(expiringSSL) > 0 {
		lines = append(lines, "Certificados SSL próximos do vencimento: "+strings.Join(expiringSSL, ", "))
	}
	return title, strings.Join(lines, "\n")
}
