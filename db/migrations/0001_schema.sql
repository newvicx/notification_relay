-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS group_members (
    group_name   TEXT NOT NULL,
    username     TEXT NOT NULL,
    display_name TEXT,
    email        TEXT,
    mobile       TEXT,
    work         TEXT,
    synced_at    TEXT NOT NULL,
    PRIMARY KEY (group_name, username)
);

CREATE TABLE IF NOT EXISTS events (
    id                INTEGER PRIMARY KEY,
    event_id          TEXT NOT NULL,
    event_url         TEXT,
    event_name        TEXT,
    event_description TEXT,
    event_severity    TEXT,
    start_time        TEXT NOT NULL,
    end_time          TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_event_id ON events (event_id);

CREATE TABLE IF NOT EXISTS notifications (
    id              INTEGER PRIMARY KEY,
    notification_id TEXT NOT NULL,
    event_id        TEXT NOT NULL,
    groups          TEXT,
    destinations    TEXT,
    channels        TEXT,
    message         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    error_message   TEXT,
    member_count    INTEGER NOT NULL,
    email_template  TEXT,
    email_vars      TEXT,
    created_at      TEXT NOT NULL,
    FOREIGN KEY (event_id)
        REFERENCES events(event_id)
        ON DELETE NO ACTION
        ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_notification_id ON notifications (notification_id);
CREATE INDEX IF NOT EXISTS idx_notifications_event_id ON notifications (event_id);

CREATE TABLE IF NOT EXISTS deliveries (
    id              INTEGER PRIMARY KEY,
    delivery_id     TEXT NOT NULL,
    notification_id TEXT NOT NULL,
    "group"         TEXT,
    member          TEXT,
    destination     TEXT,
    channel         TEXT NOT NULL,
    status          TEXT NOT NULL,
    email_template  TEXT,
    email_vars      TEXT,
    attempt         INTEGER NOT NULL DEFAULT 1,
    poll_attempts   INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    sent_at         TEXT NOT NULL,
    completed_at    TEXT,
    FOREIGN KEY (notification_id)
        REFERENCES notifications(notification_id)
        ON DELETE NO ACTION
        ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_deliveries_notification_id ON deliveries (notification_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_delivery_id ON deliveries (delivery_id);

CREATE TABLE IF NOT EXISTS email_templates (
    id            INTEGER PRIMARY KEY,
    template_name TEXT NOT NULL,
    subject       TEXT NOT NULL,
    body          TEXT NOT NULL,
    required_vars TEXT NOT NULL,
    description   TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_templates_template_name ON email_templates (template_name);

CREATE TABLE IF NOT EXISTS audit_log (
    id             INTEGER PRIMARY KEY,
    timestamp      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    username       TEXT NOT NULL,
    ip_address     TEXT,
    action         TEXT NOT NULL,
    impacted_table TEXT NOT NULL,
    old_values     TEXT,
    new_values     TEXT
);

CREATE TABLE IF NOT EXISTS sync_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    group_name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS sync_groups;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS email_templates;
DROP TABLE IF EXISTS deliveries;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS group_members;

-- +goose StatementEnd
