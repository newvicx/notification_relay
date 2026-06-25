package smtpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
)

var validChannels = map[string]bool{
	"sms":   true,
	"voice": true,
	"email": true,
}

type session struct {
	s          *SMTPServer
	remoteAddr string // from smtp.Conn at NewSession — used in every audit log entry
	username   string // set after SASL PLAIN auth succeeds
	authed     bool
	// recipients accumulates one entry per RCPT TO. Each carries a group and
	// the channels encoded for that group, so different groups in the same
	// message can target different channels.
	recipients []parsedRecipient
}

// AuthMechanisms advertises support for PLAIN only.
func (sess *session) AuthMechanisms() []string {
	return []string{sasl.Plain}
}

// Auth handles the SASL exchange. Only PLAIN is supported.
func (sess *session) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, &gosmtp.SMTPError{
			Code:         504,
			EnhancedCode: gosmtp.EnhancedCode{5, 7, 4},
			Message:      "Unsupported authentication mechanism",
		}
	}
	return sasl.NewPlainServer(func(identity, username, password string) error {
		ctx := context.Background()
		result, err := sess.s.auth.AuthenticateUser(ctx, username, password)
		if err != nil {
			if err == ldap.ErrInvalidCredentials {
				sess.writeAuditLogAs(ctx, username, "smtp_login_failed", "", "", "")
				return &gosmtp.SMTPError{
					Code:         535,
					EnhancedCode: gosmtp.EnhancedCode{5, 7, 8},
					Message:      "Authentication credentials invalid",
				}
			}
			sess.s.logger.Error("smtp auth: ldap error", "username", username, "error", err)
			sess.writeAuditLogAs(ctx, username, "smtp_login_failed", "", "", "")
			return &gosmtp.SMTPError{
				Code:         451,
				EnhancedCode: gosmtp.EnhancedCode{4, 7, 0},
				Message:      "Temporary authentication failure",
			}
		}

		roles := resolveRoles(result.Groups, sess.s.roleConfig)
		if !hasPermission(roles, permPublish) {
			sess.writeAuditLogAs(ctx, username, "smtp_unauthorized", "", "", "")
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 7, 1},
				Message:      "Not authorized to publish notifications",
			}
		}

		sess.username = username
		sess.authed = true

		// sess.writeAuditLog(ctx, "smtp_login", "", "", "")
		return nil
	}), nil
}

// Mail accepts the MAIL FROM command. The envelope sender is ignored —
// targets and channels are read from the RCPT TO recipients.
func (sess *session) Mail(from string, opts *gosmtp.MailOptions) error {
	if !sess.authed {
		return gosmtp.ErrAuthRequired
	}
	return nil
}

// Rcpt validates the recipient domain and accumulates the group name and any
// delivery channels encoded in the address local part ("group+sms+voice").
func (sess *session) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	if !sess.authed {
		return gosmtp.ErrAuthRequired
	}
	rcpt, err := parseRecipient(to, sess.s.cfg.Domain)
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
			Message:      err.Error(),
		}
	}
	// Reject unknown channels at RCPT time so the sender sees which recipient
	// is at fault.
	for _, ch := range rcpt.channels {
		if !validChannels[ch] {
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
				Message:      fmt.Sprintf("Unknown channel %q in recipient %q; valid values: sms, voice, email", ch, to),
			}
		}
	}
	sess.recipients = append(sess.recipients, rcpt)
	return nil
}

