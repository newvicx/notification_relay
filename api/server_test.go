package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"notification_relay/api"
	"notification_relay/config"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
	"notification_relay/testutil"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubAuth is a configurable stub for ldap.Authenticator.
type stubAuth struct {
	result *ldap.AuthResult
	err    error
}

func (s *stubAuth) AuthenticateUser(_ context.Context, _, _ string) (*ldap.AuthResult, error) {
	return s.result, s.err
}

// roleConfig used across all server tests.
var testRoleConfig = map[string][]string{
	"publisher": {"grp-pub"},
	"reader":    {"grp-reader"},
}

// publisherAuth returns a stub that authenticates successfully as a publisher.
func publisherAuth() *stubAuth {
	return &stubAuth{result: &ldap.AuthResult{UserDN: "CN=pub,DC=example,DC=com", Groups: []string{"grp-pub"}}}
}

// readerAuth returns a stub that authenticates successfully as a reader.
func readerAuth() *stubAuth {
	return &stubAuth{result: &ldap.AuthResult{UserDN: "CN=reader,DC=example,DC=com", Groups: []string{"grp-reader"}}}
}

func newTestServer(t *testing.T, auth ldap.Authenticator) (*api.Server, func()) {
	t.Helper()
	_, q := testutil.OpenDB(t)
	queue := make(chan notify.Job, 16)
	logger := noopLogger()
	srv := api.NewServer(config.HTTPConfig{}, q, queue, logger, auth, testRoleConfig)
	return srv, func() {}
}

