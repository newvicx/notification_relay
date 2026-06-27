package smtpapi

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"log/slog"

	gosmtp "github.com/emersion/go-smtp"

	"notification_relay/config"
	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
)

// SMTPServer is an SMTP ingestion server that converts inbound email into
// notification relay jobs. Each RCPT TO recipient encodes an LDAP group target
// and its delivery channels in the address local part ("group+sms+voice") and
// becomes its own notification, so different groups in one message can target
// different channels. The Subject becomes the event name; the body is the
// message. For backward compatibility, channels may instead be supplied via
// the From: header.
type SMTPServer struct {
	cfg        config.SMTPServerConfig
	q          *db.Queries
	queue      chan<- notify.Job
	logger     *slog.Logger
	auth       ldap.Authenticator
	roleConfig map[string][]string
	cramStore  *cramCredentialStore
	srv        *gosmtp.Server
}

// NewSMTPServer constructs an SMTPServer. Call Start to begin accepting
// connections.
func NewSMTPServer(
	cfg config.SMTPServerConfig,
	q *db.Queries,
	queue chan<- notify.Job,
	logger *slog.Logger,
	auth ldap.Authenticator,
	roleConfig map[string][]string,
) *SMTPServer {
	s := &SMTPServer{
		cfg:        cfg,
		q:          q,
		queue:      queue,
		logger:     logger,
		auth:       auth,
		roleConfig: roleConfig,
	}

	if cfg.CRAMMD5Enabled {
		key, err := base64.StdEncoding.DecodeString(cfg.CRAMMD5SecretKey)
		if err != nil {
			// config.validate() already checked this; treat as a startup bug.
			logger.Error("smtp server: invalid cram_md5_secret_key", "error", err)
		} else {
			s.cramStore = newCRAMCredentialStore(q, key)
		}
	}

	srv := gosmtp.NewServer(s)
	srv.Addr = cfg.ListenAddr
	srv.Domain = cfg.Domain
	srv.MaxMessageBytes = cfg.MaxMessageBytes
	srv.AllowInsecureAuth = cfg.TLSMode == "" || cfg.TLSMode == "none"

	if cfg.TLSMode == "starttls" || cfg.TLSMode == "tls" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			logger.Error("smtp server: failed to load TLS cert", "error", err)
		} else {
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		}
	}

	s.srv = srv
	return s
}

// Start begins accepting SMTP connections. It blocks until the server closes.
// In "tls" mode the listener requires a TLS handshake from the first byte
// (implicit TLS, like SMTPS). In "starttls" mode it accepts plaintext
// connections and upgrades them via the STARTTLS command. In "none" mode it
// only ever accepts plaintext connections.
func (s *SMTPServer) Start() error {
	s.logger.Info("smtp server listening", "addr", s.cfg.ListenAddr, "tls_mode", s.cfg.TLSMode)
	if s.cfg.TLSMode == "tls" {
		return s.srv.ListenAndServeTLS()
	}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server, waiting for in-flight sessions to
// finish or until ctx is cancelled.
func (s *SMTPServer) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// NewSession implements smtp.Backend.
func (s *SMTPServer) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{
		s:          s,
		remoteAddr: c.Conn().RemoteAddr().String(),
	}, nil
}
