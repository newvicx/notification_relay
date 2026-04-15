//go:build integration

// Integration tests for the LDAP authenticator, client, and syncer.
// They require a running LDAP server and are excluded from the normal test run:
//
//	go test -tags integration -v ./ldap/ -run Integration_LDAP
//
// The tests load the ldap: section from the same config.yaml used by the Twilio
// integration tests. Set TEST_CONFIG to the file path, or place config.yaml in
// the project root and the tests will find it automatically.
//
// # Recommended LDAP server: glauth
//
// glauth (https://github.com/glauth/glauth) is a single Go binary that serves
// LDAP without Docker. Download from the releases page, then create a minimal
// config file and run it:
//
//	./glauth -c glauth-test.cfg
//
// Example glauth-test.cfg that matches the integration test expectations:
//
//	[ldap]
//	  enabled = true
//	  listen = "0.0.0.0:3893"
//
//	[ldaps]
//	  enabled = false
//
//	[[users]]
//	  name = "testuser"
//	  givenname = "Test"
//	  sn = "User"
//	  mail = "testuser@example.com"
//	  uidnumber = 5001
//	  primarygroup = 5501
//	  passsha256 = "6478579e37aff45f013e14eeb30b3cc56c72ccdc310123bcdf53e0333e3f416a"
//	  # passsha256 above = sha256("testpass")
//	  [[users.capabilities]]
//	    action = "search"
//	    object = "*"
//
//	[[users]]
//	  name = "svc-relay"
//	  uidnumber = 5000
//	  primarygroup = 5500
//	  passsha256 = "652c7dc687d98c9889304ed2e408c74b611e86a40caa51c4b43f1dd5913c5cd0"
//	  # passsha256 above = sha256("svcpass")
//	  [[users.capabilities]]
//	    action = "search"
//	    object = "*"
//
//	[[groups]]
//	  name = "svc-accounts"
//	  gidnumber = 5500
//
//	[[groups]]
//	  name = "grp-oncall"
//	  gidnumber = 5501
//	  includegroups = [5500]
//
// Matching config.yaml ldap section:
//
//	ldap:
//	  primary_url: "ldap://localhost:3893"
//	  bind_dn: "cn=svc-relay,dc=example,dc=com"
//	  bind_password: "svcpass"
//	  user_base_dn: "dc=example,dc=com"
//	  group_base_dn: "dc=example,dc=com"
//	  group_filter: "(objectClass=posixGroup)"
//	  sync_groups: ["grp-oncall"]
//	  tls_skip_verify: true
//
// Test environment variables (override defaults from config.yaml):
//
//	TEST_LDAP_TEST_USER      username of the test user (default: testuser)
//	TEST_LDAP_TEST_PASSWORD  password of the test user (default: testpass)
//	TEST_LDAP_TEST_GROUP     group CN to search (default: grp-oncall)

package ldap_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"notification_relay/config"
	"notification_relay/testutil"

	ldap "notification_relay/ldap"

	"gopkg.in/yaml.v3"
)

// ---- Config loading ----

