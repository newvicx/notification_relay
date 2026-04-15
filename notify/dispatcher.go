package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// Job represents a notification to be dispatched.
type Job struct {
	NotificationID string
}

// Dispatcher reads Jobs from the queue channel and fans them out to delivery providers.
type Dispatcher struct {
	cfg    config.NotifyConfig
	q      *db.Queries
	queue  <-chan Job
	logger *slog.Logger
	sms    SMSProvider   // nil if not configured
	voice  VoiceProvider // nil if not configured
	email  EmailProvider // nil if not configured
	// sem bounds the total number of in-flight per-channel delivery goroutines
	// across all workers (capacity = cfg.DeliveryConcurrency).
	sem chan struct{}
}

func NewDispatcher(
	cfg config.NotifyConfig,
	q *db.Queries,
	queue <-chan Job,
	sms SMSProvider,
	voice VoiceProvider,
	email EmailProvider,
	logger *slog.Logger,
) *Dispatcher {
	concurrency := cfg.DeliveryConcurrency
	if concurrency <= 0 {
		concurrency = 16
	}
	return &Dispatcher{
		cfg:    cfg,
		q:      q,
		queue:  queue,
		logger: logger,
		sms:    sms,
		voice:  voice,
		email:  email,
		sem:    make(chan struct{}, concurrency),
	}
}

// Run starts the dispatcher workers. It returns when ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.logger.Info("dispatcher started", "workers", d.cfg.WorkerCount)
	for range d.cfg.WorkerCount {
		go d.worker(ctx)
	}
	<-ctx.Done()
	d.logger.Info("dispatcher shutting down")
}

func (d *Dispatcher) worker(ctx context.Context) {
	for {
		select {
		case job, ok := <-d.queue:
			if !ok {
				return
			}
			d.logger.Info("dispatch job received", "notification_id", job.NotificationID)
			d.processJob(ctx, job)
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) processJob(ctx context.Context, job Job) {
	notif, err := d.q.GetNotificationByNotificationID(ctx, job.NotificationID)
	if err != nil {
		d.logger.Error("dispatcher: load notification failed",
			"notification_id", job.NotificationID, "error", err)
		return
	}

	var groups []string
	if err := json.Unmarshal([]byte(notif.Groups), &groups); err != nil {
		d.logger.Error("dispatcher: parse groups failed",
			"notification_id", job.NotificationID, "error", err)
		return
	}

	var channels []string
	if err := json.Unmarshal([]byte(notif.Channels), &channels); err != nil {
		d.logger.Error("dispatcher: parse channels failed",
			"notification_id", job.NotificationID, "error", err)
		return
	}

	channelSet := make(map[string]bool, len(channels))
	for _, c := range channels {
		channelSet[c] = true
	}

	var (
		wg           sync.WaitGroup
		totalMembers int
	)
	for _, group := range groups {
		members, err := d.q.ListGroupMembers(ctx, group)
		if err != nil {
			d.logger.Error("dispatcher: list group members failed",
				"notification_id", job.NotificationID, "group", group, "error", err)
			continue
		}
		totalMembers += len(members)
		for _, member := range members {
			if channelSet["sms"] && d.sms != nil && member.Mobile.Valid && member.Mobile.String != "" {
				wg.Add(1)
				go func(g string, m db.GroupMember) {
					defer wg.Done()
					d.sem <- struct{}{}
					defer func() { <-d.sem }()
					d.dispatchSMS(ctx, notif, g, m)
				}(group, member)
			}
			if channelSet["voice"] && d.voice != nil && ((member.Mobile.Valid && member.Mobile.String != "") || (member.Work.Valid && member.Work.String != "")) {
				wg.Add(1)
				go func(g string, m db.GroupMember) {
					defer wg.Done()
					d.sem <- struct{}{}
					defer func() { <-d.sem }()
					d.dispatchVoice(ctx, notif, g, m)
				}(group, member)
			}
			if channelSet["email"] && d.email != nil && member.Email.Valid && member.Email.String != "" {
				wg.Add(1)
				go func(g string, m db.GroupMember) {
					defer wg.Done()
					d.sem <- struct{}{}
					defer func() { <-d.sem }()
					d.dispatchEmail(ctx, notif, g, m)
				}(group, member)
			}
		}
	}
	wg.Wait()

	if err := d.q.UpdateNotificationMemberCount(ctx, db.UpdateNotificationMemberCountParams{
		MemberCount:    int64(totalMembers),
		NotificationID: notif.NotificationID,
	}); err != nil {
		d.logger.Error("dispatcher: update member count failed",
			"notification_id", notif.NotificationID, "error", err)
	}
}

func (d *Dispatcher) dispatchSMS(ctx context.Context, notif db.Notification, group string, member db.GroupMember) {
	var (
		lastErr      error
		sid, status  string
		finalAttempt int
	)

	for n := 0; n < d.cfg.RetryLimit; n++ {
		if n > 0 {
			delay := d.cfg.RetryDelay * (1 << (n - 1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		finalAttempt = n + 1
		sid, status, lastErr = d.sms.Send(member.Mobile.String, notif.Message)
		if lastErr == nil {
			break
		}
		d.logger.Warn("dispatcher: sms send attempt failed",
			"notification_id", notif.NotificationID,
			"member", member.Username,
			"attempt", finalAttempt,
			"error", lastErr)
	}

	deliveryID := sid
	if lastErr != nil {
		deliveryID = uuidV7()
		status = "failed"
	}

	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     deliveryID,
		NotificationID: notif.NotificationID,
		Group:          group,
		Member:         member.Username,
		Channel:        "sms",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		d.logger.Error("dispatcher: insert sms delivery failed",
			"notification_id", notif.NotificationID, "member", member.Username, "error", err)
		return
	}
	if lastErr != nil {
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: errorString(lastErr), Valid: true},
		})
	}
}

func (d *Dispatcher) dispatchVoice(ctx context.Context, notif db.Notification, group string, member db.GroupMember) {
	var (
		lastErr      error
		sid, status  string
		finalAttempt int
	)

	for n := 0; n < d.cfg.RetryLimit; n++ {
		if n > 0 {
			delay := d.cfg.RetryDelay * (1 << (n - 1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		finalAttempt = n + 1
		if member.Mobile.Valid {
			sid, status, lastErr = d.voice.Call(member.Mobile.String, notif.Message)
		} else {
			sid, status, lastErr = d.voice.Call(member.Work.String, notif.Message)
		}
		if lastErr == nil {
			break
		}
		d.logger.Warn("dispatcher: voice call attempt failed",
			"notification_id", notif.NotificationID,
			"member", member.Username,
			"attempt", finalAttempt,
			"error", lastErr)
	}

	deliveryID := sid
	if lastErr != nil {
		deliveryID = uuidV7()
		status = "failed"
	}

	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     deliveryID,
		NotificationID: notif.NotificationID,
		Group:          group,
		Member:         member.Username,
		Channel:        "voice",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		d.logger.Error("dispatcher: insert voice delivery failed",
			"notification_id", notif.NotificationID, "member", member.Username, "error", err)
		return
	}
	if lastErr != nil {
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: errorString(lastErr), Valid: true},
		})
	}
}

