package notify

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"notification_relay/config"
	"notification_relay/db"
	"notification_relay/testutil"
)

func insertTestEvent(t *testing.T, q *db.Queries, eventID, startTime string) {
	t.Helper()
	_, err := q.InsertEvent(context.Background(), db.InsertEventParams{
		EventID:   eventID,
		StartTime: startTime,
		CreatedAt: startTime,
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func TestSweeperAutoClosesStaleOpenEvents(t *testing.T) {
	conn, q := testutil.OpenDB(t)

	stale := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)

	insertTestEvent(t, q, "stale-event", stale)
	insertTestEvent(t, q, "fresh-event", fresh)

	sweeper := NewSweeper(config.EventSweepConfig{
		TTL:      time.Hour,
		Interval: time.Minute,
	}, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sweeper.sweep(context.Background())

	staleEvent, err := q.GetEventByEventID(context.Background(), "stale-event")
	if err != nil {
		t.Fatalf("get stale event: %v", err)
	}
	if !staleEvent.EndTime.Valid || staleEvent.EndTime.String == "" {
		t.Errorf("expected stale event to be closed, end_time is empty")
	}
	if staleEvent.AutoClosed == 0 {
		t.Errorf("expected stale event to be flagged auto_closed")
	}

	freshEvent, err := q.GetEventByEventID(context.Background(), "fresh-event")
	if err != nil {
		t.Fatalf("get fresh event: %v", err)
	}
	if freshEvent.EndTime.Valid {
		t.Errorf("expected fresh event to remain open, got end_time=%q", freshEvent.EndTime.String)
	}
}

func TestSweeperIgnoresAlreadyClosedEvents(t *testing.T) {
	conn, q := testutil.OpenDB(t)

	stale := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	insertTestEvent(t, q, "closed-event", stale)

	endTime := time.Now().UTC().Format(time.RFC3339)
	if err := q.UpdateEventEndTime(context.Background(), db.UpdateEventEndTimeParams{
		EndTime: sql.NullString{String: endTime, Valid: true},
		EventID: "closed-event",
	}); err != nil {
		t.Fatalf("update event end time: %v", err)
	}

	sweeper := NewSweeper(config.EventSweepConfig{
		TTL:      time.Hour,
		Interval: time.Minute,
	}, conn, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sweeper.sweep(context.Background())

	event, err := q.GetEventByEventID(context.Background(), "closed-event")
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if event.AutoClosed != 0 {
		t.Errorf("expected already-closed event to be untouched, but auto_closed was set")
	}
	if event.EndTime.String != endTime {
		t.Errorf("expected end_time to remain %q, got %q", endTime, event.EndTime.String)
	}
}
