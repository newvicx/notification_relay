-- +goose Up
-- +goose StatementBegin

ALTER TABLE events ADD COLUMN created_by TEXT;
ALTER TABLE events ADD COLUMN created_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00';
ALTER TABLE events ADD COLUMN modified_by TEXT;
ALTER TABLE events ADD COLUMN modified_at TEXT;

ALTER TABLE notifications ADD COLUMN created_by TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE events DROP COLUMN created_by;
ALTER TABLE events DROP COLUMN created_at;
ALTER TABLE events DROP COLUMN modified_by;
ALTER TABLE events DROP COLUMN modified_at;

ALTER TABLE notifications DROP COLUMN created_by;

-- +goose StatementEnd
