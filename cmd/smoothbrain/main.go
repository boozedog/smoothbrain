package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmarx/smoothbrain/internal/auth"
	"github.com/dmarx/smoothbrain/internal/config"
	"github.com/dmarx/smoothbrain/internal/core"
	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/dmarx/smoothbrain/internal/plugin/claudecode"
	"github.com/dmarx/smoothbrain/internal/plugin/mattermost"
	"github.com/dmarx/smoothbrain/internal/plugin/obsidian"
	"github.com/dmarx/smoothbrain/internal/plugin/tailscale"
	"github.com/dmarx/smoothbrain/internal/plugin/td"
	"github.com/dmarx/smoothbrain/internal/plugin/uptimekuma"
	"github.com/dmarx/smoothbrain/internal/plugin/webmd"
	"github.com/dmarx/smoothbrain/internal/plugin/xai"
	"github.com/dmarx/smoothbrain/internal/store"
	"github.com/lmittmann/tint"
)

func main() {
	configPath := flag.String("config", "/etc/smoothbrain/config.json", "path to config file")
	flag.Parse()

	// Log buffer captures recent entries for the web UI.
	logBuf := core.NewLogBuffer(200)

	// Bootstrap logger for config loading.
	log := slog.New(core.NewLogHandler(tint.NewHandler(os.Stderr, &tint.Options{TimeFormat: time.TimeOnly}), logBuf))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Re-create logger with configured level.
	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	log = slog.New(core.NewLogHandler(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      level,
		TimeFormat: time.TimeOnly,
	}), logBuf))
	log.Info("config loaded", "http", cfg.HTTP.Address, "database", cfg.Database, "log_level", level.String())

	db, err := store.Open(cfg.Database)
	if err != nil {
		log.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("database ready", "path", cfg.Database)

	// Plugin registry
	registry := plugin.NewRegistry(log, db.DB())
	registry.Register(uptimekuma.New(log))
	registry.Register(td.New(log))
	registry.Register(xai.New(log))
	registry.Register(mattermost.New(log))
	registry.Register(webmd.New(log))
	registry.Register(claudecode.New(log))
	registry.Register(obsidian.New(log))
	registry.Register(tailscale.New(log))

	if err := registry.InitAll(cfg.Plugins); err != nil {
		log.Error("failed to init plugins", "error", err)
		os.Exit(1)
	}

	// Build command list from routes and pass to command-aware plugins.
	cmdsBySource := make(map[string][]plugin.CommandInfo)
	for _, r := range cfg.Routes {
		if r.Event != "" {
			cmdsBySource[r.Source] = append(cmdsBySource[r.Source], plugin.CommandInfo{
				Name:        r.Event,
				Description: r.Description,
			})
		}
	}
	for source, cmds := range cmdsBySource {
		if p, ok := registry.Get(source); ok {
			if ca, ok := p.(plugin.CommandAware); ok {
				ca.SetCommands(cmds)
			}
		}
	}

	// Event bus + router + websocket hub
	bus := core.NewBus(db, log)
	hub := core.NewHub(db, log)
	router := core.NewRouter(cfg.Routes, registry, db, log)
	router.SetNotifyFn(hub.Notify)
	bus.Subscribe(router.HandleEvent)
	bus.Subscribe(hub.HandleEvent)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	if err := registry.StartAll(ctx, bus); err != nil {
		log.Error("failed to start plugins", "error", err)
		os.Exit(1)
	}
	defer registry.StopAll()

	supervisor := core.NewSupervisor(cfg.Supervisor.Tasks, bus, db, log)
	supervisor.Start(ctx)
	defer supervisor.Stop()

	// HTTP server
	srv := core.NewServer(db, log, hub, registry, cfg.Routes, logBuf)
	registry.RegisterWebhooks(srv)

	var handler http.Handler = srv.Handler()
	if cfg.Auth.RPID != "" {
		a, err := auth.New(cfg.Auth, db.DB(), log)
		if err != nil {
			log.Error("failed to init auth", "error", err)
			os.Exit(1)
		}
		a.RegisterRoutes(srv.Mux())
		handler = a.Middleware(srv.Handler())
		log.Info("auth enabled", "rp_id", cfg.Auth.RPID)
	}

	httpServer := &http.Server{
		Addr:    cfg.HTTP.Address,
		Handler: handler,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		httpServer.Shutdown(shutdownCtx)
		cancel()
	}()

	log.Info("smoothbrain starting", "address", cfg.HTTP.Address)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Error("http server error", "error", err)
		os.Exit(1)
	}
}
