package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"notification_relay/api"
	"notification_relay/config"
	"notification_relay/db"
	ldapsync "notification_relay/ldap"
	"notification_relay/notify"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		// slog not yet initialised; use stderr directly
		slog.Error("failed to load config", "path", *cfgPath, "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Logging)
	logger.Info("notification relay starting")

	// Database
	writer, reader, err := db.Open(cfg.Database.Path, cfg.Database.MaxReaderConns)
	if err != nil {
		logger.Error("failed to open database", "path", cfg.Database.Path, "error", err)
		os.Exit(1)
	}
	defer writer.Close()
	defer reader.Close()

	if err := db.RunMigrations(writer); err != nil {
		logger.Error("migrations failed", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations applied")

	writerQ := db.New(writer)
	readerQ := db.New(reader)
	_ = readerQ // used by HTTP handlers in the next phase

	// Job queue
	jobQueue := make(chan notify.Job, 256)

	// Context cancelled on OS signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	// LDAP syncer
	ldapClient := ldapsync.NewClient(
		cfg.LDAP.PrimaryURL,
		cfg.LDAP.BackupURL,
		cfg.LDAP.BindDN,
		cfg.LDAP.BindPassword,
		cfg.LDAP.UserBaseDN,
		cfg.LDAP.GroupBaseDN,
		cfg.LDAP.GroupFilter,
		cfg.LDAP.TLSSkipVerify,
	)
	syncer := ldapsync.NewSyncer(cfg.LDAP, ldapClient, writer, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		syncer.Run(ctx)
	}()

	// Twilio status poller
	poller := notify.NewPoller(cfg.Twilio, writer, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		poller.Run(ctx)
	}()

	// Dispatcher
	dispatcher := notify.NewDispatcher(cfg.Notify, writerQ, jobQueue, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatcher.Run(ctx)
	}()

	// LDAP authenticator (with LRU cache)
	ldapAuth := ldapsync.NewAuthenticator(
		cfg.LDAP.PrimaryURL,
		cfg.LDAP.BackupURL,
		cfg.LDAP.BindDN,
		cfg.LDAP.BindPassword,
		cfg.LDAP.UserBaseDN,
		cfg.LDAP.TLSSkipVerify,
	)
	ldapAuth = ldapsync.NewCachedAuthenticator(ldapAuth, cfg.LDAP.AuthCacheSize, cfg.LDAP.AuthCacheTTL)

	// HTTP server
	srv := api.NewServer(cfg.HTTP, writerQ, jobQueue, logger, ldapAuth, cfg.LDAP.Roles)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}

	wg.Wait()
	logger.Info("notification relay stopped")
}

func newLogger(cfg config.LoggingConfig) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
