-- +goose Up
-- notifications.notification_id must be UNIQUE so that the FK from
-- deliveries(notification_id) references notifications(notification_id)
-- satisfies SQLite's requirement that the parent key column be either
-- the rowid/INTEGER PRIMARY KEY or subject to a UNIQUE constraint.
CREATE UNIQUE INDEX idx_notifications_notification_id_unique ON notifications (notification_id);

-- +goose Down
DROP INDEX IF EXISTS idx_notifications_notification_id_unique;
