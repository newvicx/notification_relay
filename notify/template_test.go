package notify

import (
	"strings"
	"testing"
)

func TestValidateTemplate_Valid(t *testing.T) {
	err := ValidateTemplate(
		"Alert: {{.severity}}",
		"<p>Event at {{.location}}: {{.severity}}</p>",
		[]string{"severity", "location"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTemplate_RequiredVarInSubjectOnly(t *testing.T) {
	err := ValidateTemplate(
		"Alert: {{ .severity }}",
		"<p>An alert occurred.</p>",
		[]string{"severity"},
	)
	if err != nil {
		t.Fatalf("var in subject only should be valid: %v", err)
	}
}

func TestValidateTemplate_MissingRequiredVar(t *testing.T) {
	err := ValidateTemplate(
		"Subject",
		"<p>No vars here.</p>",
		[]string{"missing_var"},
	)
	if err == nil {
		t.Fatal("want error for missing required var")
	}
	if !strings.Contains(err.Error(), "missing_var") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestValidateTemplate_InvalidBodySyntax(t *testing.T) {
	err := ValidateTemplate("Subject", "{{.unclosed", nil)
	if err == nil {
		t.Fatal("want error for bad template syntax")
	}
	if !strings.Contains(err.Error(), "body") {
		t.Errorf("error should mention 'body', got: %v", err)
	}
}

func TestValidateTemplate_InvalidSubjectSyntax(t *testing.T) {
	err := ValidateTemplate("{{.unclosed", "<p>body</p>", nil)
	if err == nil {
		t.Fatal("want error for bad subject syntax")
	}
	if !strings.Contains(err.Error(), "subject") {
		t.Errorf("error should mention 'subject', got: %v", err)
	}
}

func TestValidateTemplate_NoRequiredVars(t *testing.T) {
	err := ValidateTemplate("Static subject", "<p>Static body.</p>", nil)
	if err != nil {
		t.Fatalf("no required vars should always pass: %v", err)
	}
}

// A required var "server" should match {{.server.host}} in the template.
func TestValidateTemplate_RequiredVarMatchesNestedAccess(t *testing.T) {
	err := ValidateTemplate(
		"Alert from {{.server.host}}",
		"<p>Details: {{.server.region}}</p>",
		[]string{"server"},
	)
	if err != nil {
		t.Fatalf("required var should match nested access: %v", err)
	}
}

// "foo" must not match {{.foobar}} — prefix-only matches are rejected.
func TestValidateTemplate_RequiredVarNotPrefixMatch(t *testing.T) {
	err := ValidateTemplate(
		"Subject",
		"<p>{{.foobar}}</p>",
		[]string{"foo"},
	)
	if err == nil {
		t.Fatal("want error: 'foo' should not match '{{.foobar}}'")
	}
}

func TestRenderTemplate(t *testing.T) {
	vars := map[string]any{
		"severity": "critical",
		"location": "Plant 3",
	}
	subject, body, err := RenderTemplate(
		"Alert: {{.severity}}",
		"<p>Event at {{.location}}: {{.severity}}</p>",
		vars,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "Alert: critical" {
		t.Errorf("subject = %q, want %q", subject, "Alert: critical")
	}
	if !strings.Contains(body, "Plant 3") || !strings.Contains(body, "critical") {
		t.Errorf("body missing expected content: %q", body)
	}
}

func TestRenderTemplate_HTMLEscaping(t *testing.T) {
	vars := map[string]any{"input": "<script>alert(1)</script>"}
	_, body, err := RenderTemplate("Subject", "<p>{{.input}}</p>", vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(body, "<script>") {
		t.Error("html/template should have escaped <script> tag")
	}
}

func TestRenderTemplate_EmptyVars(t *testing.T) {
	_, body, err := RenderTemplate("Subject", "<p>No vars.</p>", nil)
	if err != nil {
		t.Fatalf("unexpected error with no vars: %v", err)
	}
	if body != "<p>No vars.</p>" {
		t.Errorf("unexpected body: %q", body)
	}
}

// Nested map access: {{.server.host}} with vars["server"] = map[string]any{...}.
func TestRenderTemplate_NestedMap(t *testing.T) {
	vars := map[string]any{
		"server": map[string]any{
			"host":   "prod-01",
			"region": "us-east-1",
		},
	}
	subject, body, err := RenderTemplate(
		"Alert on {{.server.host}}",
		"<p>Region: {{.server.region}}</p>",
		vars,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "Alert on prod-01" {
		t.Errorf("subject = %q, want %q", subject, "Alert on prod-01")
	}
	if !strings.Contains(body, "us-east-1") {
		t.Errorf("body missing region: %q", body)
	}
}

// Slice iteration: {{range .alerts}}.
func TestRenderTemplate_SliceRange(t *testing.T) {
	vars := map[string]any{
		"alerts": []any{"disk full", "cpu high"},
	}
	_, body, err := RenderTemplate(
		"Subject",
		"<ul>{{range .alerts}}<li>{{.}}</li>{{end}}</ul>",
		vars,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, "disk full") || !strings.Contains(body, "cpu high") {
		t.Errorf("body missing alert items: %q", body)
	}
}

// Missing key returns an error when missingkey=error is set.
// 4/22/26: Test removed as we dont want to error on missing keys due
// to the need for conditional rendering. We rely on people to include
// required attributes
// func TestRenderTemplate_MissingKeyError(t *testing.T) {
// 	vars := map[string]any{"present": "yes"}
// 	_, _, err := RenderTemplate("Subject", "<p>{{.missing}}</p>", vars)
// 	if err == nil {
// 		t.Fatal("want error for missing key")
// 	}
// }

func TestContainsFieldRef(t *testing.T) {
	cases := []struct {
		src  string
		name string
		want bool
	}{
		{"{{.foo}}", "foo", true},
		{"{{.foo.bar}}", "foo", true},
		{"{{.foo | upper}}", "foo", true},
		{"{{.foobar}}", "foo", false},
		{"no template here", "foo", false},
		{"{{.foo}}", "bar", false},
	}
	for _, tc := range cases {
		got := containsFieldRef(tc.src, tc.name)
		if got != tc.want {
			t.Errorf("containsFieldRef(%q, %q) = %v, want %v", tc.src, tc.name, got, tc.want)
		}
	}
}

func TestWalkPath(t *testing.T) {
	vars := map[string]any{
		"top": "val",
		"nested": map[string]any{
			"child": "x",
			"deep": map[string]any{
				"leaf": 1,
			},
		},
	}
	cases := []struct {
		path string
		want bool
	}{
		{"top", true},
		{"nested", true},
		{"nested.child", true},
		{"nested.deep.leaf", true},
		{"missing", false},
		{"nested.missing", false},
		{"nested.deep.missing", false},
		{"top.nope", false}, // top is a string, not a map
	}
	for _, tc := range cases {
		got := walkPath(vars, tc.path)
		if got != tc.want {
			t.Errorf("walkPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
