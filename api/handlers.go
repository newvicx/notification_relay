package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type publishRequest struct {
	EventID          string   `json:"event_id"`
	EventURL         string   `json:"event_url"`
	EventName        string   `json:"event_name"`
	EventDescription string   `json:"event_description"`
	EventSeverity    string   `json:"event_severity"`
	StartTime        string   `json:"start_time"`
	Groups           []string `json:"groups"`
	Channels         []string `json:"channels"`
	Message          string   `json:"message"`
}

type publishResponse struct {
	NotificationID string   `json:"notification_id"`
	EventID        string   `json:"event_id"`
	Groups         []string `json:"groups"`
	Channels       []string `json:"channels"`
	Message        string   `json:"message"`
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
	if len(req.Groups) == 0 {
		http.Error(w, "groups must be non-empty", http.StatusBadRequest)
		return
	}
	if len(req.Channels) == 0 {
		http.Error(w, "channels must be non-empty", http.StatusBadRequest)
		return
	}
	for _, ch := range req.Channels {
		if !validChannels[ch] {
			http.Error(w, fmt.Sprintf("unknown channel %q; valid values: sms, voice, email", ch), http.StatusBadRequest)
			return
		}
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
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

	// Encode groups and channels as JSON arrays for storage.
	groupsJSON, err := json.Marshal(req.Groups)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	channelsJSON, err := json.Marshal(req.Channels)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	notificationID := newUUIDV7()
	notif, err := s.q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: notificationID,
		EventID:        event.EventID,
		Groups:         string(groupsJSON),
		Channels:       string(channelsJSON),
		Message:        req.Message,
		MemberCount:    0,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
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
		Channels:       req.Channels,
		Message:        req.Message,
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