func (d *Dispatcher) dispatchEmail(ctx context.Context, notif db.Notification, group string, member db.GroupMember) {
	deliveryID := uuidV7()
	sentAt := time.Now().UTC().Format(time.RFC3339)

	recordFailed := func(reason string) {
		delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
			DeliveryID:     deliveryID,
			NotificationID: notif.NotificationID,
			Group:          group,
			Member:         member.Username,
			Channel:        "email",
			Status:         "failed",
			EmailTemplate:  notif.EmailTemplate,
			EmailVars:      notif.EmailVars,
			Attempt:        1,
			SentAt:         sentAt,
		})
		if err != nil {
			d.logger.Error("dispatcher: insert email delivery failed",
				"notification_id", notif.NotificationID, "member", member.Username, "error", err)
			return
		}
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: reason, Valid: true},
		})
	}

	// Require a template on the notification.
	if !notif.EmailTemplate.Valid || notif.EmailTemplate.String == "" {
		recordFailed("no email template specified")
		return
	}

	// Load template from DB.
	tmpl, err := d.q.GetEmailTemplateByName(ctx, notif.EmailTemplate.String)
	if err != nil {
		d.logger.Error("dispatcher: load email template failed",
			"template", notif.EmailTemplate.String, "error", err)
		recordFailed(fmt.Sprintf("template not found: %s", notif.EmailTemplate.String))
		return
	}

	// Parse vars from notification.
	vars := make(map[string]string)
	if notif.EmailVars.Valid && notif.EmailVars.String != "" {
		if err := json.Unmarshal([]byte(notif.EmailVars.String), &vars); err != nil {
			recordFailed("invalid email_vars JSON")
			return
		}
	}

	// Validate all required vars are present.
	var requiredVars []string
	if err := json.Unmarshal([]byte(tmpl.RequiredVars), &requiredVars); err != nil {
		recordFailed("template has invalid required_vars")
		return
	}
	for _, v := range requiredVars {
		if _, ok := vars[v]; !ok {
			recordFailed(fmt.Sprintf("missing required template variable: %s", v))
			return
		}
	}

	// Render subject and body.
	subject, body, err := RenderTemplate(tmpl.Subject, tmpl.Body, vars)
	if err != nil {
		d.logger.Error("dispatcher: render email template failed",
			"template", tmpl.TemplateName, "error", err)
		recordFailed(fmt.Sprintf("template render error: %v", err))
		return
	}

	// Send.
	sendErr := d.email.Send(ctx, member.Email.String, subject, body)

	status := "sent"
	if sendErr != nil {
		status = "failed"
		d.logger.Warn("dispatcher: email send failed",
			"notification_id", notif.NotificationID,
			"member", member.Username,
			"error", sendErr)
	}

	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     deliveryID,
		NotificationID: notif.NotificationID,
		Group:          group,
		Member:         member.Username,
		Channel:        "email",
		Status:         status,
		EmailTemplate:  notif.EmailTemplate,
		EmailVars:      notif.EmailVars,
		Attempt:        1,
		SentAt:         sentAt,
	})
	if err != nil {
		d.logger.Error("dispatcher: insert email delivery failed",
			"notification_id", notif.NotificationID, "member", member.Username, "error", err)
		return
	}
	if sendErr != nil {
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: errorString(sendErr), Valid: true},
		})
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
