package api_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"notification_relay/db"
)

func insertAuditEntry(t *testing.T, q *db.Queries, username, action, timestamp string) {
	t.Helper()
	err := q.InsertAuditLog(t.Context(), db.InsertAuditLogParams{
		Timestamp:     timestamp,
		Username:      username,
		IpAddress:     sql.NullString{String: "127.0.0.1", Valid: true},
		Action:        action,
		ImpactedTable: "notifications",
		OldValues:     sql.NullString{},
		NewValues:     sql.NullString{},
	})
	if err != nil {
		t.Fatalf("insertAuditEntry: %v", err)
	}
}

func TestListAuditLog_ContainsAuthEntries(t *testing.T) {
	// The authenticate middleware writes a "login" entry on every request, so the
	// audit log is never empty after the first authenticated call.
	srv, _ := newAdminServerWithQ(t)
	w := do(srv, "GET", "/api/v1/audit", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Errorf("want at least 1 audit entry (login), got %v", resp["entries"])
	}
}

func TestListAuditLog_FilterByNonexistentUser(t *testing.T) {
	srv, _ := newAdminServerWithQ(t)
	w := do(srv, "GET", "/api/v1/audit?username=nobody-xyz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) != 0 {
		t.Errorf("want 0 entries for nonexistent user, got %v", resp["entries"])
	}
}

func TestListAuditLog_FilterByUsername(t *testing.T) {
	srv, q := newAdminServerWithQ(t)

	now := time.Now().UTC()
	insertAuditEntry(t, q, "alice", "create_notification", now.Format(time.RFC3339))
	insertAuditEntry(t, q, "bob", "delete_template", now.Add(time.Second).Format(time.RFC3339))
	insertAuditEntry(t, q, "alice", "end_event", now.Add(2*time.Second).Format(time.RFC3339))

	w := do(srv, "GET", "/api/v1/audit?username=alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("want 2 alice entries, got %d", len(entries))
	}
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["username"] != "alice" {
			t.Errorf("unexpected username %v in filtered results", entry["username"])
		}
	}
}

func TestListAuditLog_FilterByTimeRange(t *testing.T) {
	srv, q := newAdminServerWithQ(t)

	base := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	insertAuditEntry(t, q, "alice", "action_early", base.Format(time.RFC3339))
	insertAuditEntry(t, q, "alice", "action_mid", base.Add(time.Hour).Format(time.RFC3339))
	insertAuditEntry(t, q, "alice", "action_late", base.Add(2*time.Hour).Format(time.RFC3339))

	from := base.Add(30 * time.Minute).Format(time.RFC3339)
	to := base.Add(90 * time.Minute).Format(time.RFC3339)

	w := do(srv, "GET", "/api/v1/audit?from="+from+"&to="+to, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	entries := resp["entries"].([]any)
	if len(entries) != 1 {
		t.Errorf("want 1 in-range entry, got %d", len(entries))
	}
	if len(entries) == 1 {
		entry := entries[0].(map[string]any)
		if entry["action"] != "action_mid" {
			t.Errorf("wrong entry returned: %v", entry["action"])
		}
	}
}

func TestAuditLog_ReaderForbidden(t *testing.T) {
	srv, _ := newTestServer(t, readerAuth())
	w := do(srv, "GET", "/api/v1/audit", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader GET audit: want 403, got %d", w.Code)
	}
}
