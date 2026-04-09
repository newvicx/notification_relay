package notify

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// Poller periodically queries Twilio for the completion status of in-flight
// voice and SMS deliveries. It is needed because the application is behind a
// firewall that prevents Twilio from delivering webhook callbacks.
type Poller struct {
	cfg    config.TwilioConfig
	writer *sql.DB
	q      *db.Queries
	logger *slog.Logger
}

func NewPoller(cfg config.TwilioConfig, writer *sql.DB, logger *slog.Logger) *Poller {
	return &Poller{
		cfg:    cfg,
		writer: writer,
		q:      db.New(writer),
		logger: logger,
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

// checkDelivery queries Twilio for the status of a single delivery and updates
// the DB accordingly. The delivery_id is used as the Twilio call/message SID.
func (p *Poller) checkDelivery(ctx context.Context, d db.Delivery) {
	// TODO: implement Twilio REST API call
	// GET /Accounts/{AccountSID}/Calls/{d.DeliveryID}.json  (voice)
	// GET /Accounts/{AccountSID}/Messages/{d.DeliveryID}.json  (sms)
	//
	// Status mapping:
	//   "completed"                          -> "delivered"
	//   "failed"/"busy"/"no-answer"/"canceled" -> "failed"
	//   anything else                          -> leave as "in_flight"
	p.logger.Debug("poller: check delivery (not yet implemented)", "delivery_id", d.DeliveryID, "channel", d.Channel)
}
