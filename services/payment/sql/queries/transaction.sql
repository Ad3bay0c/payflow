-- name: CreateTransaction :one
INSERT INTO transactions (
    id, idempotency_key,
    sender_wallet_id, receiver_wallet_id,
    amount, fee, currency,
    status, type, description, metadata,
    created_at, updated_at
) VALUES (
             $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
         )
    RETURNING *;

-- name: GetTransactionByID :one
SELECT * FROM transactions WHERE id = $1;

-- name: GetTransactionByIdempotencyKey :one
-- Check for duplicate requests before processing.
-- If this returns a row, return it directly — never process twice.
SELECT * FROM transactions WHERE idempotency_key = $1;

-- name: UpdateTransactionStatus :one
UPDATE transactions
SET status       = $2,
    completed_at = $3,
    failed_at    = $4,
    updated_at   = $5
WHERE id = $1
    RETURNING *;

-- name: ListTransactionsByWallet :many
-- Paginated transaction history for a wallet.
-- Returns both sent and received transactions, newest first.
SELECT * FROM transactions
WHERE sender_wallet_id = @wallet_id
   OR receiver_wallet_id = @wallet_id
ORDER BY created_at DESC
    LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountTransactionsByWallet :one
SELECT COUNT(*) FROM transactions
WHERE sender_wallet_id = @wallet_id
   OR receiver_wallet_id = @wallet_id;

-- name: CreateOutboxEvent :one
INSERT INTO outbox_events (id, topic, message_key, payload, created_at)
VALUES ($1, $2, $3, $4, $5)
    RETURNING *;

-- name: GetPendingOutboxEvents :many
-- Relay fetches pending events in batches ordered by creation time.
-- SKIP LOCKED means multiple relay instances can run safely —
-- each instance locks the rows it's processing, others skip them.
SELECT * FROM outbox_events
WHERE status = 'pending'
  AND (last_attempt IS NULL OR last_attempt < NOW() - INTERVAL '30 seconds')
ORDER BY created_at ASC
    LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events
SET status       = 'published',
    published_at = NOW()
WHERE id = $1;

-- name: MarkOutboxEventFailed :exec
UPDATE outbox_events
SET status       = 'failed',
    attempts     = attempts + 1,
    last_attempt = NOW()
WHERE id = $1;

-- name: IncrementOutboxAttempt :exec
UPDATE outbox_events
SET attempts     = attempts + 1,
    last_attempt = NOW(),
    status       = CASE
                       WHEN attempts + 1 >= 5 THEN 'failed'
                       ELSE 'pending'
        END
WHERE id = $1;
