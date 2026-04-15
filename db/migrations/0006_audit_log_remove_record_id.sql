-- +goose Up
ALTER TABLE audit_log DROP COLUMN record_id;

-- +goose Down
ALTER TABLE audit_log ADD COLUMN record_id INTEGER;
