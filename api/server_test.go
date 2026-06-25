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

// stubGroupVerifier is a configurable stub for ldap.GroupVerifier.
type stubGroupVerifier struct {
	err error
}

func (s *stubGroupVerifier) VerifyGroup(_ context.Context, _ string) error {
	return s.err
}

// okGroupVerifier returns a stub that always reports the group as valid.
func okGroupVerifier() *stubGroupVerifier { return &stubGroupVerifier{} }

// stubUserLookup is a configurable stub for ldap.UserLookup.
type stubUserLookup struct {
	member *ldap.Member
	err    error
}

func (s *stubUserLookup) LookupUser(_ context.Context, _ string) (*ldap.Member, error) {
	return s.member, s.err
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
	srv := api.NewServer(config.HTTPConfig{}, q, queue, logger, auth, okGroupVerifier(), &stubUserLookup{}, testRoleConfig, []string{"test"})
	shutdownOnCleanup(t, srv)
	return srv, func() {}
}

// shutdownOnCleanup stops the server's background goroutines (notably the
// UI session sweeper) when the test finishes, so tests don't leak goroutines.
func shutdownOnCleanup(t *testing.T, srv *api.Server) {
	t.Helper()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
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
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
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

func TestListEvents_Filters(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	create := func(id, name, description, severity, status string) {
		body, _ := json.Marshal(map[string]any{
			"event_id":          id,
			"event_name":        name,
			"event_description": description,
			"event_severity":    severity,
			"start_time":        time.Now().UTC().Format(time.RFC3339),
		})
		w := do(srv, "POST", "/api/v1/events", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: want 201, got %d: %s", id, w.Code, w.Body)
		}
		if status == "ended" {
			w := do(srv, "POST", "/api/v1/events/"+id+"/end", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("end %s: want 200, got %d: %s", id, w.Code, w.Body)
			}
		}
	}

	create("EVT-DISK", "Disk usage high", "disk nearly full on host1", "test", "active")
	create("EVT-NET", "Network blip", "transient packet loss", "", "ended")

	cases := []struct {
		name      string
		query     string
		wantCode  int
		wantCount int
	}{
		{"event_name substring", "event_name=disk", http.StatusOK, 1},
		{"event_name case-insensitive", "event_name=DISK", http.StatusOK, 1},
		{"description substring", "description=packet", http.StatusOK, 1},
		{"severity exact match", "severity=test", http.StatusOK, 1},
		{"severity invalid", "severity=bogus", http.StatusBadRequest, 0},
		{"status active", "status=active", http.StatusOK, 1},
		{"status ended", "status=ended", http.StatusOK, 1},
		{"status invalid", "status=bogus", http.StatusBadRequest, 0},
		{"event_id substring", "event_id=net", http.StatusOK, 1},
		{"no match", "event_name=nonexistent", http.StatusOK, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(srv, "GET", "/api/v1/events?"+tc.query, nil)
			if w.Code != tc.wantCode {
				t.Fatalf("want %d, got %d: %s", tc.wantCode, w.Code, w.Body)
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			var resp struct {
				Events []map[string]any `json:"events"`
			}
			json.NewDecoder(w.Body).Decode(&resp)
			if len(resp.Events) != tc.wantCount {
				t.Errorf("want %d events, got %d", tc.wantCount, len(resp.Events))
			}
		})
	}
}

func TestUIListEvents_FiltersRendered(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	loginBody := "username=pub&password=pass"
	req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader([]byte(loginBody)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login: want 303, got %d: %s", w.Code, w.Body)
	}
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		sessionCookie = c
	}
	if sessionCookie == nil {
		t.Fatal("login: no session cookie set")
	}

	req = httptest.NewRequest("GET", "/ui/events?event_name=disk&severity=test&status=active&created_by=jdoe", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	body := w.Body.String()
	for _, want := range []string{
		`value="disk"`,
		`value="jdoe"`,
		`<option value="test" selected>test</option>`,
		`<option value="active" selected>Active</option>`,
	} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("response body missing %q\nbody:\n%s", want, body)
		}
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

	endTime := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
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

func TestPublish_DestinationsOnly(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-DONLY"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-DONLY",
		"destinations": []map[string]any{
			{"channel": "sms", "target": "+12125550001"},
		},
		"message": "direct alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["notification_id"].(string); !ok {
		t.Errorf("notification_id missing from response: %v", resp)
	}
	dests, ok := resp["destinations"].([]any)
	if !ok || len(dests) != 1 {
		t.Errorf("want destinations array of length 1, got %v", resp["destinations"])
	}
}