func loadLDAPConfig(t *testing.T) config.LDAPConfig {
	t.Helper()

	path := os.Getenv("TEST_CONFIG")
	if path == "" {
		for _, candidate := range []string{"../config.yaml", "../../config.yaml"} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		t.Skip("config.yaml not found — set TEST_CONFIG to its path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read config file %q: %v", path, err)
	}

	var wrapper struct {
		LDAP config.LDAPConfig `yaml:"ldap"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("parse ldap config: %v", err)
	}
	if wrapper.LDAP.PrimaryURL == "" {
		t.Skip("ldap.primary_url not configured — skipping LDAP integration test")
	}
	return wrapper.LDAP
}

func testUser(t *testing.T) (username, password string) {
	t.Helper()
	u := os.Getenv("TEST_LDAP_TEST_USER")
	p := os.Getenv("TEST_LDAP_TEST_PASSWORD")
	if u == "" {
		u = "testuser"
	}
	if p == "" {
		p = "testpass"
	}
	return u, p
}

func testGroup(t *testing.T) string {
	t.Helper()
	if g := os.Getenv("TEST_LDAP_TEST_GROUP"); g != "" {
		return g
	}
	return "grp-oncall"
}

// ---- Tests ----

func TestIntegration_LDAP_AuthenticateValid(t *testing.T) {
	cfg := loadLDAPConfig(t)
	username, password := testUser(t)

	auth := ldap.NewAuthenticator(
		cfg.PrimaryURL, cfg.BackupURL,
		cfg.BindDN, cfg.BindPassword,
		cfg.UserBaseDN, cfg.TLSSkipVerify,
	)
	result, err := auth.AuthenticateUser(context.Background(), username, password)
	if err != nil {
		t.Fatalf("AuthenticateUser(%q): %v", username, err)
	}
	if result.UserDN == "" {
		t.Error("expected non-empty UserDN")
	}
	t.Logf("authenticated: dn=%s groups=%v", result.UserDN, result.Groups)
}

func TestIntegration_LDAP_AuthenticateWrongPassword(t *testing.T) {
	cfg := loadLDAPConfig(t)
	username, _ := testUser(t)

	auth := ldap.NewAuthenticator(
		cfg.PrimaryURL, cfg.BackupURL,
		cfg.BindDN, cfg.BindPassword,
		cfg.UserBaseDN, cfg.TLSSkipVerify,
	)
	_, err := auth.AuthenticateUser(context.Background(), username, "definitely-wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
	if err != ldap.ErrInvalidCredentials {
		t.Errorf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestIntegration_LDAP_AuthenticateUnknownUser(t *testing.T) {
	cfg := loadLDAPConfig(t)

	auth := ldap.NewAuthenticator(
		cfg.PrimaryURL, cfg.BackupURL,
		cfg.BindDN, cfg.BindPassword,
		cfg.UserBaseDN, cfg.TLSSkipVerify,
	)
	_, err := auth.AuthenticateUser(context.Background(), "nonexistent-user-xyz", "pass")
	if err == nil {
		t.Fatal("expected error for unknown user, got nil")
	}
	if err != ldap.ErrInvalidCredentials {
		t.Errorf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestIntegration_LDAP_SearchGroupMembers(t *testing.T) {
	cfg := loadLDAPConfig(t)
	groupName := testGroup(t)

	client := ldap.NewClient(
		cfg.PrimaryURL, cfg.BackupURL,
		cfg.BindDN, cfg.BindPassword,
		cfg.UserBaseDN, cfg.GroupBaseDN, cfg.GroupFilter,
		cfg.TLSSkipVerify,
	)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	members, err := client.SearchGroupMembers(context.Background(), groupName, cfg.PageSize)
	if err != nil {
		t.Fatalf("SearchGroupMembers(%q): %v", groupName, err)
	}
	t.Logf("group %q has %d members", groupName, len(members))
	for _, m := range members {
		t.Logf("  member: username=%q email=%q", m.Username, m.Email)
		if m.Username == "" {
			t.Error("member has empty username")
		}
	}
}

func TestIntegration_LDAP_SyncerRoundTrip(t *testing.T) {
	cfg := loadLDAPConfig(t)
	groupName := testGroup(t)
	cfg.SyncGroups = []string{groupName}
	if cfg.PageSize == 0 {
		cfg.PageSize = 500
	}
	// Short interval so Run exits after the first sync cycle when context is cancelled.
	cfg.SyncInterval = 100 * time.Millisecond

	conn, q := testutil.OpenDB(t)
	client := ldap.NewClient(
		cfg.PrimaryURL, cfg.BackupURL,
		cfg.BindDN, cfg.BindPassword,
		cfg.UserBaseDN, cfg.GroupBaseDN, cfg.GroupFilter,
		cfg.TLSSkipVerify,
	)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	syncer := ldap.NewSyncer(cfg, client, conn, logger)

	// Run performs an immediate syncOnce before entering the ticker loop.
	// We allow 10s for the sync to complete against the local test server,
	// then cancel to unblock Run.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		syncer.Run(ctx)
		close(runDone)
	}()

	// Give syncOnce time to finish (local LDAP is fast), then cancel.
	select {
	case <-runDone:
		// Run exited on its own (unusual).
	case <-time.After(3 * time.Second):
		cancel() // unblock Run
		<-runDone
	}

	members, err := q.ListGroupMembers(context.Background(), groupName)
	if err != nil {
		t.Fatalf("ListGroupMembers: %v", err)
	}
	if len(members) == 0 {
		t.Errorf("expected at least 1 member in %q after sync, got 0", groupName)
	}
	t.Logf("synced %d members into %q", len(members), groupName)
}
