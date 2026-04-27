package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// Poller periodically queries Twilio for the completion status of in-flight
// voice and SMS deliveries. It is needed because the application is behind a
// firewall that prevents Twilio from delivering webhook callbacks.
type Poller struct {
	cfg     config.TwilioConfig
	writer  *sql.DB
	q       *db.Queries
	client  *http.Client
	baseURL string
	logger  *slog.Logger
}

func NewPoller(cfg config.TwilioConfig, writer *sql.DB, logger *slog.Logger) *Poller {
	return &Poller{
		cfg:     cfg,
		writer:  writer,
		q:       db.New(writer),
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://api.twilio.com",
		logger:  logger,
	}
}

// Run starts the polling loop. It returns when ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	p.logger.Info("twilio poller started", "interval", p.cfg.PollInterval.String())

	for {
		select {
		case <-ticker.C:
			p.pollPending(ctx)
		case <-ctx.Done():
			p.logger.Info("twilio poller shutting down")
			return
		}
	}
}

func (p *Poller) RunPollFailed(ctx context.Context) {
	deliveries, err := p.q.ListPollFailedDeliveries(ctx)
	if err != nil {
		p.logger.Error("poller: list poll_failed deliveries failed", "error", err)
		return
	}
	err = p.q.ResetPollAttempts(ctx)
	if err != nil {
		p.logger.Error("poller: reset poll attempts failed", "error", err)
		return
	}

	p.logger.Debug("poller: checking poll failed deliveries", "deliveries", len(deliveries))

	for _, d := range deliveries {
		p.checkDelivery(ctx, d)
	}

	p.logger.Info("done checking poll_failed deliveries")

}

func (p *Poller) pollPending(ctx context.Context) {
	voice, err := p.q.ListInFlightVoiceDeliveries(ctx)
	if err != nil {
		p.logger.Error("poller: list in-flight voice deliveries failed", "error", err)
		return
	}
	sms, err := p.q.ListInFlightSMSDeliveries(ctx)
	if err != nil {
		p.logger.Error("poller: list in-flight sms deliveries failed", "error", err)
		return
	}

	deliveries := append(voice, sms...)
	if len(deliveries) == 0 {
		return
	}

	p.logger.Debug("poller: checking in-flight deliveries", "voice", len(voice), "sms", len(sms))

	for _, d := range deliveries {
		p.checkDelivery(ctx, d)
	}
}

