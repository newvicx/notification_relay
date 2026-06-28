-- +goose Up
ALTER TABLE events ADD COLUMN auto_closed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE events DROP COLUMN auto_closed;
