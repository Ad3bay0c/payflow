BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Users
-- Core identity table. Phone number is the primary identifier in Nigerian
-- fintech — users know their phone number, not necessarily their email.
CREATE TABLE users (
       id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
       phone_number VARCHAR(15) NOT NULL,
       email        VARCHAR(255),
       full_name    VARCHAR(255) NOT NULL,

-- KYC status maps directly to CBN's tiered KYC framework.
-- pending → basic (phone verified) → verified (BVN) → full (documents)
       kyc_status   VARCHAR(20) NOT NULL DEFAULT 'pending'
           CHECK (kyc_status IN ('pending', 'basic', 'verified', 'full')),

-- Tier controls transaction limits per CBN regulation:
-- Tier 1: ₦50,000/day  Tier 2: ₦500,000/day  Tier 3: ₦5,000,000/day
       tier         SMALLINT    NOT NULL DEFAULT 1
           CHECK (tier IN (1, 2, 3)),

       is_active    BOOLEAN     NOT NULL DEFAULT true,

-- deleted_at enables soft deletes — CBN requires user data retained
-- for 5 years after account closure. Hard deletes are never used.
       created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_users_phone ON users (phone_number) WHERE deleted_at IS NULL;
CREATE INDEX        idx_users_email ON users (email)        WHERE deleted_at IS NULL;

-- User Credentials
-- Password hashes stored separately from the user profile.
-- Why separate? If we ever need to rotate credentials or add MFA,
-- we touch this table only — not the user record.
-- The hash is bcrypt with cost 12 — never store plaintext passwords.
CREATE TABLE user_credentials (
      user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
      password_hash BYTEA       NOT NULL,
      created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Audit Log
-- Every sensitive auth event is recorded here.
-- This table is append-only — no updates, no deletes.
-- Required by CBN for security auditing.
CREATE TABLE auth_audit_log (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID        REFERENCES users(id),
    event      VARCHAR(50) NOT NULL,  -- 'login' | 'logout' | 'register' | 'otp_requested' | 'otp_verified' | 'token_revoked'
    ip_address VARCHAR(45),
    user_agent TEXT,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_user_id   ON auth_audit_log (user_id);
CREATE INDEX idx_audit_event     ON auth_audit_log (event);
CREATE INDEX idx_audit_created   ON auth_audit_log (created_at DESC);

COMMIT;
