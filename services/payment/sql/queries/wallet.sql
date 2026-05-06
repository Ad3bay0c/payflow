-- name: CreateWallet :one
INSERT INTO wallets (id, user_id, currency, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
    RETURNING *;

-- name: GetWalletByID :one
SELECT * FROM wallets
WHERE id = $1 AND is_active = true;

-- name: GetWalletByUserID :one
SELECT * FROM wallets
WHERE user_id = $1 AND is_active = true;

-- name: GetWalletByIDForUpdate :one
SELECT * FROM wallets
WHERE id = $1 AND is_active = true
    FOR UPDATE;

-- name: UpdateWalletBalance :one
-- Optimistic locking — the WHERE clause includes the version we read.
-- If another transaction modified this wallet since we read it,
-- version will have changed and this update returns 0 rows.
-- The application detects 0 rows and retries with fresh data.
UPDATE wallets
SET balance    = $2,
    version    = version + 1,
    updated_at = $3
WHERE id      = $1
  AND version = $4
    RETURNING *;

-- name: FundWallet :one
-- Direct balance addition — used for funding (no optimistic lock needed
-- since funding always adds, never subtracts, making it safe to retry)
UPDATE wallets
SET balance    = balance + $2,
    updated_at = $3
WHERE id = $1
  AND is_active = true
    RETURNING *;
