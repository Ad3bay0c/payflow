-- name: CreateUser :exec
INSERT INTO users (
    id, phone_number, email, full_name,
    kyc_status, tier, is_active,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CreateUserCredentials :exec
INSERT INTO user_credentials (user_id, password_hash, created_at, updated_at)
VALUES ($1, $2, $3, $4);

-- name: FindUserByID :one
SELECT *
FROM users
WHERE id = $1
  AND deleted_at IS NULL;

-- name: FindUserByPhone :one
SELECT *
FROM users
WHERE phone_number = $1
  AND deleted_at IS NULL;

-- name: FindPasswordHash :one
SELECT password_hash
FROM user_credentials
WHERE user_id = $1;

-- name: UpdateKYCStatus :exec
UPDATE users
SET kyc_status = $2,
    tier       = $3,
    updated_at = $4
WHERE id = $1
  AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users
SET deleted_at = $2,
    updated_at = $2
WHERE id = $1;

-- name: CreateAuditLog :exec
INSERT INTO auth_audit_log (id, user_id, event, ip_address, user_agent, metadata, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);
