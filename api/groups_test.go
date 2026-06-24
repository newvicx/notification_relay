package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"notification_relay/api"
	"notification_relay/config"
	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
	"notification_relay/testutil"
)

// newAdminServerWithQ creates a test server with admin auth and also returns
// the underlying Queries handle so tests can insert data directly.
func newAdminServerWithQ(t *testing.T) (*api.Server, *db.Queries) {
	t.Helper()
	roleConfig := map[string][]string{
		"admin":     {"grp-admins"},
		"publisher": {"grp-pub"},
		"reader":    {"grp-reader"},
	}
	_, q := testutil.OpenDB(t)
	queue := make(chan notify.Job, 16)
	srv := api.NewServer(config.HTTPConfig{}, q, queue, noopLogger(),
		&stubAuth{result: &ldap.AuthResult{UserDN: "CN=admin,DC=example,DC=com", Groups: []string{"grp-admins"}}},
		okGroupVerifier(), &stubUserLookup{}, roleConfig, []string{"test"})
	shutdownOnCleanup(t, srv)
	return srv, q
}

func insertMember(t *testing.T, q *db.Queries, groupName, username string) {
	t.Helper()
	err := q.InsertGroupMember(t.Context(), db.InsertGroupMemberParams{
		GroupName:   groupName,
		Username:    username,
		DisplayName: sql.NullString{String: username + " Display", Valid: true},
		Email:       sql.NullString{String: username + "@example.com", Valid: true},
		Mobile:      sql.NullString{},
		Work:        sql.NullString{},
		SyncedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("insertMember: %v", err)
	}
}

func TestListGroups_Empty(t *testing.T) {
	srv, _ := newAdminServerWithQ(t)
	w := do(srv, "GET", "/api/v1/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var groups []string
	json.NewDecoder(w.Body).Decode(&groups)
	if len(groups) != 0 {
		t.Errorf("want empty array, got %v", groups)
	}
}

func TestListGroups(t *testing.T) {
	srv, q := newAdminServerWithQ(t)
	insertMember(t, q, "grp-oncall", "alice")
	insertMember(t, q, "grp-oncall", "bob")
	insertMember(t, q, "grp-admins", "carol")

	w := do(srv, "GET", "/api/v1/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var groups []string
	json.NewDecoder(w.Body).Decode(&groups)
	if len(groups) != 2 {
		t.Errorf("want 2 distinct groups, got %d: %v", len(groups), groups)
	}
}

func TestListGroupMembers(t *testing.T) {
	srv, q := newAdminServerWithQ(t)
	insertMember(t, q, "grp-oncall", "alice")
	insertMember(t, q, "grp-oncall", "bob")

	w := do(srv, "GET", "/api/v1/groups/grp-oncall/members", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var members []map[string]any
	json.NewDecoder(w.Body).Decode(&members)
	if len(members) != 2 {
		t.Errorf("want 2 members, got %d", len(members))
	}
	// Verify fields are present.
	for _, m := range members {
		if m["username"] == nil {
			t.Error("member missing username field")
		}
		if m["synced_at"] == nil {
			t.Error("member missing synced_at field")
		}
		if fmt.Sprintf("%v", m["email"]) == "" {
			t.Error("member missing email field")
		}
	}
}

func TestListGroupMembers_UnknownGroup(t *testing.T) {
	srv, _ := newAdminServerWithQ(t)
	w := do(srv, "GET", "/api/v1/groups/no-such-group/members", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}
