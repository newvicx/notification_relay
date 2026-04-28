package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// Job represents a notification to be dispatched.
type Job struct {
	NotificationID string
}

// notifDestination mirrors the API Destination type for JSON parsing in the dispatcher.
type notifDestination struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
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

	d.setNotificationStatus(ctx, notif.NotificationID, "processing", "")

	var groups []string
	if notif.Groups.Valid && notif.Groups.String != "" {
		if err := json.Unmarshal([]byte(notif.Groups.String), &groups); err != nil {
			d.logger.Error("dispatcher: parse groups failed",
				"notification_id", job.NotificationID, "error", err)
			d.setNotificationStatus(ctx, notif.NotificationID, "failed", "failed to parse groups: "+err.Error())
			return
		}
	}

	var channels []string
	if notif.Channels.Valid && notif.Channels.String != "" {
		if err := json.Unmarshal([]byte(notif.Channels.String), &channels); err != nil {
			d.logger.Error("dispatcher: parse channels failed",
				"notification_id", job.NotificationID, "error", err)
			d.setNotificationStatus(ctx, notif.NotificationID, "failed", "failed to parse channels: "+err.Error())
			return
		}
	}

	var destinations []notifDestination
	if notif.Destinations.Valid && notif.Destinations.String != "" {
		if err := json.Unmarshal([]byte(notif.Destinations.String), &destinations); err != nil {
			d.logger.Error("dispatcher: parse destinations failed",
				"notification_id", job.NotificationID, "error", err)
			d.setNotificationStatus(ctx, notif.NotificationID, "failed", "failed to parse destinations: "+err.Error())
			return
		}
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
				if _, err := d.q.GetSMSSubscription(ctx, member.Username); err == nil {
					wg.Add(1)
					go func(g string, m db.GroupMember) {
						defer wg.Done()
						d.sem <- struct{}{}
						defer func() { <-d.sem }()
						d.dispatchSMS(ctx, notif, g, m)
					}(group, member)
				} else {
					d.logger.Debug("sms withheld: not subscribed", "member", member.Username)
					if _, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
						DeliveryID:     uuidV7(),
						NotificationID: notif.NotificationID,
						Group:          sql.NullString{String: group, Valid: true},
						Member:         sql.NullString{String: member.Username, Valid: true},
						Channel:        "sms",
						Status:         "not_subscribed",
						Attempt:        0,
						SentAt:         time.Now().UTC().Format(time.RFC3339),
					}); err != nil {
						d.logger.Error("dispatcher: insert not_subscribed delivery failed", "member", member.Username, "error", err)
					}
				}
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

	for _, dest := range destinations {
		wg.Add(1)
		go func(dst notifDestination) {
			defer wg.Done()
			d.sem <- struct{}{}
			defer func() { <-d.sem }()
			switch dst.Channel {
			case "sms":
				if d.sms != nil {
					if _, err := d.q.GetSMSSubscriptionByPhone(ctx, dst.Target); err == nil {
						d.dispatchSMSToDestination(ctx, notif, dst.Target)
					} else {
						d.logger.Warn("sms destination withheld: phone not registered", "target", dst.Target)
						if _, err := d.q.InsertDestinationDelivery(ctx, db.InsertDestinationDeliveryParams{
							DeliveryID:     uuidV7(),
							NotificationID: notif.NotificationID,
							Destination:    sql.NullString{String: dst.Target, Valid: true},
							Channel:        "sms",
							Status:         "not_subscribed",
							Attempt:        0,
							SentAt:         time.Now().UTC().Format(time.RFC3339),
						}); err != nil {
							d.logger.Error("dispatcher: insert not_subscribed delivery failed", "target", dst.Target, "error", err)
						}
					}
				}
			case "voice":
				if d.voice != nil {
					d.dispatchVoiceToDestination(ctx, notif, dst.Target)
				}
			case "email":
				if d.email != nil {
					d.dispatchEmailToDestination(ctx, notif, dst.Target)
				}
			}
		}(dest)
	}
	wg.Wait()

	// member_count tracks total delivery targets: group members + direct destinations.
	totalTargets := totalMembers + len(destinations)
	if totalTargets == 0 {
		d.setNotificationStatus(ctx, notif.NotificationID, "failed", "no members found for requested groups")
	} else {
		d.setNotificationStatus(ctx, notif.NotificationID, "completed", "")
	}

	if err := d.q.UpdateNotificationMemberCount(ctx, db.UpdateNotificationMemberCountParams{
		MemberCount:    int64(totalTargets),
		NotificationID: notif.NotificationID,
	}); err != nil {
		d.logger.Error("dispatcher: update member count failed",
			"notification_id", notif.NotificationID, "error", err)
	}
}

