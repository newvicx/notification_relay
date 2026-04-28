package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/flowchartsman/swaggerui"

	"notification_relay/config"
	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
)

// Server is the HTTP API server.
type Server struct {
	cfg             config.HTTPConfig
	q               *db.Queries
	queue           chan<- notify.Job
	logger          *slog.Logger
	srv             *http.Server
	auth            ldap.Authenticator
	groupVerifier   ldap.GroupVerifier
	userLookup      ldap.UserLookup
	roleConfig      map[string][]string
	eventSeverities []string
}

func NewServer(cfg config.HTTPConfig, q *db.Queries, queue chan<- notify.Job, logger *slog.Logger, auth ldap.Authenticator, groupVerifier ldap.GroupVerifier, userLookup ldap.UserLookup, roleConfig map[string][]string, eventSeverities []string) *Server {
	s := &Server{
		cfg:             cfg,
		q:               q,
		queue:           queue,
		logger:          logger,
		auth:            auth,
		groupVerifier:   groupVerifier,
		userLookup:      userLookup,
		roleConfig:      roleConfig,
		eventSeverities: eventSeverities,
	}
	mux := http.NewServeMux()
	if fileExists(cfg.SpecPath) {
		spec, err := os.ReadFile("openapi.yaml")
		if err != nil {
			logger.Error("Failed to read openapi.yaml", "error", err)
		} else {
			mux.Handle("GET /docs", swaggerui.Handler(spec))
		}
	}
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
	// Events
	mux.Handle("POST /api/v1/events",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handleCreateEvent))))
	mux.Handle("GET /api/v1/events",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListEvents))))
	mux.Handle("GET /api/v1/events/{event_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetEvent))))
	mux.Handle("PATCH /api/v1/events/{event_id}",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handleUpdateEvent))))
	mux.Handle("POST /api/v1/events/{event_id}/end",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handleEndEvent))))
	mux.Handle("GET /api/v1/events/{event_id}/notifications",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListEventNotifications))))

	// Notifications
	mux.Handle("POST /api/v1/notifications",
		s.authenticate(s.requirePermissions(PermPublish)(http.HandlerFunc(s.handlePublishNotification))))
	mux.Handle("GET /api/v1/notifications/{notification_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetNotification))))
	mux.Handle("GET /api/v1/notifications/{notification_id}/deliveries",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListNotificationDeliveries))))

	// Deliveries
	mux.Handle("GET /api/v1/deliveries/{delivery_id}",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleGetDelivery))))

	// Groups: read-only membership view (all authenticated users)
	mux.Handle("GET /api/v1/groups",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListGroups))))
	mux.Handle("GET /api/v1/groups/{group_name}/members",
		s.authenticate(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleListGroupMembers))))

	// Groups: sync configuration (admin-only; controls which LDAP groups the syncer mirrors)
	mux.Handle("GET /api/v1/groups/sync",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleListSyncGroups))))
	mux.Handle("POST /api/v1/groups/sync",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleCreateSyncGroup))))
	mux.Handle("DELETE /api/v1/groups/sync/{group_name}",
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

	// SMS subscription self-service forms (form-based auth, no middleware)
	mux.HandleFunc("GET /subscribe", s.handleSubscribeForm)
	mux.HandleFunc("POST /subscribe", s.handleSubscribeSubmit)
	mux.HandleFunc("GET /unsubscribe", s.handleUnsubscribeForm)
	mux.HandleFunc("POST /unsubscribe", s.handleUnsubscribeSubmit)

	// SMS subscription API (admin manages all; authenticated users manage their own)
	mux.Handle("GET /api/v1/sms-subscriptions",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleListSMSSubscriptions))))
	mux.Handle("POST /api/v1/sms-subscriptions",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleAdminSubscribe))))
	mux.Handle("POST /api/v1/sms-subscriptions/me",
		s.authenticate(http.HandlerFunc(s.handleSelfSubscribe)))
	mux.Handle("DELETE /api/v1/sms-subscriptions/me",
		s.authenticate(http.HandlerFunc(s.handleSelfUnsubscribe)))
	mux.Handle("DELETE /api/v1/sms-subscriptions/{username}",
		s.authenticate(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleDeleteSMSSubscription))))
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

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err == nil {
		return true // File exists
	}
	if errors.Is(err, os.ErrNotExist) {
		return false // File does not exist
	}
	return false // Other error (permission denied, etc.)
}
