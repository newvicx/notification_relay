package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"notification_relay/api"
	"notification_relay/config"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
	"notification_relay/testutil"
)

const testCRAMKey = "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=" // base64 of 32 'a' bytes

// newCRAMAdminServer creates a test server with admin auth and CRAM-MD5
// enabled (a valid encryption key configured), so the credential endpoints
// are exercisable.
func newCRAMAdminServer(t *testing.T) *api.Server {
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
		okGroupVerifier(), &stubUserLookup{}, roleConfig, []string{"test"},
		config.SMTPServerConfig{CRAMMD5Enabled: true, CRAMMD5SecretKey: testCRAMKey})
	shutdownOnCleanup(t, srv)
	return srv
}

func TestCRAMCredentials_CreateListDelete(t *testing.T) {
	srv := newCRAMAdminServer(t)

	w := do(srv, "POST", "/api/v1/smtp/cram-credentials", []byte(`{"username":"tim","roles":["publisher"]}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body)
	}
	var created struct {
		Username  string   `json:"username"`
		Roles     []string `json:"roles"`
		CreatedAt string   `json:"created_at"`
		Secret    string   `json:"secret"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Username != "tim" || len(created.Roles) != 1 || created.Roles[0] != "publisher" {
		t.Fatalf("unexpected create response: %+v", created)
	}
	if created.Secret == "" {
		t.Fatal("expected one-time secret in create response")
	}

	w = do(srv, "GET", "/api/v1/smtp/cram-credentials", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", w.Code, w.Body)
	}
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("want 1 credential, got %d", len(raw))
	}
	if _, hasSecret := raw[0]["secret"]; hasSecret {
		t.Fatal("list response must never include the secret")
	}

	w = do(srv, "DELETE", "/api/v1/smtp/cram-credentials/tim", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d: %s", w.Code, w.Body)
	}

	w = do(srv, "GET", "/api/v1/smtp/cram-credentials", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("want 0 credentials after delete, got %d", len(raw))
	}
}

func TestCRAMCredentials_DuplicateUsernameConflict(t *testing.T) {
	srv := newCRAMAdminServer(t)

	w := do(srv, "POST", "/api/v1/smtp/cram-credentials", []byte(`{"username":"tim","roles":["publisher"]}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d: %s", w.Code, w.Body)
	}
	w = do(srv, "POST", "/api/v1/smtp/cram-credentials", []byte(`{"username":"tim","roles":["reader"]}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d: %s", w.Code, w.Body)
	}
}

func TestCRAMCredentials_UnknownRoleRejected(t *testing.T) {
	srv := newCRAMAdminServer(t)
	w := do(srv, "POST", "/api/v1/smtp/cram-credentials", []byte(`{"username":"tim","roles":["superuser"]}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body)
	}
}

func TestCRAMCredentials_DeleteUnknownNotFound(t *testing.T) {
	srv := newCRAMAdminServer(t)
	w := do(srv, "DELETE", "/api/v1/smtp/cram-credentials/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body)
	}
}

func TestCRAMCredentials_CreateWithoutKeyConfiguredFails(t *testing.T) {
	t.Helper()
	roleConfig := map[string][]string{"admin": {"grp-admins"}}
	_, q := testutil.OpenDB(t)
	queue := make(chan notify.Job, 16)
	srv := api.NewServer(config.HTTPConfig{}, q, queue, noopLogger(),
		&stubAuth{result: &ldap.AuthResult{UserDN: "CN=admin,DC=example,DC=com", Groups: []string{"grp-admins"}}},
		okGroupVerifier(), &stubUserLookup{}, roleConfig, []string{"test"}, config.SMTPServerConfig{})
	shutdownOnCleanup(t, srv)

	w := do(srv, "POST", "/api/v1/smtp/cram-credentials", []byte(`{"username":"tim","roles":["publisher"]}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body)
	}
}
