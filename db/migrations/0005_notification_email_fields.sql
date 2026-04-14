-- +goose Up
ALTER TABLE notifications ADD COLUMN email_template TEXT;
ALTER TABLE notifications ADD COLUMN email_vars     TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; migration is not reversible.
