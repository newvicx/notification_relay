package api

import (
	"net/http"
)

// handlePublishNotification accepts a notification job and queues it for async delivery.
// Authentication and authorization are enforced by the requirePermission middleware
// before this handler is reached. Use UserFromContext to retrieve the caller's identity.
func (s *Server) handlePublishNotification(w http.ResponseWriter, r *http.Request) {
	// TODO: decode and validate request body
	// TODO: insert event + notification rows
	// TODO: enqueue notify.Job
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
