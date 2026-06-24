package api_test

import (
	"encoding/json"
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
	srv := api.NewServer(config.HTTPConfig{}, q, queue, noopLogger(), adminAuth(), okGroupVerifier(), &stubUserLookup{}, roleConfig, []string{"test"})
	shutdownOnCleanup(t, srv)
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
		"subject":       "New Subject: {{.severity}}",
		"body":          "<p>{{.severity}}</p>",
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

// ---- Audit log tests ----

// findAuditEntry returns the first audit entry matching action from q, or nil.
func findAuditEntry(t *testing.T, q *db.Queries, action string, after time.Time) *db.AuditLog {
	t.Helper()
	entries, err := q.ListAuditLogFiltered(t.Context(), db.ListAuditLogFilteredParams{
		Username: "user",
		FromTime: after.UTC().Format(time.RFC3339),
		ToTime:   "",
		Offset:   0,
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("ListAuditLogFiltered: %v", err)
	}
	for i := range entries {
		if entries[i].Action == action {
			return &entries[i]
		}
	}
	return nil
}

func TestCreateTemplate_AuditLog(t *testing.T) {
	srv, q := newAdminServerWithQ(t)
	before := time.Now().Add(-time.Second)

	body, _ := json.Marshal(map[string]any{
		"template_name": "audit-create-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	w := do(srv, "POST", "/api/v1/templates", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}

	entry := findAuditEntry(t, q, "create_template", before)
	if entry == nil {
		t.Fatal("expected create_template audit entry, none found")
	}
	if !entry.NewValues.Valid {
		t.Fatal("expected new_values to be set")
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(entry.NewValues.String), &snap); err != nil {
		t.Fatalf("new_values is not valid JSON: %v", err)
	}
	if snap["template_name"] != "audit-create-tmpl" {
		t.Errorf("new_values.template_name = %v, want %q", snap["template_name"], "audit-create-tmpl")
	}
	if entry.OldValues.Valid {
		t.Error("expected old_values to be NULL for create")
	}
}

func TestUpdateTemplate_AuditLog(t *testing.T) {
	srv, q := newAdminServerWithQ(t)

	createBody, _ := json.Marshal(map[string]any{
		"template_name": "audit-upd-tmpl",
		"subject":       "Old Subject",
		"body":          "<p>old</p>",
	})
	do(srv, "POST", "/api/v1/templates", createBody)

	before := time.Now().Add(-time.Second)
	updateBody, _ := json.Marshal(map[string]any{
		"subject": "New Subject",
		"body":    "<p>new</p>",
	})
	w := do(srv, "PUT", "/api/v1/templates/audit-upd-tmpl", updateBody)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	entry := findAuditEntry(t, q, "update_template", before)
	if entry == nil {
		t.Fatal("expected update_template audit entry, none found")
	}

	var oldSnap, newSnap map[string]any
	if err := json.Unmarshal([]byte(entry.OldValues.String), &oldSnap); err != nil {
		t.Fatalf("old_values is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(entry.NewValues.String), &newSnap); err != nil {
		t.Fatalf("new_values is not valid JSON: %v", err)
	}
	if oldSnap["subject"] != "Old Subject" {
		t.Errorf("old_values.subject = %v, want %q", oldSnap["subject"], "Old Subject")
	}
	if newSnap["subject"] != "New Subject" {
		t.Errorf("new_values.subject = %v, want %q", newSnap["subject"], "New Subject")
	}
}

func TestDeleteTemplate_AuditLog(t *testing.T) {
	srv, q := newAdminServerWithQ(t)

	createBody, _ := json.Marshal(map[string]any{
		"template_name": "audit-del-tmpl",
		"subject":       "Subject",
		"body":          "<p>body</p>",
	})
	do(srv, "POST", "/api/v1/templates", createBody)

	before := time.Now().Add(-time.Second)
	w := do(srv, "DELETE", "/api/v1/templates/audit-del-tmpl", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}

	entry := findAuditEntry(t, q, "delete_template", before)
	if entry == nil {
		t.Fatal("expected delete_template audit entry, none found")
	}
	if !entry.OldValues.Valid {
		t.Fatal("expected old_values to be set for delete")
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(entry.OldValues.String), &snap); err != nil {
		t.Fatalf("old_values is not valid JSON: %v", err)
	}
	if snap["template_name"] != "audit-del-tmpl" {
		t.Errorf("old_values.template_name = %v, want %q", snap["template_name"], "audit-del-tmpl")
	}
	if entry.NewValues.Valid {
		t.Error("expected new_values to be NULL for delete")
	}
}
