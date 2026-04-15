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
// the DB accordingly. The delivery_id is used as the Twilio call/message SID.
func (p *Poller) checkDelivery(ctx context.Context, d db.Delivery) {
	var apiURL string
	switch d.Channel {
	case "voice":
		apiURL = fmt.Sprintf(
			"%s/2010-04-01/Accounts/%s/Calls/%s.json",
			p.baseURL, p.cfg.AccountSID, d.DeliveryID,
		)
	default: // sms
		apiURL = fmt.Sprintf(
			"%s/2010-04-01/Accounts/%s/Messages/%s.json",
			p.baseURL, p.cfg.AccountSID, d.DeliveryID,
		)
	}

	body, err := twilioGet(p.client, p.cfg.TokenSID, p.cfg.AuthToken, apiURL)
	if err != nil {
		p.logger.Error("poller: twilio status fetch failed",
			"delivery_id", d.DeliveryID, "channel", d.Channel, "error", err)
		return
	}

	var r twilioStatusResponse
	if err := json.Unmarshal(body, &r); err != nil {
		p.logger.Error("poller: parse twilio status response failed",
			"delivery_id", d.DeliveryID, "error", err)
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
