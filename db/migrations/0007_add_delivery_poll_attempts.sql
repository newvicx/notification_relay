-- +goose Up
ALTER TABLE deliveries ADD COLUMN poll_attempts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE deliveries DROP COLUMN poll_attempts;
