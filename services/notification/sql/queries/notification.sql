-- name: CreateNotification :one
INSERT INTO notifications (
    id, transaction_id, user_id, recipient,
    channel, subject, body, status,
    created_at, updated_at
) VALUES (
             $1, $2, $3, $4, $5, $6, $7, 'pending', $8, $8
         )
    RETURNING *;

-- name: UpdateNotificationSent :exec
UPDATE notifications
SET status       = 'sent',
    provider_ref = $2,
    sent_at      = NOW(),
    attempts     = attempts + 1,
    last_attempt = NOW(),
    updated_at   = NOW()
WHERE id = $1;

-- name: UpdateNotificationFailed :exec
UPDATE notifications
SET status        = CASE WHEN attempts + 1 >= 3 THEN 'failed' ELSE 'pending' END,
    error_message = $2,
    attempts      = attempts + 1,
    last_attempt  = NOW(),
    updated_at    = NOW()
WHERE id = $1;

-- name: GetPendingNotifications :many
-- Retry failed notifications that haven't exceeded max attempts
SELECT * FROM notifications
WHERE status = 'pending'
  AND (last_attempt IS NULL OR last_attempt < NOW() - INTERVAL '60 seconds')
ORDER BY created_at ASC
    LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: GetNotificationsByTransaction :many
SELECT * FROM notifications
WHERE transaction_id = $1
ORDER BY created_at ASC;
