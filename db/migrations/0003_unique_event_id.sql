-- +goose Up
-- events.event_id must be UNIQUE so that the FK from notifications(event_id)
-- references events(event_id) satisfies SQLite's requirement that the parent
-- key column be either the rowid/INTEGER PRIMARY KEY or subject to a UNIQUE
-- constraint. Without this, SQLite raises "foreign key mismatch" when FK
-- enforcement is enabled.
DROP INDEX IF EXISTS idx_events_event_id;
CREATE UNIQUE INDEX idx_events_event_id ON events (event_id);

-- +goose Down
DROP INDEX IF EXISTS idx_events_event_id;
CREATE INDEX idx_events_event_id ON events (event_id);