// twilioStatusResponse holds only the fields we need from Twilio's resource JSON.
type twilioStatusResponse struct {
	Status       string `json:"status"`
	ErrorCode    *int   `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// checkDelivery queries Twilio for the status of a single delivery and updates
// the DB accordingly.
func (p *Poller) checkDelivery(ctx context.Context, d db.Delivery) {
	if !d.TwilioSid.Valid || d.TwilioSid.String == "" {
		const msg = "delivery is in-flight but has no twilio_sid; this indicates a bug or direct DB modification"
		p.logger.Error("poller: malformed delivery — missing twilio_sid",
			"delivery_id", d.DeliveryID, "channel", d.Channel)
		if err := p.q.UpdateDeliveryStatus(ctx, db.UpdateDeliveryStatusParams{
			Status:       "malformed",
			CompletedAt:  sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
			ErrorMessage: sql.NullString{String: msg, Valid: true},
			DeliveryID:   d.DeliveryID,
		}); err != nil {
			p.logger.Error("poller: failed to mark delivery malformed",
				"delivery_id", d.DeliveryID, "error", err)
		}
		return
	}

	var apiURL string
	switch d.Channel {
	case "voice":
		apiURL = fmt.Sprintf(
			"%s/2010-04-01/Accounts/%s/Calls/%s.json",
			p.baseURL, p.cfg.AccountSID, d.TwilioSid.String,
		)
	default: // sms
		apiURL = fmt.Sprintf(
			"%s/2010-04-01/Accounts/%s/Messages/%s.json",
			p.baseURL, p.cfg.AccountSID, d.TwilioSid.String,
		)
	}

	body, err := twilioGet(p.client, p.cfg.TokenSID, p.cfg.AuthToken, apiURL)
	if err != nil {
		p.logger.Error("poller: twilio status fetch failed",
			"delivery_id", d.DeliveryID, "channel", d.Channel, "error", err)
		p.recordPollFailure(ctx, d, fmt.Sprintf("status fetch failed: %v", err))
		return
	}

	var r twilioStatusResponse
	if err := json.Unmarshal(body, &r); err != nil {
		p.logger.Error("poller: parse twilio status response failed",
			"delivery_id", d.DeliveryID, "error", err)
		p.recordPollFailure(ctx, d, fmt.Sprintf("parse response failed: %v", err))
		return
	}

	mapped, terminal := mapTwilioStatus(d.Channel, r.Status)
	if !terminal {
		return // still in flight; check again next poll cycle
	}

	var errMsg sql.NullString
	if r.ErrorCode != nil && *r.ErrorCode != 0 {
		errMsg = sql.NullString{
			String: fmt.Sprintf("%d: %s", *r.ErrorCode, r.ErrorMessage),
			Valid:  true,
		}
	}

	if err := p.q.UpdateDeliveryStatus(ctx, db.UpdateDeliveryStatusParams{
		Status:       mapped,
		CompletedAt:  sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
		ErrorMessage: errMsg,
		DeliveryID:   d.DeliveryID,
	}); err != nil {
		p.logger.Error("poller: update delivery status failed",
			"delivery_id", d.DeliveryID, "status", mapped, "error", err)
		return
	}

	p.logger.Info("poller: delivery completed",
		"delivery_id", d.DeliveryID, "channel", d.Channel, "status", mapped)
}

// recordPollFailure increments poll_attempts for the delivery. If the
// configured PollAttemptLimit is reached the delivery is marked "poll_failed"
// and will no longer be returned by the in-flight queries, preventing
// unbounded queue growth when Twilio is unreachable.
func (p *Poller) recordPollFailure(ctx context.Context, d db.Delivery, reason string) {
	updated, err := p.q.IncrementPollAttempts(ctx, d.DeliveryID)
	if err != nil {
		p.logger.Error("poller: increment poll attempts failed",
			"delivery_id", d.DeliveryID, "error", err)
		return
	}

	limit := p.cfg.PollAttemptLimit
	if limit <= 0 || updated.PollAttempts < int64(limit) {
		return
	}

	if err := p.q.UpdateDeliveryStatus(ctx, db.UpdateDeliveryStatusParams{
		Status:       "poll_failed",
		CompletedAt:  sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true},
		ErrorMessage: sql.NullString{String: reason, Valid: true},
		DeliveryID:   d.DeliveryID,
	}); err != nil {
		p.logger.Error("poller: mark delivery poll_failed failed",
			"delivery_id", d.DeliveryID, "error", err)
		return
	}

	p.logger.Warn("poller: delivery marked poll_failed, giving up",
		"delivery_id", d.DeliveryID,
		"channel", d.Channel,
		"poll_attempts", updated.PollAttempts,
		"reason", reason)
}

// mapTwilioStatus maps a raw Twilio status string to our internal status and
// reports whether it is a terminal (non-polling) state.
//
// Voice terminal statuses: completed, failed, busy, no-answer, canceled
// SMS terminal statuses:   delivered, undelivered, failed
func mapTwilioStatus(channel, twilioStatus string) (status string, terminal bool) {
	switch channel {
	case "voice":
		switch twilioStatus {
		case "completed", "failed", "busy", "no-answer", "canceled":
			return twilioStatus, true
		}
	case "sms":
		switch twilioStatus {
		case "delivered", "undelivered", "failed":
			return twilioStatus, true
		}
	}
	return twilioStatus, false
}