// Data processes the message, creates one event plus a notification per
// recipient (so each group targets its own channels), and enqueues a job for
// each notification.
func (sess *session) Data(r io.Reader) error {
	if !sess.authed {
		return gosmtp.ErrAuthRequired
	}

	msg, err := mail.ReadMessage(r)
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 6, 0},
			Message:      "Failed to parse message: " + err.Error(),
		}
	}

	// Channels are normally encoded per recipient (collected in Rcpt). For
	// backward compatibility, the From: message header supplies fallback
	// channels applied to any recipient that did not embed its own.
	var fallbackChannels []string
	if fromHeader := msg.Header.Get("From"); fromHeader != "" {
		fallbackChannels, err = extractChannelsFromFromHeader(fromHeader, sess.s.cfg.Domain)
		if err != nil {
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 7},
				Message:      "Invalid From header: " + err.Error(),
			}
		}
		for _, ch := range fallbackChannels {
			if !validChannels[ch] {
				return &gosmtp.SMTPError{
					Code:         550,
					EnhancedCode: gosmtp.EnhancedCode{5, 6, 0},
					Message:      fmt.Sprintf("Unknown channel %q in From header; valid values: sms, voice, email", ch),
				}
			}
		}
	}

	eventName := strings.TrimSpace(msg.Header.Get("Subject"))

	message, err := extractPlainText(msg)
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 6, 0},
			Message:      "Failed to extract message body: " + err.Error(),
		}
	}

	// Validate.
	if len(sess.recipients) == 0 {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
			Message:      "At least one recipient (group) is required",
		}
	}
	if message == "" {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 6, 0},
			Message:      "Message body must not be empty",
		}
	}

	// Resolve each recipient's effective channels (its own, else the From:
	// fallback) up front so no records are created if any recipient is invalid.
	resolved, err := resolveTargets(sess.recipients, fallbackChannels)
	if err != nil {
		var mc missingChannelError
		if errors.As(err, &mc) {
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 7},
				Message: fmt.Sprintf(
					"No channel specified for group %q; encode it in the recipient address, e.g. %s+sms+voice@%s",
					mc.group, mc.group, sess.s.cfg.Domain),
			}
		}
		return smtpInternalError()
	}

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	event, err := sess.s.q.InsertEvent(ctx, db.InsertEventParams{
		EventID:          newUUIDV7(),
		EventUrl:         sql.NullString{},
		EventName:        nullString(eventName),
		EventDescription: sql.NullString{},
		EventSeverity:    sql.NullString{},
		StartTime:        now,
		CreatedBy:        nullString(sess.username),
		CreatedAt:        now,
	})
	if err != nil {
		sess.s.logger.Error("smtp: insert event failed", "error", err)
		return smtpTransientError("Failed to persist event")
	}
	sess.writeAuditLog(ctx, "create_event", "events", "", marshalJSON(event))

	emailVars := fmt.Sprintf(`{"subject": "%s"}`, eventName)
	// One notification per recipient: each targets a single group with the
	// channels encoded for that group.
	for _, rcpt := range resolved {
		groupsJSON, err := json.Marshal([]string{rcpt.group})
		if err != nil {
			return smtpInternalError()
		}
		channelsJSON, err := json.Marshal(rcpt.channels)
		if err != nil {
			return smtpInternalError()
		}

		notif, err := sess.s.q.InsertNotification(ctx, db.InsertNotificationParams{
			NotificationID: newUUIDV7(),
			EventID:        event.EventID,
			Groups:         sql.NullString{String: string(groupsJSON), Valid: true},
			Destinations:   sql.NullString{},
			Channels:       sql.NullString{String: string(channelsJSON), Valid: true},
			Message:        message,
			MemberCount:    0,
			EmailTemplate:  sql.NullString{String: "default", Valid: true},
			EmailVars:      sql.NullString{String: emailVars, Valid: true},
			CreatedAt:      now,
			CreatedBy:      nullString(sess.username),
			Status:         "pending",
		})
		if err != nil {
			sess.s.logger.Error("smtp: insert notification failed", "error", err)
			return smtpTransientError("Failed to persist notification")
		}
		sess.writeAuditLog(ctx, "create_notification", "notifications", "", marshalJSON(notif))

		select {
		case sess.s.queue <- notify.Job{NotificationID: notif.NotificationID}:
		default:
			sess.s.logger.Error("smtp: job queue full", "notification_id", notif.NotificationID)
			return &gosmtp.SMTPError{
				Code:         452,
				EnhancedCode: gosmtp.EnhancedCode{4, 3, 1},
				Message:      "Server busy, try again later",
			}
		}

		sess.s.logger.Info("smtp: notification queued",
			"notification_id", notif.NotificationID,
			"event_id", event.EventID,
			"group", rcpt.group,
			"channels", rcpt.channels,
			"username", sess.username,
		)
	}

	return nil
}

// Reset clears per-message state (recipients). Auth state is intentionally
// preserved: SMTP RSET resets the envelope, not the authenticated session.
func (sess *session) Reset() {
	sess.recipients = nil
}

// Logout frees session resources.
func (sess *session) Logout() error {
	return nil
}

// writeAuditLog records an audit entry using the session's authenticated username.
func (sess *session) writeAuditLog(ctx context.Context, action, impactedTable, oldJSON, newJSON string) {
	sess.writeAuditLogAs(ctx, sess.username, action, impactedTable, oldJSON, newJSON)
}

// writeAuditLogAs records an audit entry with an explicit username. Used for
// pre-auth events (e.g. failed logins) where sess.username is not yet set.
func (sess *session) writeAuditLogAs(ctx context.Context, username, action, impactedTable, oldJSON, newJSON string) {
	err := sess.s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Username:      username,
		IpAddress:     sql.NullString{String: sess.remoteAddr, Valid: sess.remoteAddr != ""},
		Action:        action,
		ImpactedTable: impactedTable,
		OldValues:     sql.NullString{String: oldJSON, Valid: oldJSON != ""},
		NewValues:     sql.NullString{String: newJSON, Valid: newJSON != ""},
	})
	if err != nil {
		sess.s.logger.Error("smtp: failed to write audit log",
			"action", action,
			"username", username,
			"error", err,
		)
	}
}

// newUUIDV7 returns a UUID v7 string (time-ordered, collision-resistant).
func newUUIDV7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("smtpapi: crypto/rand unavailable: " + err.Error())
	}
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func smtpInternalError() *gosmtp.SMTPError {
	return &gosmtp.SMTPError{
		Code:         451,
		EnhancedCode: gosmtp.EnhancedCode{4, 0, 0},
		Message:      "Internal server error",
	}
}

func smtpTransientError(msg string) *gosmtp.SMTPError {
	return &gosmtp.SMTPError{
		Code:         451,
		EnhancedCode: gosmtp.EnhancedCode{4, 0, 0},
		Message:      msg,
	}
}
