package api

import (
	"context"
	"errors"
	"html/template"
	"io/fs"
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
	uiSessions      *uiSessionStore
	uiCancel        context.CancelFunc
	uiPages         map[string]*template.Template
	uiFragments     map[string]*template.Template
}

func NewServer(cfg config.HTTPConfig, q *db.Queries, queue chan<- notify.Job, logger *slog.Logger, auth ldap.Authenticator, groupVerifier ldap.GroupVerifier, userLookup ldap.UserLookup, roleConfig map[string][]string, eventSeverities []string) *Server {
	uiCtx, uiCancel := context.WithCancel(context.Background())
	uiPages, uiFragments := mustLoadUITemplates()
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
		uiSessions:      newUISessionStore(),
		uiCancel:        uiCancel,
		uiPages:         uiPages,
		uiFragments:     uiFragments,
	}
	// sweepExpired runs until uiCtx is cancelled, which Shutdown does via
	// s.uiCancel(); main.go calls Shutdown on SIGINT/SIGTERM.
	go s.uiSessions.sweepExpired(uiCtx)
	mux := http.NewServeMux()
	if fileExists(cfg.SpecPath) {
		spec, err := os.ReadFile(cfg.SpecPath)
		if err != nil {
			logger.Error("failed to read openapi.yaml", "error", err)
		} else {
			mux.Handle("GET /docs/", http.StripPrefix("/docs", swaggerui.Handler(spec)))
		}
	} else {
		logger.Info("did not load openAPI spec")
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

	// Web UI (session-cookie auth, reuses the JSON API's RBAC and audit logging)
	mux.HandleFunc("GET /ui/login", s.handleUILoginForm)
	mux.HandleFunc("POST /ui/login", s.handleUILoginSubmit)
	mux.HandleFunc("POST /ui/logout", s.handleUILogout)
	mux.HandleFunc("GET /ui/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/events", http.StatusSeeOther)
	})

	mux.Handle("GET /ui/events",
		s.authenticateSession(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleUIListEvents))))
	mux.Handle("GET /ui/events/{event_id}",
		s.authenticateSession(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleUIGetEvent))))
	mux.Handle("GET /ui/notifications/{notification_id}/deliveries-partial",
		s.authenticateSession(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleUINotificationDeliveriesPartial))))

	mux.Handle("GET /ui/groups/sync",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUIListSyncGroups))))
	mux.Handle("POST /ui/groups/sync",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUICreateSyncGroup))))
	mux.Handle("POST /ui/groups/sync/{group_name}/delete",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUIDeleteSyncGroup))))

	mux.Handle("GET /ui/templates",
		s.authenticateSession(s.requirePermissions(PermRead)(http.HandlerFunc(s.handleUIListTemplates))))
	mux.Handle("GET /ui/templates/new",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUINewTemplateForm))))
	mux.Handle("POST /ui/templates/new",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUICreateTemplate))))
	mux.Handle("GET /ui/templates/{template_name}/edit",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUIEditTemplateForm))))
	mux.Handle("POST /ui/templates/{template_name}/edit",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUIUpdateTemplate))))
	mux.Handle("POST /ui/templates/{template_name}/delete",
		s.authenticateSession(s.requirePermissions(PermAdmin)(http.HandlerFunc(s.handleUIDeleteTemplate))))

	uiStaticSub, err := fs.Sub(uiStaticFS, "ui_static")
	if err != nil {
		s.logger.Error("failed to mount ui static assets", "error", err)
	} else {
		mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServerFS(uiStaticSub)))
	}
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
	s.uiCancel()
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
