//go:build integration

// Integration tests for the SMTP email provider.
// These tests spin up an in-process fake SMTP server — no Docker or external
// dependencies required.
//
// Run with:
//
//	go test -tags integration -v ./notify/ -run Integration_SMTP

package notify_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"notification_relay/config"
	"notification_relay/notify"
)

// writeHTMLPreview extracts the HTML body from a raw SMTP DATA section and
// writes it to a temporary file. The file path is logged so it can be opened
// directly in a browser.
func writeHTMLPreview(t *testing.T, rawData string) {
	t.Helper()
	// The HTML body starts after the blank line that separates headers from body.
	parts := strings.SplitN(rawData, "\n\n", 2)
	html := rawData
	if len(parts) == 2 {
		html = parts[1]
	}

	f, err := os.CreateTemp("", "smtp_preview_*.html")
	if err != nil {
		t.Logf("writeHTMLPreview: could not create temp file: %v", err)
		return
	}
	defer f.Close()
	f.WriteString(html)
	t.Logf("HTML preview: file://%s", f.Name())
}

func TestIntegration_SMTPEmail(t *testing.T) {
	srv := startFakeSMTP(t)

	cfg := config.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        srv.port(),
		FromAddress: "relay@example.com",
		TLSMode:     "none",
		// Username empty → skip AUTH
	}

	email := notify.NewSMTPEmail(cfg)
	body := "<p>Hello <strong>world</strong></p>"
	err := email.Send(context.Background(), "oncall@example.com", "Test Subject", body)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.received()
	if len(msgs) != 1 {
		t.Fatalf("want 1 message captured, got %d", len(msgs))
	}
	m := msgs[0]

	if !strings.Contains(m.RawData, "Subject: Test Subject") {
		t.Errorf("Subject header missing from captured message:\n%s", m.RawData)
	}
	if !strings.Contains(m.RawData, "Hello") {
		t.Errorf("Body content missing from captured message:\n%s", m.RawData)
	}
	if m.From != "relay@example.com" {
		t.Errorf("want From=relay@example.com, got %q", m.From)
	}
	if len(m.To) == 0 || m.To[0] != "oncall@example.com" {
		t.Errorf("want To=oncall@example.com, got %v", m.To)
	}

	writeHTMLPreview(t, m.RawData)
}

func TestIntegration_SMTPEmail_WithAuth(t *testing.T) {
	srv := startFakeSMTP(t)

	cfg := config.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        srv.port(),
		FromAddress: "relay@example.com",
		Username:    "smtpuser",
		Password:    "smtppass",
		TLSMode:     "none",
	}

	email := notify.NewSMTPEmail(cfg)
	err := email.Send(context.Background(), "recipient@example.com", "Auth Test", "<p>Auth works.</p>")
	if err != nil {
		t.Fatalf("Send with auth: %v", err)
	}

	msgs := srv.received()
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].RawData, "Subject: Auth Test") {
		t.Errorf("subject header missing")
	}

	writeHTMLPreview(t, msgs[0].RawData)
}

func TestIntegration_SMTPEmail_TemplateRendered(t *testing.T) {
	srv := startFakeSMTP(t)

	cfg := config.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        srv.port(),
		FromAddress: "relay@example.com",
		TLSMode:     "none",
	}

	// Render a real HTML template with variable substitution.
	subject, htmlBody, err := notify.RenderTemplate(
		"Alert: {{.severity}}",
		"<h1>{{.severity}}</h1><p>Location: {{.location}}</p>",
		map[string]any{"severity": "critical", "location": "Plant 3"},
	)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if subject != "Alert: critical" {
		t.Errorf("rendered subject = %q, want %q", subject, "Alert: critical")
	}

	email := notify.NewSMTPEmail(cfg)
	err = email.Send(context.Background(), "oncall@example.com", subject, htmlBody)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.received()
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	m := msgs[0]

	if !strings.Contains(m.RawData, "Subject: Alert: critical") {
		t.Errorf("rendered subject not in email headers:\n%s", m.RawData)
	}
	if !strings.Contains(m.RawData, "Plant 3") {
		t.Errorf("rendered body variable 'Plant 3' missing:\n%s", m.RawData)
	}
	if !strings.Contains(m.RawData, "critical") {
		t.Errorf("rendered body variable 'critical' missing:\n%s", m.RawData)
	}

	// Write the rendered HTML to a file for browser inspection.
	writeHTMLPreview(t, m.RawData)
}
