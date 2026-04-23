package ldap

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"notification_relay/config"
	"notification_relay/db"
	"notification_relay/testutil"
)

// mockClient is a configurable test double for the Client interface.
type mockClient struct {
	connectErr error
	members    map[string][]Member // groupName → members returned by SearchGroupMembers
	searchErr  error
	closed     bool
}

func (m *mockClient) Connect(_ context.Context) error { return m.connectErr }
func (m *mockClient) SearchGroupMembers(_ context.Context, groupName string, _ uint32) ([]Member, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.members[groupName], nil
}
func (m *mockClient) Close() error {
	m.closed = true
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSyncGroup_InsertsMembers(t *testing.T) {
	conn, q := testutil.OpenDB(t)
	client := &mockClient{
		members: map[string][]Member{
			"grp-oncall": {
				{Username: "alice", DisplayName: "Alice Smith", Email: "alice@example.com"},
				{Username: "bob", DisplayName: "Bob Jones", Email: "bob@example.com"},
			},
		},
	}
	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	count, err := syncer.syncGroup(context.Background(), "grp-oncall")
	if err != nil {
		t.Fatalf("syncGroup: %v", err)
	}
	if count != 2 {
		t.Errorf("want count=2, got %d", count)
	}

	members, err := q.ListGroupMembers(context.Background(), "grp-oncall")
	if err != nil {
		t.Fatalf("ListGroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("want 2 members in DB, got %d", len(members))
	}

	byName := make(map[string]db.GroupMember)
	for _, m := range members {
		byName[m.Username] = m
	}
	if !byName["alice"].Email.Valid || byName["alice"].Email.String != "alice@example.com" {
		t.Errorf("alice.Email = %v, want alice@example.com", byName["alice"].Email)
	}
	if !byName["bob"].DisplayName.Valid || byName["bob"].DisplayName.String != "Bob Jones" {
		t.Errorf("bob.DisplayName = %v, want 'Bob Jones'", byName["bob"].DisplayName)
	}
}

func TestSyncGroup_ReplacesExisting(t *testing.T) {
	conn, q := testutil.OpenDB(t)

	// Seed an old member directly.
	if err := q.InsertGroupMember(context.Background(), db.InsertGroupMemberParams{
		GroupName: "grp-oncall",
		Username:  "old-member",
		SyncedAt:  "2020-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	client := &mockClient{
		members: map[string][]Member{
			"grp-oncall": {
				{Username: "new-member", Email: "new@example.com"},
			},
		},
	}
	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	if _, err := syncer.syncGroup(context.Background(), "grp-oncall"); err != nil {
		t.Fatalf("syncGroup: %v", err)
	}

	members, _ := q.ListGroupMembers(context.Background(), "grp-oncall")
	if len(members) != 1 {
		t.Fatalf("want 1 member after replace, got %d", len(members))
	}
	if members[0].Username != "new-member" {
		t.Errorf("want new-member, got %q", members[0].Username)
	}
}

func TestSyncGroup_EmptyGroup(t *testing.T) {
	conn, q := testutil.OpenDB(t)
	client := &mockClient{
		members: map[string][]Member{"grp-empty": {}},
	}
	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	count, err := syncer.syncGroup(context.Background(), "grp-empty")
	if err != nil {
		t.Fatalf("syncGroup with empty group: %v", err)
	}
	if count != 0 {
		t.Errorf("want count=0, got %d", count)
	}

	members, _ := q.ListGroupMembers(context.Background(), "grp-empty")
	if len(members) != 0 {
		t.Errorf("want 0 rows in DB, got %d", len(members))
	}
}

func TestSyncGroup_SkipsBlankUsername(t *testing.T) {
	conn, q := testutil.OpenDB(t)
	client := &mockClient{
		members: map[string][]Member{
			"grp-oncall": {
				{Username: ""},           // blank — should be skipped
				{Username: "valid-user"}, // should be inserted
			},
		},
	}
	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	count, err := syncer.syncGroup(context.Background(), "grp-oncall")
	if err != nil {
		t.Fatalf("syncGroup: %v", err)
	}
	// syncGroup returns len(members) which includes the blank entry.
	_ = count

	members, _ := q.ListGroupMembers(context.Background(), "grp-oncall")
	if len(members) != 1 {
		t.Fatalf("want 1 member (blank skipped), got %d: %v", len(members), members)
	}
	if members[0].Username != "valid-user" {
		t.Errorf("want valid-user, got %q", members[0].Username)
	}
}

func TestSyncGroup_SearchError(t *testing.T) {
	conn, q := testutil.OpenDB(t)

	// Pre-seed a member that must survive the failed sync.
	q.InsertGroupMember(context.Background(), db.InsertGroupMemberParams{
		GroupName: "grp-oncall",
		Username:  "survivor",
		SyncedAt:  "2020-01-01T00:00:00Z",
	})

	client := &mockClient{searchErr: context.DeadlineExceeded}
	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	_, err := syncer.syncGroup(context.Background(), "grp-oncall")
	if err == nil {
		t.Fatal("want error from syncGroup when search fails")
	}

	// Transaction must be rolled back — existing row must still be there.
	members, _ := q.ListGroupMembers(context.Background(), "grp-oncall")
	if len(members) != 1 || members[0].Username != "survivor" {
		t.Errorf("existing rows changed after failed sync: %v", members)
	}
}

func TestSyncOnce_SyncsAllGroups(t *testing.T) {
	conn, q := testutil.OpenDB(t)
	client := &mockClient{
		members: map[string][]Member{
			"grp-a": {{Username: "alice"}},
			"grp-b": {{Username: "bob"}},
		},
	}

	// Seed sync groups into DB.
	for _, name := range []string{"grp-a", "grp-b"} {
		if _, err := q.InsertSyncGroup(context.Background(), db.InsertSyncGroupParams{
			GroupName: name,
			CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertSyncGroup(%s): %v", name, err)
		}
	}

	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())
	syncer.syncOnce(context.Background())

	for _, group := range []string{"grp-a", "grp-b"} {
		members, err := q.ListGroupMembers(context.Background(), group)
		if err != nil {
			t.Fatalf("ListGroupMembers(%s): %v", group, err)
		}
		if len(members) != 1 {
			t.Errorf("group %s: want 1 member, got %d", group, len(members))
		}
	}
}

func TestSyncOnce_ConnectError(t *testing.T) {
	conn, q := testutil.OpenDB(t)
	client := &mockClient{connectErr: context.DeadlineExceeded}

	// Seed a sync group so the connect attempt is reached.
	if _, err := q.InsertSyncGroup(context.Background(), db.InsertSyncGroupParams{
		GroupName: "grp-oncall",
		CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertSyncGroup: %v", err)
	}

	cfg := config.LDAPConfig{PageSize: 500}
	syncer := NewSyncer(cfg, client, conn, discardLogger())

	// Must not panic; connect error is logged and sync is skipped.
	syncer.syncOnce(context.Background())

	// DB must be unchanged.
	members, _ := q.ListGroupMembers(context.Background(), "grp-oncall")
	if len(members) != 0 {
		t.Errorf("expected no members after connect error, got %d", len(members))
	}
}
