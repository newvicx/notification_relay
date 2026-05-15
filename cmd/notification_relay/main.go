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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"notification_relay/api"
	"notification_relay/config"
	"notification_relay/db"
	ldapsync "notification_relay/ldap"
	"notification_relay/notify"
	"notification_relay/smtpapi"
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

	logger, closeLogger, err := newLogger(cfg.Logging)
	if err != nil {
		slog.Error("failed to initialise logger", "error", err)
		os.Exit(1)
	}
	defer closeLogger()

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

	wg.Add(1)
	go func() {
		defer wg.Done()
		poller.RunPollFailed(ctx)
	}()

	// Twilio delivery providers (only constructed when AccountSID is configured)
	var smsProvider notify.SMSProvider
	var voiceProvider notify.VoiceProvider
	if cfg.Twilio.AccountSID != "" {
		smsProvider = notify.NewTwilioSMS(cfg.Twilio)
		voiceProvider = notify.NewTwilioVoice(cfg.Twilio, cfg.Notify.DeliveryTimeout)
	}

	// SMTP email provider (only constructed when host is configured)
	var emailProvider notify.EmailProvider
	if cfg.SMTP.Host != "" {
		emailProvider = notify.NewSMTPEmail(cfg.SMTP, cfg.Notify.DeliveryTimeout)
	}

	// Dispatcher
	dispatcher := notify.NewDispatcher(cfg.Notify, writerQ, jobQueue, smsProvider, voiceProvider, emailProvider, logger)
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

	// LDAP group verifier (used by the API to validate new sync groups)
	groupVerifier := ldapsync.NewGroupVerifier(
		cfg.LDAP.PrimaryURL,
		cfg.LDAP.BackupURL,
		cfg.LDAP.BindDN,
		cfg.LDAP.BindPassword,
		cfg.LDAP.GroupBaseDN,
		cfg.LDAP.GroupFilter,
		cfg.LDAP.TLSSkipVerify,
	)

	// LDAP user lookup (used by the SMS subscription form to fetch mobile numbers)
	userLookup := ldapsync.NewUserLookup(
		cfg.LDAP.PrimaryURL,
		cfg.LDAP.BackupURL,
		cfg.LDAP.BindDN,
		cfg.LDAP.BindPassword,
		cfg.LDAP.UserBaseDN,
		cfg.LDAP.TLSSkipVerify,
	)

	// HTTP server
	srv := api.NewServer(cfg.HTTP, writerQ, jobQueue, logger, ldapAuth, groupVerifier, userLookup, cfg.LDAP.Roles, cfg.Severities)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
		}
	}()

	// SMTP ingestion server (disabled when listen_addr is empty)
	var smtpSrv *smtpapi.SMTPServer
	if cfg.SMTPServer.ListenAddr != "" {
		smtpSrv = smtpapi.NewSMTPServer(cfg.SMTPServer, writerQ, jobQueue, logger, ldapAuth, cfg.LDAP.Roles)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := smtpSrv.Start(); err != nil {
				logger.Error("smtp server error", "error", err)
			}
		}()
	}

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}
	if smtpSrv != nil {
		if err := smtpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("smtp server shutdown error", "error", err)
		}
	}

	wg.Wait()
	logger.Info("notification relay stopped")
}

// newLogger creates a slog.Logger from the logging config. When cfg.Dir is set,
// logs are written to daily-rotating files in that directory (YYYY-MM-DD.log).
// Otherwise logs go to stdout. The returned cleanup function flushes and closes
// any open log file; it must be called before the process exits.
func newLogger(cfg config.LoggingConfig) (*slog.Logger, func(), error) {
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

	if cfg.Dir != "" {
		w, err := newDailyRotatingWriter(cfg.Dir)
		if err != nil {
			return nil, nil, fmt.Errorf("open log dir: %w", err)
		}
		var handler slog.Handler
		if cfg.Format == "text" {
			handler = slog.NewTextHandler(w, opts)
		} else {
			handler = slog.NewJSONHandler(w, opts)
		}
		return slog.New(handler), func() { w.Close() }, nil
	}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler), func() {}, nil
}

// dailyRotatingWriter is an io.Writer that writes to date-stamped files in a
// directory, opening a new file whenever the calendar date advances. All writes
// are serialised through a mutex so the writer is safe for concurrent use by
// multiple slog handlers / goroutines.
type dailyRotatingWriter struct {
	dir  string
	mu   sync.Mutex
	file *os.File
	date string
}

func newDailyRotatingWriter(dir string) (*dailyRotatingWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory %q: %w", dir, err)
	}
	w := &dailyRotatingWriter{dir: dir}
	if err := w.openFile(); err != nil {
		return nil, err
	}
	return w, nil
}

// openFile opens (or creates) today's log file and closes the previous one.
// Caller must hold w.mu.
func (w *dailyRotatingWriter) openFile() error {
	date := time.Now().Format("2006-01-02")
	path := filepath.Join(w.dir, date+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file %q: %w", path, err)
	}
	if w.file != nil {
		w.file.Close()
	}
	w.file = f
	w.date = date
	return nil
}

func (w *dailyRotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if today := time.Now().Format("2006-01-02"); today != w.date {
		if err := w.openFile(); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *dailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