func TestPublish_GroupsAndDestinations(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-BOTH"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-BOTH",
		"groups":   []string{"grp-oncall"},
		"channels": []string{"sms"},
		"destinations": []map[string]any{
			{"channel": "sms", "target": "+12125550002"},
		},
		"message": "combined alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_MissingBoth(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-MISS"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-MISS",
		"message":  "alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_ChannelsRequiredWithGroups(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-CHNL"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-CHNL",
		"groups":   []string{"grp-oncall"},
		"message":  "alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_InvalidDestinationChannel(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-DCHAN"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-DCHAN",
		"destinations": []map[string]any{
			{"channel": "fax", "target": "+12125550003"},
		},
		"message": "alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_InvalidDestinationTarget_Email(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-DEMAIL"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-DEMAIL",
		"destinations": []map[string]any{
			{"channel": "email", "target": "notanemail"},
		},
		"message":        "alert",
		"email_template": "tmpl",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_InvalidDestinationTarget_Phone(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-DPHONE"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-DPHONE",
		"destinations": []map[string]any{
			{"channel": "sms", "target": "12345"},
		},
		"message": "alert",
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestPublish_EmailDestinationRequiresTemplate(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())

	eventBody, _ := json.Marshal(map[string]any{"event_id": "EVT-ETMPL"})
	do(srv, "POST", "/api/v1/events", eventBody)

	body, _ := json.Marshal(map[string]any{
		"event_id": "EVT-ETMPL",
		"destinations": []map[string]any{
			{"channel": "email", "target": "user@example.com"},
		},
		"message": "alert",
		// no email_template
	})
	w := do(srv, "POST", "/api/v1/notifications", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
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

// ---- Event audit log tests ----

func TestCreateEvent_AuditLog(t *testing.T) {
	srv, q := newAdminServerWithQ(t)
	before := time.Now().Add(-time.Second)

	body, _ := json.Marshal(map[string]any{"event_id": "EVT-AUDIT-CREATE"})
	w := do(srv, "POST", "/api/v1/events", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}

	entry := findAuditEntry(t, q, "create_event", before)
	if entry == nil {
		t.Fatal("expected create_event audit entry, none found")
	}
	if !entry.NewValues.Valid {
		t.Fatal("expected new_values to be set")
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(entry.NewValues.String), &snap); err != nil {
		t.Fatalf("new_values is not valid JSON: %v", err)
	}
	if snap["event_id"] != "EVT-AUDIT-CREATE" {
		t.Errorf("new_values.event_id = %v, want %q", snap["event_id"], "EVT-AUDIT-CREATE")
	}
	if entry.OldValues.Valid {
		t.Error("expected old_values to be NULL for create")
	}
}

func TestEndEvent_AuditLog(t *testing.T) {
	srv, q := newAdminServerWithQ(t)

	createBody, _ := json.Marshal(map[string]any{"event_id": "EVT-AUDIT-END"})
	do(srv, "POST", "/api/v1/events", createBody)

	before := time.Now().Add(-time.Second)
	w := do(srv, "POST", "/api/v1/events/EVT-AUDIT-END/end", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	entry := findAuditEntry(t, q, "end_event", before)
	if entry == nil {
		t.Fatal("expected end_event audit entry, none found")
	}

	var oldSnap, newSnap map[string]any
	if err := json.Unmarshal([]byte(entry.OldValues.String), &oldSnap); err != nil {
		t.Fatalf("old_values is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(entry.NewValues.String), &newSnap); err != nil {
		t.Fatalf("new_values is not valid JSON: %v", err)
	}
	if oldSnap["event_id"] != "EVT-AUDIT-END" {
		t.Errorf("old_values.event_id = %v, want %q", oldSnap["event_id"], "EVT-AUDIT-END")
	}
	// old snapshot should have no end_time; new snapshot should have end_time set
	oldEndTime, _ := oldSnap["end_time"].(map[string]any)
	if oldEndTime != nil && oldEndTime["Valid"] == true {
		t.Error("expected end_time to be unset in old_values snapshot")
	}
	newEndTime, _ := newSnap["end_time"].(map[string]any)
	if newEndTime == nil || newEndTime["Valid"] != true {
		t.Error("expected end_time to be set in new_values snapshot")
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