func (d *Dispatcher) setNotificationStatus(ctx context.Context, notificationID, status, errMsg string) {
	var em sql.NullString
	if errMsg != "" {
		em = sql.NullString{String: errMsg, Valid: true}
	}
	if err := d.q.UpdateNotificationStatus(ctx, db.UpdateNotificationStatusParams{
		Status:         status,
		ErrorMessage:   em,
		NotificationID: notificationID,
	}); err != nil {
		d.logger.Error("dispatcher: update notification status failed",
			"notification_id", notificationID, "error", err)
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

	if lastErr != nil {
		status = "failed"
	}

	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     uuidV7(),
		NotificationID: notif.NotificationID,
		Group:          sql.NullString{String: group, Valid: true},
		Member:         sql.NullString{String: member.Username, Valid: true},
		Channel:        "sms",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
		TwilioSid:      sql.NullString{String: sid, Valid: sid != ""},
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

	if lastErr != nil {
		status = "failed"
	}

	delivery, err := d.q.InsertDelivery(ctx, db.InsertDeliveryParams{
		DeliveryID:     uuidV7(),
		NotificationID: notif.NotificationID,
		Group:          sql.NullString{String: group, Valid: true},
		Member:         sql.NullString{String: member.Username, Valid: true},
		Channel:        "voice",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
		TwilioSid:      sql.NullString{String: sid, Valid: sid != ""},
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
			Group:          sql.NullString{String: group, Valid: true},
			Member:         sql.NullString{String: member.Username, Valid: true},
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

	// Parse user-supplied vars from the notification.
	userVars := make(map[string]any)
	if notif.EmailVars.Valid && notif.EmailVars.String != "" {
		if err := json.Unmarshal([]byte(notif.EmailVars.String), &userVars); err != nil {
			recordFailed("invalid email_vars JSON")
			return
		}
	}

	// Load the associated event so context vars can be injected.
	event, err := d.q.GetEventByEventID(ctx, notif.EventID)
	if err != nil {
		d.logger.Error("dispatcher: load event failed",
			"notification_id", notif.NotificationID, "event_id", notif.EventID, "error", err)
		recordFailed(fmt.Sprintf("failed to load event: %s", notif.EventID))
		return
	}

	// Merge user vars with notification/event context.
	vars := mergeEmailVars(userVars, notif, event)

	// Validate all required vars are present.
	var requiredVars []string
	if err := json.Unmarshal([]byte(tmpl.RequiredVars), &requiredVars); err != nil {
		recordFailed("template has invalid required_vars")
		return
	}
	for _, v := range requiredVars {
		if !walkPath(vars, v) {
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
		Group:          sql.NullString{String: group, Valid: true},
		Member:         sql.NullString{String: member.Username, Valid: true},
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

func (d *Dispatcher) dispatchSMSToDestination(ctx context.Context, notif db.Notification, target string) {
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
		sid, status, lastErr = d.sms.Send(target, notif.Message)
		if lastErr == nil {
			break
		}
		d.logger.Warn("dispatcher: sms destination send attempt failed",
			"notification_id", notif.NotificationID,
			"target", target,
			"attempt", finalAttempt,
			"error", lastErr)
	}

	if lastErr != nil {
		status = "failed"
	}

	delivery, err := d.q.InsertDestinationDelivery(ctx, db.InsertDestinationDeliveryParams{
		DeliveryID:     uuidV7(),
		NotificationID: notif.NotificationID,
		Destination:    sql.NullString{String: target, Valid: true},
		Channel:        "sms",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
		TwilioSid:      sql.NullString{String: sid, Valid: sid != ""},
	})
	if err != nil {
		d.logger.Error("dispatcher: insert sms destination delivery failed",
			"notification_id", notif.NotificationID, "target", target, "error", err)
		return
	}
	if lastErr != nil {
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: errorString(lastErr), Valid: true},
		})
	}
}

func (d *Dispatcher) dispatchVoiceToDestination(ctx context.Context, notif db.Notification, target string) {
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
		sid, status, lastErr = d.voice.Call(target, notif.Message)
		if lastErr == nil {
			break
		}
		d.logger.Warn("dispatcher: voice destination call attempt failed",
			"notification_id", notif.NotificationID,
			"target", target,
			"attempt", finalAttempt,
			"error", lastErr)
	}

	if lastErr != nil {
		status = "failed"
	}

	delivery, err := d.q.InsertDestinationDelivery(ctx, db.InsertDestinationDeliveryParams{
		DeliveryID:     uuidV7(),
		NotificationID: notif.NotificationID,
		Destination:    sql.NullString{String: target, Valid: true},
		Channel:        "voice",
		Status:         status,
		Attempt:        int64(finalAttempt),
		SentAt:         time.Now().UTC().Format(time.RFC3339),
		TwilioSid:      sql.NullString{String: sid, Valid: sid != ""},
	})
	if err != nil {
		d.logger.Error("dispatcher: insert voice destination delivery failed",
			"notification_id", notif.NotificationID, "target", target, "error", err)
		return
	}
	if lastErr != nil {
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: errorString(lastErr), Valid: true},
		})
	}
}

