package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"notification_relay/db"
)

type eventResponse struct {
	ID               int64  `json:"id"`
	EventID          string `json:"event_id"`
	EventURL         string `json:"event_url,omitempty"`
	EventName        string `json:"event_name,omitempty"`
	EventDescription string `json:"event_description,omitempty"`
	EventSeverity    string `json:"event_severity,omitempty"`
	StartTime        string `json:"start_time"`
	EndTime          string `json:"end_time,omitempty"`
}

type notificationResponse struct {
	ID             int64    `json:"id"`
	NotificationID string   `json:"notification_id"`
	EventID        string   `json:"event_id"`
	Groups         []string `json:"groups"`
	Channels       []string `json:"channels"`
	Message        string   `json:"message"`
	MemberCount    int64    `json:"member_count"`
	CreatedAt      string   `json:"created_at"`
}

func toEventResponse(e db.Event) eventResponse {
	return eventResponse{
		ID:               e.ID,
		EventID:          e.EventID,
		EventURL:         e.EventUrl.String,
		EventName:        e.EventName.String,
		EventDescription: e.EventDescription.String,
		EventSeverity:    e.EventSeverity.String,
		StartTime:        e.StartTime,
		EndTime:          e.EndTime.String,
	}
}

func toNotificationResponse(n db.Notification) (notificationResponse, error) {
	var groups []string
	if err := json.Unmarshal([]byte(n.Groups), &groups); err != nil {
		return notificationResponse{}, err
	}
	var channels []string
	if err := json.Unmarshal([]byte(n.Channels), &channels); err != nil {
		return notificationResponse{}, err
	}
	return notificationResponse{
		ID:             n.ID,
		NotificationID: n.NotificationID,
		EventID:        n.EventID,
		Groups:         groups,
		Channels:       channels,
		Message:        n.Message,
		MemberCount:    n.MemberCount,
		CreatedAt:      n.CreatedAt,
	}, nil
}

// handleCreateEvent creates a new event. Returns 409 if the event_id already exists.
func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventID          string `json:"event_id"`
		EventURL         string `json:"event_url"`
		EventName        string `json:"event_name"`
		EventDescription string `json:"event_description"`
		EventSeverity    string `json:"event_severity"`
		StartTime        string `json:"start_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.EventID == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Check for duplicate event_id.
	if _, err := s.q.GetEventByEventID(ctx, req.EventID); err == nil {
		http.Error(w, "event already exists", http.StatusConflict)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("create event: check existing failed", "event_id", req.EventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	startTime := req.StartTime
	if startTime == "" {
		startTime = time.Now().UTC().Format(time.RFC3339)
	}

	event, err := s.q.InsertEvent(ctx, db.InsertEventParams{
		EventID:          req.EventID,
		EventUrl:         nullString(req.EventURL),
		EventName:        nullString(req.EventName),
		EventDescription: nullString(req.EventDescription),
		EventSeverity:    nullString(req.EventSeverity),
		StartTime:        startTime,
	})
	if err != nil {
		s.logger.Error("create event: insert failed", "event_id", req.EventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toEventResponse(event))
}

// handleListEvents returns a paginated list of events ordered by start_time DESC.
// Query params: limit (default 50, max 200), offset (default 0).
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	offset := int64(0)

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "offset must be a non-negative integer", http.StatusBadRequest)
			return
		}
		offset = n
	}

	events, err := s.q.ListEvents(r.Context(), db.ListEventsParams{Limit: limit, Offset: offset})
	if err != nil {
		s.logger.Error("list events failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := make([]eventResponse, len(events))
	for i, e := range events {
		resp[i] = toEventResponse(e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Events []eventResponse `json:"events"`
		Limit  int64           `json:"limit"`
		Offset int64           `json:"offset"`
	}{Events: resp, Limit: limit, Offset: offset})
}

// handleGetEvent returns a single event by event_id.
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")

	event, err := s.q.GetEventByEventID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		s.logger.Error("get event failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toEventResponse(event))
}

// handleEndEvent marks an event as ended by setting its end_time.
// Idempotent: if end_time is already set, returns 200 without modifying.
// Accepts an optional JSON body {"end_time": "RFC3339"} to specify the time.
func (s *Server) handleEndEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")

	event, err := s.q.GetEventByEventID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		s.logger.Error("end event: get failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Idempotent: already ended.
	if event.EndTime.Valid && event.EndTime.String != "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toEventResponse(event))
		return
	}

	endTime := time.Now().UTC().Format(time.RFC3339)
	if r.ContentLength > 0 {
		var body struct {
			EndTime string `json:"end_time"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if body.EndTime != "" {
			endTime = body.EndTime
		}
	}

	if err := s.q.UpdateEventEndTime(r.Context(), db.UpdateEventEndTimeParams{
		EndTime: sql.NullString{String: endTime, Valid: true},
		EventID: eventID,
	}); err != nil {
		s.logger.Error("end event: update failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	event.EndTime = sql.NullString{String: endTime, Valid: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toEventResponse(event))
}

// handleListEventNotifications returns all notifications for a given event.
func (s *Server) handleListEventNotifications(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")

	// Verify event exists.
	if _, err := s.q.GetEventByEventID(r.Context(), eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		s.logger.Error("list event notifications: get event failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	notifications, err := s.q.ListNotificationsByEventID(r.Context(), eventID)
	if err != nil {
		s.logger.Error("list event notifications failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := make([]notificationResponse, 0, len(notifications))
	for _, n := range notifications {
		nr, err := toNotificationResponse(n)
		if err != nil {
			s.logger.Error("list event notifications: decode failed",
				"notification_id", n.NotificationID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp = append(resp, nr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleGetNotification returns a single notification by notification_id.
func (s *Server) handleGetNotification(w http.ResponseWriter, r *http.Request) {
	notificationID := r.PathValue("notification_id")

	notif, err := s.q.GetNotificationByNotificationID(r.Context(), notificationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "notification not found", http.StatusNotFound)
			return
		}
		s.logger.Error("get notification failed", "notification_id", notificationID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	nr, err := toNotificationResponse(notif)
	if err != nil {
		s.logger.Error("get notification: decode failed", "notification_id", notificationID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nr)
}

// handleListNotificationDeliveries returns all deliveries for a given notification.
func (s *Server) handleListNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	notificationID := r.PathValue("notification_id")

	// Verify notification exists.
	if _, err := s.q.GetNotificationByNotificationID(r.Context(), notificationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "notification not found", http.StatusNotFound)
			return
		}
		s.logger.Error("list notification deliveries: get notification failed",
			"notification_id", notificationID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	deliveries, err := s.q.ListDeliveriesByNotificationID(r.Context(), notificationID)
	if err != nil {
		s.logger.Error("list notification deliveries failed",
			"notification_id", notificationID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if deliveries == nil {
		deliveries = []db.Delivery{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}

// handleGetDelivery returns a single delivery by delivery_id.
func (s *Server) handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	deliveryID := r.PathValue("delivery_id")

	delivery, err := s.q.GetDeliveryByDeliveryID(r.Context(), deliveryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "delivery not found", http.StatusNotFound)
			return
		}
		s.logger.Error("get delivery failed", "delivery_id", deliveryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delivery)
}
