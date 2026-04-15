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
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handlePublishNotification))))

	// Events
	mux.Handle("POST /api/v1/events",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handleCreateEvent))))
	mux.Handle("GET /api/v1/events",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListEvents))))
	mux.Handle("GET /api/v1/events/{event_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetEvent))))
	mux.Handle("POST /api/v1/events/{event_id}/end",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handleEndEvent))))
	mux.Handle("GET /api/v1/events/{event_id}/notifications",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListEventNotifications))))

	// Notifications
	mux.Handle("GET /api/v1/notifications/{notification_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetNotification))))
	mux.Handle("GET /api/v1/notifications/{notification_id}/deliveries",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListNotificationDeliveries))))

	// Deliveries
	mux.Handle("GET /api/v1/deliveries/{delivery_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetDelivery))))

	// Groups (LDAP-synced, read-only via API)
	mux.Handle("GET /api/v1/groups",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListGroups))))
	mux.Handle("GET /api/v1/groups/{group_name}/members",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListGroupMembers))))

	// Sync groups (admin-only; controls which LDAP groups the syncer mirrors)
	mux.Handle("GET /api/v1/sync-groups",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleListSyncGroups))))
	mux.Handle("POST /api/v1/sync-groups",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleCreateSyncGroup))))
	mux.Handle("DELETE /api/v1/sync-groups/{group_name}",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleDeleteSyncGroup))))

	// Audit log (admin-only)
	mux.Handle("GET /api/v1/audit",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleListAuditLog))))

	// Email templates (write requires admin; read requires reader)
	mux.Handle("POST /api/v1/templates",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleCreateTemplate))))
	mux.Handle("GET /api/v1/templates",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListTemplates))))
	mux.Handle("GET /api/v1/templates/{template_name}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetTemplate))))
	mux.Handle("PUT /api/v1/templates/{template_name}",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUpdateTemplate))))
	mux.Handle("DELETE /api/v1/templates/{template_name}",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleDeleteTemplate))))
}

// Handler returns the underlying HTTP handler, useful for testing with httptest.
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
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
