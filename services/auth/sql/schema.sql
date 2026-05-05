CREATE TABLE users (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    phone_number VARCHAR(15) NOT NULL,
    email        VARCHAR(255),
    full_name    VARCHAR(255) NOT NULL,
    kyc_status   VARCHAR(20) NOT NULL DEFAULT 'pending'
                 CHECK (kyc_status IN ('pending', 'basic', 'verified', 'full')),
    tier         SMALLINT    NOT NULL DEFAULT 1
                 CHECK (tier IN (1, 2, 3)),
    is_active    BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

CREATE TABLE user_credentials (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash BYTEA       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE auth_audit_log (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID        REFERENCES users(id),
    event      VARCHAR(50) NOT NULL,
    ip_address VARCHAR(45),
    user_agent TEXT,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
