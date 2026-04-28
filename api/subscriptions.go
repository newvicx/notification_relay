package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"notification_relay/db"
	ldap "notification_relay/ldap"
)

type smsSubscriptionResponse struct {
	Username     string `json:"username"`
	Phone        string `json:"phone"`
	SubscribedAt string `json:"subscribed_at"`
}

func toSMSSubscriptionResponse(s db.SmsSubscription) smsSubscriptionResponse {
	return smsSubscriptionResponse{
		Username:     s.Username,
		Phone:        s.Phone,
		SubscribedAt: s.SubscribedAt,
	}
}

func (s *Server) handleListSMSSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.q.ListSMSSubscriptions(r.Context())
	if err != nil {
		s.logger.Error("list sms subscriptions failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := make([]smsSubscriptionResponse, len(subs))
	for i, sub := range subs {
		resp[i] = toSMSSubscriptionResponse(sub)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAdminSubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	member, err := s.userLookup.LookupUser(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, ldap.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		s.logger.Error("admin subscribe: ldap lookup failed", "username", req.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if member.Mobile == "" {
		http.Error(w, "user has no mobile phone number on record", http.StatusUnprocessableEntity)
		return
	}
	if !rePhone.MatchString(member.Mobile) {
		http.Error(w, "mobile number is not in expected format", http.StatusUnprocessableEntity)
		return
	}

	// Already subscribed — return existing record.
	if existing, err := s.q.GetSMSSubscription(r.Context(), req.Username); err == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toSMSSubscriptionResponse(existing))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.q.InsertSMSSubscription(r.Context(), db.InsertSMSSubscriptionParams{
		Username:     req.Username,
		Phone:        member.Mobile,
		SubscribedAt: now,
	}); err != nil {
		s.logger.Error("admin subscribe: insert failed", "username", req.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "sms_subscribe", "sms_subscriptions", "", marshalAuditJSON(map[string]string{
		"username": req.Username,
		"phone":    member.Mobile,
	}))

	sendWelcomeSMS(r.Context(), s, req.Username, member.Mobile)

	sub, _ := s.q.GetSMSSubscription(r.Context(), req.Username)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toSMSSubscriptionResponse(sub))
}

func (s *Server) handleSelfSubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	member, err := s.userLookup.LookupUser(r.Context(), user.Username)
	if err != nil {
		if errors.Is(err, ldap.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		s.logger.Error("self subscribe: ldap lookup failed", "username", user.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if member.Mobile == "" {
		http.Error(w, "no mobile phone number on record for your account", http.StatusUnprocessableEntity)
		return
	}
	if !rePhone.MatchString(member.Mobile) {
		http.Error(w, "mobile number is not in expected format", http.StatusUnprocessableEntity)
		return
	}

	// Already subscribed — return existing record.
	if existing, err := s.q.GetSMSSubscription(r.Context(), user.Username); err == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toSMSSubscriptionResponse(existing))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.q.InsertSMSSubscription(r.Context(), db.InsertSMSSubscriptionParams{
		Username:     user.Username,
		Phone:        member.Mobile,
		SubscribedAt: now,
	}); err != nil {
		s.logger.Error("self subscribe: insert failed", "username", user.Username, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "sms_subscribe", "sms_subscriptions", "", marshalAuditJSON(map[string]string{
		"username": user.Username,
		"phone":    member.Mobile,
	}))

	sendWelcomeSMS(r.Context(), s, user.Username, member.Mobile)

	sub, _ := s.q.GetSMSSubscription(r.Context(), user.Username)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toSMSSubscriptionResponse(sub))
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