func (d *Dispatcher) dispatchEmailToDestination(ctx context.Context, notif db.Notification, target string) {
	deliveryID := uuidV7()
	sentAt := time.Now().UTC().Format(time.RFC3339)

	recordFailed := func(reason string) {
		delivery, err := d.q.InsertDestinationDelivery(ctx, db.InsertDestinationDeliveryParams{
			DeliveryID:     deliveryID,
			NotificationID: notif.NotificationID,
			Destination:    sql.NullString{String: target, Valid: true},
			Channel:        "email",
			Status:         "failed",
			EmailTemplate:  notif.EmailTemplate,
			EmailVars:      notif.EmailVars,
			Attempt:        1,
			SentAt:         sentAt,
		})
		if err != nil {
			d.logger.Error("dispatcher: insert email destination delivery failed",
				"notification_id", notif.NotificationID, "target", target, "error", err)
			return
		}
		_ = d.q.UpdateDeliveryError(ctx, db.UpdateDeliveryErrorParams{
			DeliveryID:   delivery.DeliveryID,
			ErrorMessage: sql.NullString{String: reason, Valid: true},
		})
	}

	if !notif.EmailTemplate.Valid || notif.EmailTemplate.String == "" {
		recordFailed("no email template specified")
		return
	}

	tmpl, err := d.q.GetEmailTemplateByName(ctx, notif.EmailTemplate.String)
	if err != nil {
		d.logger.Error("dispatcher: load email template failed",
			"template", notif.EmailTemplate.String, "error", err)
		recordFailed(fmt.Sprintf("template not found: %s", notif.EmailTemplate.String))
		return
	}

	userVars := make(map[string]any)
	if notif.EmailVars.Valid && notif.EmailVars.String != "" {
		if err := json.Unmarshal([]byte(notif.EmailVars.String), &userVars); err != nil {
			recordFailed("invalid email_vars JSON")
			return
		}
	}

	event, err := d.q.GetEventByEventID(ctx, notif.EventID)
	if err != nil {
		d.logger.Error("dispatcher: load event failed",
			"notification_id", notif.NotificationID, "event_id", notif.EventID, "error", err)
		recordFailed(fmt.Sprintf("failed to load event: %s", notif.EventID))
		return
	}

	vars := mergeEmailVars(userVars, notif, event)

	var requiredVars []string
	if err := json.Unmarshal([]byte(tmpl.RequiredVars), &requiredVars); err != nil {
		recordFailed("template has invalid required_vars")
		return
	}
	for _, v := range requiredVars {
		if !walkPath(vars, v) {
			recordFailed(fmt.Sprintf("missing required template variable: %s", v))
			return
		}
	}

	subject, body, err := RenderTemplate(tmpl.Subject, tmpl.Body, vars)
	if err != nil {
		d.logger.Error("dispatcher: render email template failed",
			"template", tmpl.TemplateName, "error", err)
		recordFailed(fmt.Sprintf("template render error: %v", err))
		return
	}

	sendErr := d.email.Send(ctx, target, subject, body)

	status := "sent"
	if sendErr != nil {
		status = "failed"
		d.logger.Warn("dispatcher: email destination send failed",
			"notification_id", notif.NotificationID,
			"target", target,
			"error", sendErr)
	}

	delivery, err := d.q.InsertDestinationDelivery(ctx, db.InsertDestinationDeliveryParams{
		DeliveryID:     deliveryID,
		NotificationID: notif.NotificationID,
		Destination:    sql.NullString{String: target, Valid: true},
		Channel:        "email",
		Status:         status,
		EmailTemplate:  notif.EmailTemplate,
		EmailVars:      notif.EmailVars,
		Attempt:        1,
		SentAt:         sentAt,
	})
	if err != nil {
		d.logger.Error("dispatcher: insert email destination delivery failed",
			"notification_id", notif.NotificationID, "target", target, "error", err)
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

// mergeEmailVars builds the final template data map. User-supplied vars from
// the notification's email_vars field occupy the top level. The reserved keys
// "notification" and "event" are always set from the real dispatch context —
// they overwrite any identically-named keys in the user vars.
//
// Templates can reference context fields directly, e.g.:
//
//	{{.notification.message}}  {{.event.severity}}  {{.event.name}}
func mergeEmailVars(userVars map[string]any, notif db.Notification, event db.Event) map[string]any {
	merged := make(map[string]any, len(userVars)+2)
	for k, v := range userVars {
		merged[k] = v
	}

	var groups []string
	if notif.Groups.Valid {
		json.Unmarshal([]byte(notif.Groups.String), &groups) // already validated upstream
	}

	merged["notification"] = map[string]any{
		"id":         notif.NotificationID,
		"message":    notif.Message,
		"created_at": notif.CreatedAt,
		"groups":     groups,
	}
	merged["event"] = map[string]any{
		"id":          event.EventID,
		"url":         event.EventUrl.String,
		"name":        event.EventName.String,
		"description": event.EventDescription.String,
		"severity":    event.EventSeverity.String,
		"start_time":  event.StartTime,
		"end_time":    event.EndTime.String,
	}
	return merged
}

// walkPath checks whether the dotted path (e.g. "server.host") exists and is
// non-nil in vars. Each dot-separated segment indexes one level of a
// map[string]any. A plain name with no dots checks a top-level key.
func walkPath(vars map[string]any, path string) bool {
	dot := strings.IndexByte(path, '.')
	if dot == -1 {
		_, ok := vars[path]
		return ok
	}
	val, ok := vars[path[:dot]]
	if !ok {
		return false
	}
	nested, ok := val.(map[string]any)
	if !ok {
		return false
	}
	return walkPath(nested, path[dot+1:])
}
