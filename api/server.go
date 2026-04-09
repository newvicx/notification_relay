package api

import (
	"context"
	"log/slog"
	"net/http"

	"notification_relay/config"
	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
)

// Server is the HTTP API server.
type Server struct {
	cfg        config.HTTPConfig
	q          *db.Queries
	queue      chan<- notify.Job
	logger     *slog.Logger
	srv        *http.Server
	auth       ldap.Authenticator
	roleConfig map[string][]string
}

func NewServer(cfg config.HTTPConfig, q *db.Queries, queue chan<- notify.Job, logger *slog.Logger, auth ldap.Authenticator, roleConfig map[string][]string) *Server {
	s := &Server{
		cfg:        cfg,
		q:          q,
		queue:      queue,
		logger:     logger,
		auth:       auth,
		roleConfig: roleConfig,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.srv = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}
	return s
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/notifications",
		s.authenticate(
			s.requirePermissions(PermPublish)(
				http.HandlerFunc(s.handlePublishNotification),
			),
		),
	)
}

// Start begins serving HTTP requests. It blocks until the server is shut down.
func (s *Server) Start() error {
	s.logger.Info("http server listening", "addr", s.cfg.ListenAddr)
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server, waiting up to ShutdownTimeout for
// in-flight requests to complete.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
