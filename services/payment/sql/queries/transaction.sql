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
