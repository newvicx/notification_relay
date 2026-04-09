-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifications ADD COLUMN channels TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite does not support DROP COLUMN in older versions; recreate the table.
CREATE TABLE notifications_backup AS SELECT id, notification_id, event_id, groups, message, member_count, created_at FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_backup RENAME TO notifications;
CREATE INDEX IF NOT EXISTS idx_notifications_event_id ON notifications (event_id);
CREATE INDEX IF NOT EXISTS idx_notifications_notification_id ON notifications (notification_id);
-- +goose StatementEnd
