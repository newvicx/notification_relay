package notify

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"notification_relay/config"
	"notification_relay/db"
)

// auditSystemUser identifies sweep-driven changes in the audit log, since
// there is no authenticated user behind an automatic action.
const auditSystemUser = "system:event-sweeper"

// Sweeper periodically auto-closes events that have been open (end_time IS
// NULL) longer than the configured TTL. It exists because the SMTP ingestion
// path has no way to revisit an event after creation, and even the HTTP API
// only closes an event if the caller remembers to. Auto-closed events are
// flagged via auto_closed so callers can tell "we stopped waiting" apart from
// a real resolution.
type Sweeper struct {
	cfg    config.EventSweepConfig
	q      *db.Queries
	logger *slog.Logger
}

func NewSweeper(cfg config.EventSweepConfig, writer *sql.DB, logger *slog.Logger) *Sweeper {
	return &Sweeper{
		cfg:    cfg,
		q:      db.New(writer),
		logger: logger,
	}
}

// Run starts the sweep loop. It returns when ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	s.logger.Info("event sweeper started", "ttl", s.cfg.TTL.String(), "interval", s.cfg.Interval.String())

	for {
		select {
		case <-ticker.C:
			s.sweep(ctx)
		case <-ctx.Done():
			s.logger.Info("event sweeper shutting down")
			return
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	cutoff := time.Now().UTC().Add(-s.cfg.TTL).Format(time.RFC3339)

	closed, err := s.q.AutoCloseStaleEvents(ctx, db.AutoCloseStaleEventsParams{
		EndTime:    sql.NullString{String: now, Valid: true},
		ModifiedAt: sql.NullString{String: now, Valid: true},
		ModifiedBy: sql.NullString{String: auditSystemUser, Valid: true},
		StartTime:  cutoff,
	})
	if err != nil {
		s.logger.Error("sweeper: auto-close stale events failed", "error", err)
		return
	}
	if len(closed) == 0 {
		return
	}

	for _, eventID := range closed {
		if err := s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			Timestamp:     now,
			Username:      auditSystemUser,
			Action:        "auto_close_event",
			ImpactedTable: "events",
			OldValues:     sql.NullString{String: eventID, Valid: true},
		}); err != nil {
			s.logger.Error("sweeper: write audit log failed", "event_id", eventID, "error", err)
		}
	}

	s.logger.Info("sweeper: auto-closed stale events", "count", len(closed))
}
