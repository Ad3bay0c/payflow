-- name: CreateWallet :one
INSERT INTO wallets (id, user_id, currency, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
    RETURNING *;

-- name: GetWalletByID :one
SELECT * FROM wallets
WHERE id = $1
  AND is_active = true;

-- name: GetWalletByIDAndUserID :one
SELECT * FROM wallets
WHERE id = $1
  AND user_id = $2
  AND is_active = true;

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
UPDATE wallets
SET balance    = balance + $2,
    version = version + 1,
    updated_at = $3
WHERE id = $1
  AND is_active = true
    RETURNING *;

-- name: GetTierLimit :one
SELECT * FROM tier_limits WHERE tier = $1;

-- name: GetDailyTransferTotal :one
-- Returns how much a wallet has transferred today.
-- Returns 0 if no transfers yet today.
SELECT COALESCE(total_kobo, 0) as total_kobo
FROM daily_transfer_summary
WHERE wallet_id = $1
  AND date = CURRENT_DATE;

-- name: UpsertDailyTransferSummary :exec
-- Atomically adds to today's transfer total.
-- INSERT on first transfer of the day, UPDATE on subsequent ones.
INSERT INTO daily_transfer_summary (wallet_id, date, total_kobo, updated_at)
VALUES ($1, CURRENT_DATE, $2, NOW())
    ON CONFLICT (wallet_id, date)
DO UPDATE SET
    total_kobo = daily_transfer_summary.total_kobo + $2,
           updated_at = NOW();

-- name: GetFeeTiers :many
-- Returns all active fee tiers ordered by max_amount ascending.
SELECT * FROM fee_tiers
WHERE is_active = true
ORDER BY id ASC;
