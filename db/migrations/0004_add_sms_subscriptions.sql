-- +goose Up
CREATE TABLE IF NOT EXISTS sms_subscriptions (
    username      TEXT NOT NULL PRIMARY KEY,
    phone         TEXT NOT NULL,
    subscribed_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS sms_subscriptions;
