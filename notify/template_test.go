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
		"Alert: {{.severity}}",
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

func TestRenderTemplate(t *testing.T) {
	vars := map[string]string{
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
	vars := map[string]string{"input": "<script>alert(1)</script>"}
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
