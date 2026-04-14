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

// adminAuth returns a stub that authenticates as an admin.
func adminAuth() *stubAuth {
	return &stubAuth{result: &ldap.AuthResult{UserDN: "CN=admin,DC=example,DC=com", Groups: []string{"grp-admins"}}}
}

func newAdminTestServer(t *testing.T) (*api.Server, func()) {
	t.Helper()
	roleConfig := map[string][]string{
		"admin":     {"grp-admins"},
		"publisher": {"grp-pub"},
		"reader":    {"grp-reader"},
	}
	_, q := testutil.OpenDB(t)
	queue := make(chan notify.Job, 16)
	srv := api.NewServer(config.HTTPConfig{}, q, queue, noopLogger(), adminAuth(), roleConfig)
	return srv, func() {}
}

// ---- Template CRUD tests ----

func TestCreateTemplate(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"template_name": "alert-tmpl",
		"subject":       "Alert: {{.severity}}",
		"body":          "<p>{{.severity}} at {{.location}}</p>",
		"required_vars": []string{"severity", "location"},
		"description":   "Standard alert email",
	})
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["template_name"] != "alert-tmpl" {
		t.Errorf("want template_name=alert-tmpl, got %v", resp["template_name"])
	}
	// required_vars should be decoded as an array, not a JSON string.
	if _, ok := resp["required_vars"].([]any); !ok {
		t.Errorf("required_vars should be an array, got %T", resp["required_vars"])
	}
}

func TestCreateTemplate_MissingName(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"subject": "Subject",
		"body":    "<p>body</p>",
	})
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestCreateTemplate_InvalidTemplateSyntax(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"template_name": "bad-tmpl",
		"subject":       "Subject",
		"body":          "{{.unclosed",
	})
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestCreateTemplate_MissingRequiredVar(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"template_name": "tmpl-missing-var",
		"subject":       "Subject",
		"body":          "<p>No vars here.</p>",
		"required_vars": []string{"severity"},
	})
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestCreateTemplate_Duplicate(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"template_name": "dup-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	do(srv, "POST", "/api/v1/templates", body)
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", w.Code)
	}
}

func TestListTemplates(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	for _, name := range []string{"tmpl-a", "tmpl-b"} {
		body, _ := json.Marshal(map[string]any{
			"template_name": name,
			"subject":       "Subject",
			"body":          "<p>body</p>",
		})
		do(srv, "POST", "/api/v1/templates", body)
	}

	w := do(srv, "GET", "/api/v1/templates", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var templates []any
	json.NewDecoder(w.Body).Decode(&templates)
	if len(templates) != 2 {
		t.Errorf("want 2 templates, got %d", len(templates))
	}
}

func TestGetTemplate(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"template_name": "get-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	do(srv, "POST", "/api/v1/templates", body)

	w := do(srv, "GET", "/api/v1/templates/get-tmpl", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGetTemplate_NotFound(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	w := do(srv, "GET", "/api/v1/templates/no-such", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestUpdateTemplate(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	createBody, _ := json.Marshal(map[string]any{
		"template_name": "upd-tmpl",
		"subject":       "Old Subject",
		"body":          "<p>old</p>",
	})
	do(srv, "POST", "/api/v1/templates", createBody)

	updateBody, _ := json.Marshal(map[string]any{
		"subject": "New Subject: {{.severity}}",
		"body":    "<p>{{.severity}}</p>",
		"required_vars": []string{"severity"},
	})
	w := do(srv, "PUT", "/api/v1/templates/upd-tmpl", updateBody)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["subject"] != "New Subject: {{.severity}}" {
		t.Errorf("subject not updated, got %v", resp["subject"])
	}
}

func TestUpdateTemplate_NotFound(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"subject": "Subject",
		"body":    "<p>body</p>",
	})
	w := do(srv, "PUT", "/api/v1/templates/no-such", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestDeleteTemplate(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"template_name": "del-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	do(srv, "POST", "/api/v1/templates", body)

	w := do(srv, "DELETE", "/api/v1/templates/del-tmpl", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}

	// Verify gone.
	wGet := do(srv, "GET", "/api/v1/templates/del-tmpl", nil)
	if wGet.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", wGet.Code)
	}
}

func TestDeleteTemplate_NotFound(t *testing.T) {
	srv, _ := newAdminTestServer(t)
	w := do(srv, "DELETE", "/api/v1/templates/no-such", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestTemplates_ReaderCannotWrite(t *testing.T) {
	srv, _ := newAdminTestServer(t)

	// Override to reader auth for these requests.
	readerSrv, _ := newTestServer(t, readerAuth())

	body, _ := json.Marshal(map[string]any{
		"template_name": "reader-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	w := do(readerSrv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader POST templates: want 403, got %d", w.Code)
	}

	// First create via admin so we can test update/delete.
	do(srv, "POST", "/api/v1/templates", body)

	w = do(readerSrv, "PUT", "/api/v1/templates/reader-tmpl", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader PUT template: want 403, got %d", w.Code)
	}

	w = do(readerSrv, "DELETE", "/api/v1/templates/reader-tmpl", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("reader DELETE template: want 403, got %d", w.Code)
	}
}
