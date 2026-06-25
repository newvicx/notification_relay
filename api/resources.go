package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"notification_relay/db"
)

type eventResponse struct {
	ID               int64  `json:"id"`
	EventID          string `json:"event_id"`
	EventURL         string `json:"event_url"`
	EventName        string `json:"event_name"`
	EventDescription string `json:"event_description"`
	EventSeverity    string `json:"event_severity"`
	StartTime        string `json:"start_time"`
	EndTime          string `json:"end_time"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	ModifiedBy       string `json:"modified_by"`
	ModifiedAt       string `json:"modified_at"`
}

type notificationResponse struct {
	ID             int64         `json:"id"`
	NotificationID string        `json:"notification_id"`
	EventID        string        `json:"event_id"`
	Groups         []string      `json:"groups"`
	Destinations   []Destination `json:"destinations"`
	Channels       []string      `json:"channels"`
	Message        string        `json:"message"`
	MemberCount    int64         `json:"member_count"`
	Status         string        `json:"status"`
	ErrorMessage   string        `json:"error_message"`
	CreatedAt      string        `json:"created_at"`
	CreatedBy      string        `json:"created_by"`
}

type deliveryResponse struct {
	ID             int64          `json:"id"`
	DeliveryID     string         `json:"delivery_id"`
	NotificationID string         `json:"notification_id"`
	Group          string         `json:"group"`
	Member         string         `json:"member"`
	Destination    string         `json:"destination"`
	Channel        string         `json:"channel"`
	Status         string         `json:"status"`
	EmailTemplate  string         `json:"email_template"`
	EmailVars      map[string]any `json:"email_vars"`
	Attempt        int64          `json:"attempt"`
	PollAttempts   int64          `json:"poll_attempts"`
	ErrorMessage   string         `json:"error_message"`
	SentAt         string         `json:"sent_at"`
	CompletedAt    string         `json:"completed_at"`
	TwilioSID      string         `json:"twilio_sid"`
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
		CreatedBy:        e.CreatedBy.String,
		CreatedAt:        e.CreatedAt,
		ModifiedBy:       e.ModifiedBy.String,
		ModifiedAt:       e.ModifiedAt.String,
	}
}

func toNotificationResponse(n db.Notification) (notificationResponse, error) {
	groups := []string{}
	if n.Groups.Valid && n.Groups.String != "" {
		if err := json.Unmarshal([]byte(n.Groups.String), &groups); err != nil {
			return notificationResponse{}, err
		}
	}
	destinations := []Destination{}
	if n.Destinations.Valid && n.Destinations.String != "" {
		if err := json.Unmarshal([]byte(n.Destinations.String), &destinations); err != nil {
			return notificationResponse{}, err
		}
	}
	channels := []string{}
	if n.Channels.Valid && n.Channels.String != "" {
		if err := json.Unmarshal([]byte(n.Channels.String), &channels); err != nil {
			return notificationResponse{}, err
		}
	}
	return notificationResponse{
		ID:             n.ID,
		NotificationID: n.NotificationID,
		EventID:        n.EventID,
		Groups:         groups,
		Destinations:   destinations,
		Channels:       channels,
		Message:        n.Message,
		MemberCount:    n.MemberCount,
		Status:         n.Status,
		ErrorMessage:   n.ErrorMessage.String,
		CreatedAt:      n.CreatedAt,
		CreatedBy:      n.CreatedBy.String,
	}, nil
}

func toDeliveryResponse(d db.Delivery) (deliveryResponse, error) {
	emailVars := map[string]any{}
	if d.EmailVars.Valid {
		if err := json.Unmarshal([]byte(d.EmailVars.String), &emailVars); err != nil {
			return deliveryResponse{}, err
		}
	}

	return deliveryResponse{
		ID:             d.ID,
		DeliveryID:     d.DeliveryID,
		NotificationID: d.NotificationID,
		Group:          d.Group.String,
		Member:         d.Member.String,
		Destination:    d.Destination.String,
		Channel:        d.Channel,
		Status:         d.Status,
		EmailTemplate:  d.EmailTemplate.String,
		EmailVars:      emailVars,
		Attempt:        d.Attempt,
		PollAttempts:   d.PollAttempts,
		ErrorMessage:   d.ErrorMessage.String,
		SentAt:         d.SentAt,
		CompletedAt:    d.CompletedAt.String,
		TwilioSID:      d.TwilioSid.String,
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
		req.EventID = newUUIDV7()
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

	if req.EventSeverity != "" {
		validSeverity := false
		for _, severity := range s.eventSeverities {
			if strings.ToLower(severity) == strings.ToLower(req.EventSeverity) {
				req.EventSeverity = severity
				validSeverity = true
				break
			}
		}
		if !validSeverity {
			valid := strings.Join(s.eventSeverities, ",")
			http.Error(w, fmt.Sprintf("unknown severity %q; valid values: %s", req.EventSeverity, valid), http.StatusBadRequest)
			return
		}
	}

	startTime := req.StartTime
	if startTime == "" {
		startTime = time.Now().UTC().Format(time.RFC3339)
	} else {
		var parseErr error
		startTime, parseErr = parseTimeToRFC3339(startTime)
		if parseErr != nil {
			http.Error(w, "invalid start_time: "+parseErr.Error(), http.StatusBadRequest)
			return
		}
	}

	user, _ := UserFromContext(ctx)
	createdBy := ""
	if user != nil {
		createdBy = user.Username
	}

	event, err := s.q.InsertEvent(ctx, db.InsertEventParams{
		EventID:          req.EventID,
		EventUrl:         nullString(req.EventURL),
		EventName:        nullString(req.EventName),
		EventDescription: nullString(req.EventDescription),
		EventSeverity:    nullString(req.EventSeverity),
		StartTime:        startTime,
		CreatedBy:        nullString(createdBy),
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.logger.Error("create event: insert failed", "event_id", req.EventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "create_event", "events", "", marshalAuditJSON(event))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toEventResponse(event))
}

// handleUpdateEvent updates mutable fields on an existing event.
func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")

	oldEvent, err := s.q.GetEventByEventID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		s.logger.Error("update event: get failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var req struct {
		EventURL         *string `json:"event_url"`
		EventName        *string `json:"event_name"`
		EventDescription *string `json:"event_description"`
		EventSeverity    *string `json:"event_severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.EventSeverity != nil {
		validSeverity := false
		for _, severity := range s.eventSeverities {
			if strings.ToLower(severity) == strings.ToLower(*req.EventSeverity) {
				req.EventSeverity = &severity
				validSeverity = true
				break
			}
		}
		if !validSeverity {
			valid := strings.Join(s.eventSeverities, ",")
			http.Error(w, fmt.Sprintf("unknown severity %q; valid values: %s", *req.EventSeverity, valid), http.StatusBadRequest)
			return
		}
	}

	// Merge: use existing value for any field not provided in the request.
	eventURL := oldEvent.EventUrl.String
	if req.EventURL != nil {
		eventURL = *req.EventURL
	}
	eventName := oldEvent.EventName.String
	if req.EventName != nil {
		eventName = *req.EventName
	}
	eventDescription := oldEvent.EventDescription.String
	if req.EventDescription != nil {
		eventDescription = *req.EventDescription
	}
	eventSeverity := oldEvent.EventSeverity.String
	if req.EventSeverity != nil {
		eventSeverity = *req.EventSeverity
	}

	user, _ := UserFromContext(r.Context())
	modifiedBy := ""
	if user != nil {
		modifiedBy = user.Username
	}

	updated, err := s.q.UpdateEvent(r.Context(), db.UpdateEventParams{
		EventUrl:         nullString(eventURL),
		EventName:        nullString(eventName),
		EventDescription: nullString(eventDescription),
		EventSeverity:    nullString(eventSeverity),
		ModifiedBy:       nullString(modifiedBy),
		ModifiedAt:       nullString(time.Now().UTC().Format(time.RFC3339)),
		EventID:          eventID,
	})
	if err != nil {
		s.logger.Error("update event: update failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.auditLogAction(r, "update_event", "events", marshalAuditJSON(oldEvent), marshalAuditJSON(updated))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toEventResponse(updated))
}

// listEventsCore returns a paginated list of events ordered by start_time DESC.
// Query params: limit (default 50, max 200), offset (default 0),
// start_from (RFC3339, inclusive lower bound on start_time),
// start_to (RFC3339, inclusive upper bound on start_time),
// event_id, event_name, description, created_by (case-insensitive substring match),
// severity (exact match against configured severities),
// status (active = no end_time, ended = end_time set).
// Shared by the JSON API and the UI events list page.
func (s *Server) listEventsCore(r *http.Request) ([]db.Event, int64, int64, error) {
	limit := int64(50)
	offset := int64(0)

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 200 {
			return nil, 0, 0, newCoreError(http.StatusBadRequest, "limit must be an integer between 1 and 200")
		}
		limit = n
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, 0, 0, newCoreError(http.StatusBadRequest, "offset must be a non-negative integer")
		}
		offset = n
	}

	startFrom := ""
	startTo := ""

	if v := r.URL.Query().Get("start_from"); v != "" {
		normalized, err := parseTimeToRFC3339(v)
		if err != nil {
			return nil, 0, 0, newCoreError(http.StatusBadRequest, "invalid start_from: "+err.Error())
		}
		startFrom = normalized
	}
	if v := r.URL.Query().Get("start_to"); v != "" {
		normalized, err := parseTimeToRFC3339(v)
		if err != nil {
			return nil, 0, 0, newCoreError(http.StatusBadRequest, "invalid start_to: "+err.Error())
		}
		startTo = normalized
	}

	eventID := r.URL.Query().Get("event_id")
	eventName := r.URL.Query().Get("event_name")
	description := r.URL.Query().Get("description")
	createdBy := r.URL.Query().Get("created_by")

	severity := r.URL.Query().Get("severity")
	if severity != "" {
		validSeverity := false
		for _, sev := range s.eventSeverities {
			if strings.ToLower(sev) == strings.ToLower(severity) {
				severity = sev
				validSeverity = true
				break
			}
		}
		if !validSeverity {
			valid := strings.Join(s.eventSeverities, ",")
			return nil, 0, 0, newCoreError(http.StatusBadRequest, fmt.Sprintf("unknown severity %q; valid values: %s", severity, valid))
		}
	}

	status := r.URL.Query().Get("status")
	if status != "" && status != "active" && status != "ended" {
		return nil, 0, 0, newCoreError(http.StatusBadRequest, "status must be 'active' or 'ended'")
	}

	events, err := s.q.ListEventsFiltered(r.Context(), db.ListEventsFilteredParams{
		StartFrom:   startFrom,
		StartTo:     startTo,
		EventID:     eventID,
		EventName:   eventName,
		Description: description,
		Severity:    severity,
		CreatedBy:   createdBy,
		Status:      status,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		s.logger.Error("list events failed", "error", err)
		return nil, 0, 0, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	return events, limit, offset, nil
}

// handleListEvents is the JSON API wrapper around listEventsCore.
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	events, limit, offset, err := s.listEventsCore(r)
	if err != nil {
		writeCoreError(w, err)
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

// getEventCore returns a single event by the event_id path value.
// Shared by the JSON API and the UI event detail page.
func (s *Server) getEventCore(r *http.Request) (db.Event, error) {
	eventID := r.PathValue("event_id")

	event, err := s.q.GetEventByEventID(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Event{}, newCoreError(http.StatusNotFound, "event not found")
		}
		s.logger.Error("get event failed", "event_id", eventID, "error", err)
		return db.Event{}, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	return event, nil
}

// handleGetEvent is the JSON API wrapper around getEventCore.
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	event, err := s.getEventCore(r)
	if err != nil {
		writeCoreError(w, err)
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
			var parseErr error
			endTime, parseErr = parseTimeToRFC3339(body.EndTime)
			if parseErr != nil {
				http.Error(w, "invalid end_time: "+parseErr.Error(), http.StatusBadRequest)
				return
			}
			if endTime < event.StartTime {
				http.Error(w, fmt.Sprintf("invalid end_time: cannot be before start_time (%q < %q)", endTime, event.StartTime), http.StatusBadRequest)
				return
			}
		}
	}

	user, _ := UserFromContext(r.Context())
	modifiedBy := ""
	if user != nil {
		modifiedBy = user.Username
	}

	if err := s.q.UpdateEventEndTime(r.Context(), db.UpdateEventEndTimeParams{
		EndTime:    sql.NullString{String: endTime, Valid: true},
		ModifiedBy: nullString(modifiedBy),
		ModifiedAt: nullString(time.Now().UTC().Format(time.RFC3339)),
		EventID:    eventID,
	}); err != nil {
		s.logger.Error("end event: update failed", "event_id", eventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	oldEvent := event
	event.EndTime = sql.NullString{String: endTime, Valid: true}
	s.auditLogAction(r, "end_event", "events", marshalAuditJSON(oldEvent), marshalAuditJSON(event))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toEventResponse(event))
}

// listEventNotificationsCore returns all notifications for a given event.
// Shared by the JSON API and the UI event detail page.
func (s *Server) listEventNotificationsCore(r *http.Request) ([]db.Notification, error) {
	eventID := r.PathValue("event_id")

	// Verify event exists.
	if _, err := s.q.GetEventByEventID(r.Context(), eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, newCoreError(http.StatusNotFound, "event not found")
		}
		s.logger.Error("list event notifications: get event failed", "event_id", eventID, "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	notifications, err := s.q.ListNotificationsByEventID(r.Context(), eventID)
	if err != nil {
		s.logger.Error("list event notifications failed", "event_id", eventID, "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	return notifications, nil
}

// handleListEventNotifications is the JSON API wrapper around listEventNotificationsCore.
func (s *Server) handleListEventNotifications(w http.ResponseWriter, r *http.Request) {
	notifications, err := s.listEventNotificationsCore(r)
	if err != nil {
		writeCoreError(w, err)
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

// listNotificationDeliveriesCore returns all deliveries for a given notification.
// Shared by the JSON API and the UI event detail page's delivery drill-down.
func (s *Server) listNotificationDeliveriesCore(r *http.Request) ([]db.Delivery, error) {
	notificationID := r.PathValue("notification_id")

	// Verify notification exists.
	if _, err := s.q.GetNotificationByNotificationID(r.Context(), notificationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, newCoreError(http.StatusNotFound, "notification not found")
		}
		s.logger.Error("list notification deliveries: get notification failed",
			"notification_id", notificationID, "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	deliveries, err := s.q.ListDeliveriesByNotificationID(r.Context(), notificationID)
	if err != nil {
		s.logger.Error("list notification deliveries failed",
			"notification_id", notificationID, "error", err)
		return nil, newCoreError(http.StatusInternalServerError, "internal server error")
	}

	return deliveries, nil
}

// handleListNotificationDeliveries is the JSON API wrapper around listNotificationDeliveriesCore.
func (s *Server) handleListNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	deliveries, err := s.listNotificationDeliveriesCore(r)
	if err != nil {
		writeCoreError(w, err)
		return
	}

	resp := make([]deliveryResponse, 0, len(deliveries))
	for _, d := range deliveries {
		dr, err := toDeliveryResponse(d)
		if err != nil {
			s.logger.Error("list notification deliveries: decode failed",
				"delivery_id", d.DeliveryID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp = append(resp, dr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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

	dr, err := toDeliveryResponse(delivery)
	if err != nil {
		s.logger.Error("get delivery: decode failed", "delivery_id", deliveryID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dr)
}
