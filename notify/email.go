package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"notification_relay/config"
)

// EmailProvider sends HTML email messages.
type EmailProvider interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPEmail implements EmailProvider using Go's net/smtp standard library.
// It supports plaintext, STARTTLS, and direct TLS connections controlled by
// the TLSMode field in SMTPConfig.
type SMTPEmail struct {
	cfg     config.SMTPConfig
	timeout time.Duration
}

// NewSMTPEmail constructs an SMTPEmail provider from the given config.
func NewSMTPEmail(cfg config.SMTPConfig, timeout time.Duration) *SMTPEmail {
	return &SMTPEmail{cfg: cfg, timeout: timeout}
}

// Send delivers an HTML email to `to` with the given subject and body.
// The body is sent as text/html; charset=UTF-8.
func (e *SMTPEmail) Send(ctx context.Context, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", e.cfg.Host, e.cfg.Port)

	// Derive a dial timeout from the context deadline if one is set.
	timeout := e.timeout
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining < timeout {
			timeout = remaining
		}
	}

	msg := buildMessage(e.cfg.FromAddress, to, subject, body)

	var client *smtp.Client
	var err error

	switch e.cfg.TLSMode {
	case "tls":
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: timeout},
			Config:    &tls.Config{ServerName: e.cfg.Host},
		}
		conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("smtp tls dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, e.cfg.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp new client: %w", err)
		}

	default: // "starttls" or "none"
		netConn, dialErr := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("smtp dial: %w", dialErr)
		}
		client, err = smtp.NewClient(netConn, e.cfg.Host)
		if err != nil {
			netConn.Close()
			return fmt.Errorf("smtp new client: %w", err)
		}
		if e.cfg.TLSMode != "none" {
			if err := client.StartTLS(&tls.Config{ServerName: e.cfg.Host}); err != nil {
				client.Close()
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	defer client.Close()

	if e.cfg.Username != "" {
		auth := smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(e.cfg.FromAddress); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		wc.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return client.Quit()
}

// buildMessage constructs a minimal RFC 2822 email message with an HTML body.
func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