func do(srv *api.Server, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.SetBasicAuth("user", "pass")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func doNoAuth(srv *api.Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// ---- Auth middleware tests ----

func TestAuth_NoHeader(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	w := doNoAuth(srv, "GET", "/api/v1/events")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("want WWW-Authenticate header")
	}
}

func TestAuth_InvalidCredentials(t *testing.T) {
	srv, _ := newTestServer(t, &stubAuth{err: ldap.ErrInvalidCredentials})
	w := do(srv, "GET", "/api/v1/events", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuth_LDAPError(t *testing.T) {
	srv, _ := newTestServer(t, &stubAuth{err: errors.New("connection refused")})
	w := do(srv, "GET", "/api/v1/events", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

func TestAuth_InsufficientRole(t *testing.T) {
	srv, _ := newTestServer(t, readerAuth())
	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-001",
		"groups":   []string{"grp-oncall"},
		"channels": []string{"sms"},
		"message":  "test",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

// ---- Event lifecycle tests ----

func TestCreateEvent(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	body, _ := json.Marshal(map[string]any{
		"event_id":   "EVT-001",
		"event_name": "Test Event",
		"start_time": time.Now().UTC().Format(time.RFC3339),
	})
	w := do(srv, "POST", "/api/v1/events", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["event_id"] != "EVT-001" {
		t.Errorf("want event_id=EVT-001, got %v", resp["event_id"])
	}
}

func TestCreateEvent_MissingEventID(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	body, _ := json.Marshal(map[string]any{"event_name": "no id"})
	w := do(srv, "POST", "/api/v1/events", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestCreateEvent_Duplicate(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	body, _ := json.Marshal(map[string]any{"event_id": "EVT-DUP"})

	do(srv, "POST", "/api/v1/events", body)
	w := do(srv, "POST", "/api/v1/events", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", w.Code)
	}
}

func TestListEvents(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	// Create two events.
	for _, id := range []string{"EVT-A", "EVT-B"} {
		body, _ := json.Marshal(map[string]any{"event_id": id})
		do(srv, "POST", "/api/v1/events", body)
	}

	w := do(srv, "GET", "/api/v1/events", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp struct {
		Events []map[string]any `json:"events"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 2 {
		t.Errorf("want 2 events, got %d", len(resp.Events))
	}
}

func TestListEvents_Pagination(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	for _, id := range []string{"EVT-1", "EVT-2", "EVT-3"} {
		body, _ := json.Marshal(map[string]any{"event_id": id})
		do(srv, "POST", "/api/v1/events", body)
	}

	w := do(srv, "GET", "/api/v1/events?limit=2&offset=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Events []map[string]any `json:"events"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) != 2 {
		t.Errorf("want 2 events with limit=2, got %d", len(resp.Events))
	}
}

func TestGetEvent(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	body, _ := json.Marshal(map[string]any{"event_id": "EVT-GET"})
	do(srv, "POST", "/api/v1/events", body)

	w := do(srv, "GET", "/api/v1/events/EVT-GET", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGetEvent_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	w := do(srv, "GET", "/api/v1/events/no-such-event", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestEndEvent(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	body, _ := json.Marshal(map[string]any{"event_id": "EVT-END"})
	do(srv, "POST", "/api/v1/events", body)

	w := do(srv, "POST", "/api/v1/events/EVT-END/end", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["end_time"] == "" || resp["end_time"] == nil {
		t.Error("want end_time to be set")
	}
}

func TestEndEvent_Idempotent(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	body, _ := json.Marshal(map[string]any{"event_id": "EVT-IDEM"})
	do(srv, "POST", "/api/v1/events", body)

	w1 := do(srv, "POST", "/api/v1/events/EVT-IDEM/end", nil)
	if w1.Code != http.StatusOK {
		t.Fatalf("first end: want 200, got %d", w1.Code)
	}
	var r1 map[string]any
	json.NewDecoder(w1.Body).Decode(&r1)

	w2 := do(srv, "POST", "/api/v1/events/EVT-IDEM/end", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("second end: want 200, got %d", w2.Code)
	}
	var r2 map[string]any
	json.NewDecoder(w2.Body).Decode(&r2)

	if r1["end_time"] != r2["end_time"] {
		t.Errorf("end_time changed on second call: %v vs %v", r1["end_time"], r2["end_time"])
	}
}

func TestEndEvent_CustomTime(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	body, _ := json.Marshal(map[string]any{"event_id": "EVT-CT"})
	do(srv, "POST", "/api/v1/events", body)

	endTime := "2025-01-01T12:00:00Z"
	endBody, _ := json.Marshal(map[string]any{"end_time": endTime})
	w := do(srv, "POST", "/api/v1/events/EVT-CT/end", endBody)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["end_time"] != endTime {
		t.Errorf("want end_time=%s, got %v", endTime, resp["end_time"])
	}
}

func TestEndEvent_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	w := do(srv, "POST", "/api/v1/events/no-such/end", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---- Notification / delivery read tests ----

func TestPublishAndGetNotification(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	// Create the event first.
	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-N1"})
	do(srv, "POST", "/api/v1/events", eventBody)

	pubBody, _ := json.Marshal(map[string]any{
		"event_id": "EVT-N1",
		"groups":   []string{"grp-oncall"},
		"channels": []string{"sms"},
		"message":  "test alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", pubBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body)
	}

	var pubResp map[string]any
	json.NewDecoder(w.Body).Decode(&pubResp)
	notifID, ok := pubResp["notification_id"].(string)
	if !ok || notifID == "" {
		t.Fatalf("notification_id missing from publish response: %v", pubResp)
	}

	wGet := do(srv, "GET", "/api/v1/notifications/"+notifID, nil)
	if wGet.Code != http.StatusOK {
		t.Fatalf("get notification: want 200, got %d", wGet.Code)
	}
	var getResp map[string]any
	json.NewDecoder(wGet.Body).Decode(&getResp)
	if getResp["notification_id"] != notifID {
		t.Errorf("notification_id mismatch")
	}
	// Groups and channels should be decoded as arrays, not JSON strings.
	if _, ok := getResp["groups"].([]any); !ok {
		t.Errorf("groups should be an array, got %T", getResp["groups"])
	}
}

func TestListNotificationDeliveries_Empty(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-D1"})
	do(srv, "POST", "/api/v1/events", eventBody)

	pubBody, _ := json.Marshal(map[string]any{
		"event_id": "EVT-D1",
		"groups":   []string{"grp-oncall"},
		"channels": []string{"sms"},
		"message":  "alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", pubBody)
	var pubResp map[string]any
	json.NewDecoder(w.Body).Decode(&pubResp)
	notifID, ok := pubResp["notification_id"].(string)
	if !ok || notifID == "" {
		t.Fatalf("notification_id missing from publish response: %v", pubResp)
	}

	wDel := do(srv, "GET", "/api/v1/notifications/"+notifID+"/deliveries", nil)
	if wDel.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", wDel.Code)
	}
	var deliveries []any
	json.NewDecoder(wDel.Body).Decode(&deliveries)
	// No dispatcher running in tests, so deliveries list is empty (not null).
	if deliveries == nil {
		t.Error("want empty array, not null")
	}
}

func TestGetDelivery_NotFound(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	w := do(srv, "GET", "/api/v1/deliveries/no-such-delivery", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestListEventNotifications(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-EN1"})
	do(srv, "POST", "/api/v1/events", eventBody)

	for range 2 {
		body, _ := json.Marshal(map[string]any{
			"event_id": "EVT-EN1",
			"groups":   []string{"grp-oncall"},
			"channels": []string{"sms"},
			"message":  "alert",
		})
		do(srv, "POST", "/api/v1/notifications", body)
	}

	w := do(srv, "GET", "/api/v1/events/EVT-EN1/notifications", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var notifs []any
	json.NewDecoder(w.Body).Decode(&notifs)
	if len(notifs) != 2 {
		t.Errorf("want 2 notifications, got %d", len(notifs))
	}
}
