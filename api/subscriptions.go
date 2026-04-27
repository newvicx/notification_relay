package api

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleListSMSSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.q.ListSMSSubscriptions(r.Context())
	if err != nil {
		s.logger.Error("list sms subscriptions failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(subs)
}

func (s *Server) handleDeleteSMSSubscription(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	existing, err := s.q.GetSMSSubscription(r.Context(), username)
	if err != nil {
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	}

	if err := s.q.DeleteSMSSubscription(r.Context(), username); err != nil {
		s.logger.Error("admin delete sms subscription failed", "username", username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "sms_subscription_deleted", "sms_subscriptions", marshalAuditJSON(map[string]string{
		"username": existing.Username,
		"phone":    existing.Phone,
	}), "")

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSelfUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	existing, err := s.q.GetSMSSubscription(r.Context(), user.Username)
	if err != nil {
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	}

	if err := s.q.DeleteSMSSubscription(r.Context(), user.Username); err != nil {
		s.logger.Error("self unsubscribe failed", "username", user.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "sms_unsubscribe", "sms_subscriptions", marshalAuditJSON(map[string]string{
		"username": existing.Username,
		"phone":    existing.Phone,
	}), "")

	w.WriteHeader(http.StatusNoContent)
}
