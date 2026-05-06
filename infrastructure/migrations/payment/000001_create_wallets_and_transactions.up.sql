BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Wallets
-- Each user has one wallet. Balance stored in kobo (smallest NGN unit).
-- ₦1,500.75 is stored as 150075 kobo. Display conversion happens in the UI.
--
-- version column enables optimistic locking:
-- UPDATE wallets SET balance = $1, version = version + 1
-- WHERE id = $2 AND version = $3
-- 0 rows updated = concurrent modification detected → retry
CREATE TABLE wallets (
     id         UUID    PRIMARY KEY DEFAULT uuid_generate_v4(),
     user_id    UUID    NOT NULL UNIQUE,
     balance    BIGINT  NOT NULL DEFAULT 0 CHECK (balance >= 0),
     currency   CHAR(3) NOT NULL DEFAULT 'NGN',
     is_active  BOOLEAN NOT NULL DEFAULT true,
     version    BIGINT  NOT NULL DEFAULT 0,
     created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
     updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallets_user_id ON wallets (user_id);

-- Transactions
-- Immutable record of every payment attempt.
-- Records are never updated after terminal status (completed/failed).
--
-- idempotency_key: client-generated unique key per payment request.
-- If the same key is seen twice, return the original result — never process twice.
-- This is what prevents double charges on network retries.
-- amount and fee stored in kobo — same reason as wallet balance.
CREATE TABLE transactions (
      id                    UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
      idempotency_key       VARCHAR(64) NOT NULL,
      sender_wallet_id      UUID                 REFERENCES wallets(id),
      receiver_wallet_id    UUID                 REFERENCES wallets(id),
      amount                BIGINT      NOT NULL CHECK (amount > 0),
      fee                   BIGINT      NOT NULL DEFAULT 0 CHECK (fee >= 0),
      currency              CHAR(3)     NOT NULL DEFAULT 'NGN',

    -- pending → completed (success)
    -- pending → failed    (fraud blocked, insufficient funds, external error)
      status                VARCHAR(20) NOT NULL DEFAULT 'pending'
          CHECK (status IN ('pending', 'completed', 'failed')),
      type                  VARCHAR(20) NOT NULL
          CHECK (type IN ('transfer', 'funding', 'withdrawal', 'reversal')),
      description           TEXT,
      metadata              JSONB,
      completed_at          TIMESTAMPTZ,
      failed_at             TIMESTAMPTZ,
      created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Enforce business rules at the database level:
    -- transfer  → both wallets must be present
    -- funding   → only receiver wallet (money coming in)
    -- withdrawal → only sender wallet (money going out)
    -- reversal  → both wallets must be present
      CONSTRAINT chk_wallet_presence CHECK (
          (type = 'transfer'   AND sender_wallet_id IS NOT NULL AND receiver_wallet_id IS NOT NULL) OR
          (type = 'funding'    AND sender_wallet_id IS NULL     AND receiver_wallet_id IS NOT NULL) OR
          (type = 'withdrawal' AND sender_wallet_id IS NOT NULL AND receiver_wallet_id IS NULL    ) OR
          (type = 'reversal'   AND sender_wallet_id IS NOT NULL AND receiver_wallet_id IS NOT NULL)
      )
);

-- Indexes for the query patterns we know we'll need
CREATE UNIQUE INDEX idx_transactions_idempotency ON transactions (idempotency_key);
CREATE INDEX idx_transactions_sender   ON transactions (sender_wallet_id, created_at DESC)
    WHERE sender_wallet_id IS NOT NULL;
CREATE INDEX idx_transactions_receiver ON transactions (receiver_wallet_id, created_at DESC)
    WHERE receiver_wallet_id IS NOT NULL;
CREATE INDEX idx_transactions_status   ON transactions (status) WHERE status = 'pending';
CREATE INDEX idx_transactions_created  ON transactions (created_at DESC);

COMMIT;