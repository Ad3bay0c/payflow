-- name: GetAccountByWalletID :one
SELECT * FROM accounts
WHERE wallet_id = $1 AND is_active = true;

-- name: GetAccountByName :one
SELECT * FROM accounts
WHERE name = $1 AND is_active = true;

-- name: CreateAccount :one
INSERT INTO accounts (id, wallet_id, account_type, name, currency, created_at)
    VALUES ($1, $2, $3, $4, $5, $6)
    RETURNING *;

-- name: GetLatestEntry :one
-- Returns the most recent ledger entry for an account.
-- balance_after on this row IS the current balance for that account.
SELECT * FROM ledger_entries
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (
    id, transaction_id, entry_group_id,
    account_id, entry_type, amount,
    balance_after, currency, description, created_at
) VALUES (
             $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
         )
    RETURNING *;

-- name: GetEntriesByTransactionID :many
SELECT * FROM ledger_entries
WHERE transaction_id = $1
ORDER BY created_at ASC;

-- name: GetEntriesByAccountID :many
SELECT * FROM ledger_entries
WHERE account_id = $1
ORDER BY created_at DESC
    LIMIT $2 OFFSET $3;

-- name: GetEntryByTransactionAndAccount :one
-- Used for idempotency — check if we already processed this transaction
SELECT * FROM ledger_entries
WHERE transaction_id = $1
  AND account_id = $2
  AND entry_type = $3;

-- name: VerifyLedgerBalance :one
-- Audit query — sum of all debits minus credits for an account
-- Should always equal the balance_after of the latest entry
SELECT
    COALESCE(SUM(CASE WHEN entry_type = 'debit'  THEN amount ELSE 0 END), 0) as total_debits,
    COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE 0 END), 0) as total_credits
FROM ledger_entries
WHERE account_id = $1;
