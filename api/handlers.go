package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"notification_relay/db"
	"notification_relay/notify"
)

// validChannels is the set of channel names accepted by the publish endpoint.
var validChannels = map[string]bool{
	"sms":   true,
	"voice": true,
	"email": true,
}

var (
	reEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	rePhone = regexp.MustCompile(`^\+[1-9]\d{6,14}$`) // E.164
)

// Destination is a direct notification target for a single channel.
type Destination struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
}

type publishRequest struct {
	EventID          string            `json:"event_id"`
	EventURL         string            `json:"event_url"`
	EventName        string            `json:"event_name"`
	EventDescription string            `json:"event_description"`
	EventSeverity    string            `json:"event_severity"`
	StartTime        string            `json:"start_time"`
	EndTime          string            `json:"end_time"`
	Groups           []string          `json:"groups"`
	Destinations     []Destination     `json:"destinations"`
	Channels         []string          `json:"channels"`
	Message          string            `json:"message"`
	EmailTemplate    string            `json:"email_template"`
	EmailVars        map[string]string `json:"email_vars"`
}

type publishResponse struct {
	NotificationID string        `json:"notification_id"`
	EventID        string        `json:"event_id"`
	Groups         []string      `json:"groups,omitempty"`
	Destinations   []Destination `json:"destinations,omitempty"`
	Channels       []string      `json:"channels,omitempty"`
	Message        string        `json:"message"`
	Status         string        `json:"status"`
}

// handlePublishNotification accepts a notification job and queues it for async delivery.
// Authentication and authorization are enforced by the requirePermission middleware
// before this handler is reached. Use UserFromContext to retrieve the caller's identity.
func (s *Server) handlePublishNotification(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// Validate required fields.
	if req.EventID == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}
	if len(req.Groups) == 0 && len(req.Destinations) == 0 {
		http.Error(w, "at least one group or destination is required", http.StatusBadRequest)
		return
	}
	if len(req.Groups) > 0 && len(req.Channels) == 0 {
		http.Error(w, "channels must be non-empty when groups are specified", http.StatusBadRequest)
		return
	}
	for _, ch := range req.Channels {
		if !validChannels[ch] {
			http.Error(w, fmt.Sprintf("unknown channel %q; valid values: sms, voice, email", ch), http.StatusBadRequest)
			return
		}
	}
	for i, dest := range req.Destinations {
		if !validChannels[dest.Channel] {
			http.Error(w, fmt.Sprintf("destinations[%d]: unknown channel %q; valid values: sms, voice, email", i, dest.Channel), http.StatusBadRequest)
			return
		}
		if dest.Target == "" {
			http.Error(w, fmt.Sprintf("destinations[%d]: target is required", i), http.StatusBadRequest)
			return
		}
		if dest.Channel == "email" && !reEmail.MatchString(dest.Target) {
			http.Error(w, fmt.Sprintf("destinations[%d]: invalid email address", i), http.StatusBadRequest)
			return
		}
		if (dest.Channel == "sms" || dest.Channel == "voice") && !rePhone.MatchString(dest.Target) {
			http.Error(w, fmt.Sprintf("destinations[%d]: invalid phone number (E.164 format required, e.g. +12125551234)", i), http.StatusBadRequest)
			return
		}
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Email channel requires a template — check both group channels and destination channels.
	hasEmail := false
	for _, ch := range req.Channels {
		if ch == "email" {
			hasEmail = true
			break
		}
	}
	if !hasEmail {
		for _, dest := range req.Destinations {
			if dest.Channel == "email" {
				hasEmail = true
				break
			}
		}
	}
	if hasEmail && req.EmailTemplate == "" {
		http.Error(w, "email_template is required when email channel is specified", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Resolve or create the event record.
	event, err := s.q.GetEventByEventID(ctx, req.EventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("publish: get event failed", "event_id", req.EventID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		startTime := req.StartTime
		if startTime == "" {
			startTime = time.Now().UTC().Format(time.RFC3339)
		}
		event, err = s.q.InsertEvent(ctx, db.InsertEventParams{
			EventID:          req.EventID,
			EventUrl:         nullString(req.EventURL),
			EventName:        nullString(req.EventName),
			EventDescription: nullString(req.EventDescription),
			EventSeverity:    nullString(req.EventSeverity),
			StartTime:        startTime,
		})
		if err != nil {
			s.logger.Error("publish: insert event failed", "event_id", req.EventID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Mark the event as ended if end_time was supplied and not already set.
	if req.EndTime != "" && (!event.EndTime.Valid || event.EndTime.String == "") {
		if err := s.q.UpdateEventEndTime(ctx, db.UpdateEventEndTimeParams{
			EndTime: sql.NullString{String: req.EndTime, Valid: true},
			EventID: event.EventID,
		}); err != nil {
			s.logger.Error("publish: update event end time failed", "event_id", event.EventID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		event.EndTime = sql.NullString{String: req.EndTime, Valid: true}
	}

	// Encode groups, destinations, and channels as JSON for storage.
	var groupsJSON sql.NullString
	if len(req.Groups) > 0 {
		b, err := json.Marshal(req.Groups)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		groupsJSON = sql.NullString{String: string(b), Valid: true}
	}
	var destinationsJSON sql.NullString
	if len(req.Destinations) > 0 {
		b, err := json.Marshal(req.Destinations)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		destinationsJSON = sql.NullString{String: string(b), Valid: true}
	}
	channelsJSON, err := json.Marshal(req.Channels)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var emailVarsJSON sql.NullString
	if len(req.EmailVars) > 0 {
		b, err := json.Marshal(req.EmailVars)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		emailVarsJSON = sql.NullString{String: string(b), Valid: true}
	}

	notificationID := newUUIDV7()
	notif, err := s.q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: notificationID,
		EventID:        event.EventID,
		Groups:         groupsJSON,
		Destinations:   destinationsJSON,
		Channels:       string(channelsJSON),
		Message:        req.Message,
		MemberCount:    0,
		EmailTemplate:  nullString(req.EmailTemplate),
		EmailVars:      emailVarsJSON,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Status:         "pending",
	})
	if err != nil {
		s.logger.Error("publish: insert notification failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Enqueue the job non-blocking; reject if the queue is full.
	select {
	case s.queue <- notify.Job{NotificationID: notif.NotificationID}:
	default:
		s.logger.Error("publish: job queue full", "notification_id", notif.NotificationID)
		http.Error(w, "server busy, try again later", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(publishResponse{
		NotificationID: notif.NotificationID,
		EventID:        event.EventID,
		Groups:         req.Groups,
		Destinations:   req.Destinations,
		Channels:       req.Channels,
		Message:        req.Message,
		Status:         notif.Status,
	})
}

// newUUIDV7 returns a UUID v7 string (time-ordered, collision-resistant).
func newUUIDV7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("api: crypto/rand unavailable: " + err.Error())
	}
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

