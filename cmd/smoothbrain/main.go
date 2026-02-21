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

	"github.com/boozedog/smoothbrain/internal/auth"
	"github.com/boozedog/smoothbrain/internal/config"
	"github.com/boozedog/smoothbrain/internal/core"
	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/plugin/claudecode"
	"github.com/boozedog/smoothbrain/internal/plugin/mattermost"
	"github.com/boozedog/smoothbrain/internal/plugin/obsidian"
	"github.com/boozedog/smoothbrain/internal/plugin/tailscale"
	"github.com/boozedog/smoothbrain/internal/plugin/td"
	"github.com/boozedog/smoothbrain/internal/plugin/uptimekuma"
	"github.com/boozedog/smoothbrain/internal/plugin/webmd"
	"github.com/boozedog/smoothbrain/internal/plugin/xai"
	"github.com/boozedog/smoothbrain/internal/store"
	"github.com/lmittmann/tint"
	"tailscale.com/tsnet"
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
	defer func() { _ = db.Close() }()
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

	handler := srv.Handler()
	if cfg.Auth.RPID != "" {
		a, err := auth.New(cfg.Auth, db.DB(), log)
		if err != nil {
			log.Error("failed to init auth", "error", err)
			os.Exit(1)
		}
		a.RegisterRoutes(srv.Mux())
		handler = a.Middleware(srv.Handler())
		log.Info("auth enabled", "rp_id", cfg.Auth.RPID)
		a.StartCleanup(ctx)
	}

	// tsnet listener (Tailscale Service)
	var tsServer *tsnet.Server
	if cfg.Tailscale.Enabled {
		if err := os.MkdirAll(cfg.Tailscale.StateDir, 0o700); err != nil {
			log.Error("failed to create tsnet state dir", "error", err)
			os.Exit(1)
		}
		tsServer = &tsnet.Server{
			Hostname:  cfg.Tailscale.Hostname,
			Dir:       cfg.Tailscale.StateDir,
			AuthKey:   cfg.Tailscale.AuthKey,
			Ephemeral: cfg.Tailscale.Ephemeral,
		}
		ln, err := tsServer.ListenService(cfg.Tailscale.ServiceName, tsnet.ServiceModeHTTP{HTTPS: true, Port: 443})
		if err != nil {
			log.Error("tsnet listen failed", "error", err)
			os.Exit(1)
		}
		go func() {
			log.Info("tsnet service listening", "service", cfg.Tailscale.ServiceName, "hostname", cfg.Tailscale.Hostname)
			//nolint:gosec // tsnet listener is internal; timeouts are set on the main HTTP server
			if err := http.Serve(ln, handler); err != nil {
				log.Error("tsnet serve error", "error", err)
			}
		}()

		// Inject tsnet server into the tailscale health plugin.
		if p, ok := registry.Get("tailscale"); ok {
			if tp, ok := p.(*tailscale.Plugin); ok {
				tp.SetServer(tsServer)
			} else {
				log.Error("tailscale plugin has unexpected type")
			}
		}
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("http shutdown error", "error", err)
		}
		if tsServer != nil {
			if err := tsServer.Close(); err != nil {
				log.Error("tsnet close error", "error", err)
			}
		}
		cancel()
	}()

	log.Info("smoothbrain starting", "address", cfg.HTTP.Address)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Error("http server error", "error", err)
		os.Exit(1)
	}
}
