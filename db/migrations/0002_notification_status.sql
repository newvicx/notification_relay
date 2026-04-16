-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifications ADD COLUMN status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE notifications ADD COLUMN error_message TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notifications DROP COLUMN error_message;
ALTER TABLE notifications DROP COLUMN status;
-- +goose StatementEnd
