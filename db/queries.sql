-- sync_groups

-- name: ListSyncGroups :many
SELECT * FROM sync_groups ORDER BY group_name;

-- name: GetSyncGroup :one
SELECT * FROM sync_groups WHERE group_name = ? LIMIT 1;

-- name: InsertSyncGroup :one
INSERT INTO sync_groups (group_name, created_by) VALUES (?, ?) RETURNING *;

-- name: DeleteSyncGroup :exec
DELETE FROM sync_groups WHERE group_name = ?;

-- group_members

-- name: InsertGroupMember :exec
INSERT INTO group_members (group_name, username, display_name, email, mobile, work, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteGroupMembers :exec
DELETE FROM group_members WHERE group_name = ?;

-- name: ListGroupMembers :many
SELECT * FROM group_members WHERE group_name = ? ORDER BY username;

-- name: GetGroupMember :one
SELECT * FROM group_members WHERE group_name = ? AND username = ?;

-- name: ListDistinctGroupNames :many
SELECT DISTINCT group_name FROM group_members ORDER BY group_name;

-- events

-- name: InsertEvent :one
INSERT INTO events (event_id, event_url, event_name, event_description, event_severity, start_time, end_time, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = ?;

-- name: GetEventByEventID :one
SELECT * FROM events WHERE event_id = ?;

-- name: UpdateEventEndTime :exec
UPDATE events SET end_time = ?, modified_at = ?, modified_by = ? WHERE event_id = ?;

-- name: ListEvents :many
SELECT * FROM events ORDER BY start_time DESC LIMIT ? OFFSET ?;

-- name: ListEventsFiltered :many
SELECT * FROM events
WHERE (:start_from = '' OR start_time >= :start_from)
  AND (:start_to = '' OR start_time <= :start_to)
  AND (:event_id = '' OR LOWER(event_id) LIKE '%' || LOWER(:event_id) || '%')
  AND (:event_name = '' OR LOWER(event_name) LIKE '%' || LOWER(:event_name) || '%')
  AND (:description = '' OR LOWER(event_description) LIKE '%' || LOWER(:description) || '%')
  AND (:severity = '' OR event_severity = :severity)
  AND (:created_by = '' OR LOWER(created_by) LIKE '%' || LOWER(:created_by) || '%')
  AND (:status = '' OR (:status = 'active' AND end_time IS NULL) OR (:status = 'ended' AND end_time IS NOT NULL))
ORDER BY start_time DESC
LIMIT :limit OFFSET :offset;

-- name: UpdateEvent :one
UPDATE events
SET event_url = ?, event_name = ?, event_description = ?, event_severity = ?,
    modified_by = ?, modified_at = ?
WHERE event_id = ?
RETURNING *;

-- notifications

-- name: InsertNotification :one
INSERT INTO notifications
    (notification_id, event_id, groups, destinations, channels, message, member_count,
     email_template, email_vars, created_at, created_by, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetNotificationByID :one
SELECT * FROM notifications WHERE id = ?;

-- name: GetNotificationByNotificationID :one
SELECT * FROM notifications WHERE notification_id = ?;

-- name: ListNotificationsByEventID :many
SELECT * FROM notifications WHERE event_id = ? ORDER BY created_at DESC;

-- name: UpdateNotificationMemberCount :exec
UPDATE notifications SET member_count = ? WHERE notification_id = ?;

-- name: UpdateNotificationStatus :exec
UPDATE notifications SET status = ?, error_message = ? WHERE notification_id = ?;

-- deliveries

-- name: InsertDelivery :one
INSERT INTO deliveries (delivery_id, notification_id, "group", member, channel, status, email_template, email_vars, attempt, sent_at, twilio_sid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: InsertDestinationDelivery :one
INSERT INTO deliveries (delivery_id, notification_id, destination, channel, status, email_template, email_vars, attempt, sent_at, twilio_sid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDeliveryByDeliveryID :one
SELECT * FROM deliveries WHERE delivery_id = ?;

-- name: ListDeliveriesByNotificationID :many
SELECT * FROM deliveries WHERE notification_id = ? ORDER BY sent_at DESC;

-- name: ListInFlightVoiceDeliveries :many
-- Non-terminal Twilio voice statuses: queued, ringing, in-progress
SELECT * FROM deliveries
WHERE channel = 'voice'
  AND status IN ('queued', 'ringing', 'in-progress')
ORDER BY sent_at;

-- name: ListPollFailedDeliveries :many
SELECT * FROM deliveries WHERE status = 'poll_failed';

-- name: ListInFlightSMSDeliveries :many
-- Non-terminal Twilio message statuses: accepted, scheduled, queued, sending, sent
-- "sent" means the carrier accepted the message but delivery is not yet confirmed;
-- polling continues until "delivered" or a terminal failure status is received.
SELECT * FROM deliveries
WHERE channel = 'sms'
  AND status IN ('accepted', 'scheduled', 'queued', 'sending', 'sent')
ORDER BY sent_at;

-- name: IncrementPollAttempts :one
-- Atomically increments poll_attempts and returns the updated delivery row,
-- allowing the caller to decide whether the attempt limit has been reached.
UPDATE deliveries
SET poll_attempts = poll_attempts + 1
WHERE delivery_id = ?
RETURNING *;

-- name: ResetPollAttempts :exec
UPDATE deliveries
SET poll_attempts = 0
WHERE status = 'poll_failed';

-- name: UpdateDeliveryStatus :exec
-- error_message should include the Twilio error code when applicable,
-- e.g. "queue overflow: 30001"
UPDATE deliveries
SET status = ?, completed_at = ?, error_message = ?
WHERE delivery_id = ?;

-- name: IncrementDeliveryAttempt :exec
UPDATE deliveries
SET attempt = attempt + 1, sent_at = ?
WHERE delivery_id = ?;

-- name: UpdateDeliveryError :exec
-- Records an error on a delivery without changing its status or completion time.
-- Called when the application fails to dispatch to the delivery service (e.g.
-- network error calling Twilio or SMTP), as distinct from a terminal status
-- returned by the provider.
UPDATE deliveries
SET error_message = ?
WHERE delivery_id = ?;

-- email_templates

-- name: InsertEmailTemplate :one
INSERT INTO email_templates (template_name, subject, body, required_vars, description)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetEmailTemplateByName :one
SELECT * FROM email_templates WHERE template_name = ?;

-- name: ListEmailTemplates :many
SELECT * FROM email_templates ORDER BY template_name;

-- name: UpdateEmailTemplate :exec
UPDATE email_templates
SET subject = ?, body = ?, required_vars = ?, description = ?
WHERE template_name = ?;

-- name: DeleteEmailTemplate :exec
DELETE FROM email_templates WHERE template_name = ?;

-- audit_log

-- name: InsertAuditLog :exec
INSERT INTO audit_log (timestamp, username, ip_address, action, impacted_table, old_values, new_values)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditLogFiltered :many
SELECT * FROM audit_log
WHERE (:username = '' OR username = :username)
  AND (:from_time = '' OR timestamp >= :from_time)
  AND (:to_time = '' OR timestamp <= :to_time)
ORDER BY timestamp DESC
LIMIT :limit OFFSET :offset;

-- sms_subscriptions

-- name: GetSMSSubscription :one
SELECT * FROM sms_subscriptions WHERE LOWER(username) = LOWER(?);

-- name: GetSMSSubscriptionByPhone :one
SELECT * FROM sms_subscriptions WHERE phone = ?;

-- name: InsertSMSSubscription :exec
INSERT INTO sms_subscriptions (username, phone, subscribed_at) VALUES (?, ?, ?);

-- name: ListSMSSubscriptions :many
SELECT * FROM sms_subscriptions ORDER BY subscribed_at DESC;

-- name: DeleteSMSSubscription :exec
DELETE FROM sms_subscriptions WHERE username = ?;
