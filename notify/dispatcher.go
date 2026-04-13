package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
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
	cfg   config.NotifyConfig
	q     *db.Queries
	queue <-chan Job
	logger *slog.Logger
	sms   SMSProvider   // nil if not configured
	voice VoiceProvider // nil if not configured
	email EmailProvider // nil if not configured
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
	return &Dispatcher{
		cfg:   cfg,
		q:     q,
		queue: queue,
		logger: logger,
		sms:   sms,
		voice: voice,
		email: email,
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

	var totalMembers int
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
				d.dispatchSMS(ctx, notif, group, member)
			}
			if channelSet["voice"] && d.voice != nil && member.Work.Valid && member.Work.String != "" {
				d.dispatchVoice(ctx, notif, group, member)
			}
			if channelSet["email"] && d.email != nil && member.Email.Valid && member.Email.String != "" {
				d.dispatchEmail(ctx, notif, group, member)
			}
		}
	}

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
		sid, status, lastErr = d.voice.Call(member.Work.String, notif.Message)
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
	// Email delivery is not yet implemented; record a failed delivery row so the
	// attempt is visible in the audit trail.
	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     uuidV7(),
		NotificationID: notif.NotificationID,
		Group:          group,
		Member:         member.Username,
		Channel:        "email",
		Status:         "failed",
		Attempt:        1,
		SentAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		d.logger.Error("dispatcher: insert email delivery failed",
			"notification_id", notif.NotificationID, "member", member.Username, "error", err)
		return
	}
	_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
		DeliveryID:   delivery.DeliveryID,
		ErrorMessage: sql.NullString{String: "email provider not implemented", Valid: true},
	})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
