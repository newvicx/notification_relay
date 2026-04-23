-- +goose Up
-- +goose StatementBegin

ALTER TABLE deliveries ADD COLUMN twilio_sid TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN before version 3.35.0; recreate the table.
CREATE TABLE deliveries_new (
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

INSERT INTO deliveries_new
    SELECT id, delivery_id, notification_id, "group", member, destination,
           channel, status, email_template, email_vars, attempt, poll_attempts,
           error_message, sent_at, completed_at
    FROM deliveries;

DROP TABLE deliveries;
ALTER TABLE deliveries_new RENAME TO deliveries;

CREATE INDEX IF NOT EXISTS idx_deliveries_notification_id ON deliveries (notification_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_delivery_id ON deliveries (delivery_id);

-- +goose StatementEnd
