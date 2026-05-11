BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Every entity that can hold money has an account in the ledger.
-- User wallets map 1:1 to accounts via wallet_id.
-- System accounts (revenue, suspense, external) have no wallet_id.
CREATE TABLE accounts (
      id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
-- wallet_id links to the payment service wallet
-- NULL for system accounts
      wallet_id    UUID        UNIQUE,
      account_type VARCHAR(20) NOT NULL
          CHECK (account_type IN ('user', 'system')),
      name         VARCHAR(100) NOT NULL UNIQUE, -- System account names: PAYFLOW_REVENUE, PAYFLOW_SUSPENSE, PAYFLOW_EXTERNAL
      currency     CHAR(3)     NOT NULL DEFAULT 'NGN',
      is_active    BOOLEAN     NOT NULL DEFAULT true,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO accounts (id, account_type, name, currency)
    VALUES
        (uuid_generate_v4(), 'system', 'PAYFLOW_REVENUE',  'NGN'),
        (uuid_generate_v4(), 'system', 'PAYFLOW_SUSPENSE', 'NGN'),
        (uuid_generate_v4(), 'system', 'PAYFLOW_EXTERNAL', 'NGN');

-- The core of the financial system. Immutable — no UPDATE, no DELETE.
-- Every entry records money moving into or out of an account.
CREATE TABLE ledger_entries (
        id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
        transaction_id   UUID        NOT NULL, -- Links to payment service — the source of truth for payment state
        entry_group_id   UUID        NOT NULL, -- Groups all entries for one payment (debit + credit + fee entries)
        account_id       UUID        NOT NULL REFERENCES accounts(id),
        entry_type       VARCHAR(10) NOT NULL CHECK (entry_type IN ('debit', 'credit')),
        amount           BIGINT      NOT NULL CHECK (amount > 0),
-- Balance of this account after this entry was posted
-- Allows instant point-in-time balance lookup
        balance_after    BIGINT      NOT NULL,
        currency         CHAR(3)     NOT NULL DEFAULT 'NGN',
        description      TEXT,
-- Immutable timestamp — never updated
        created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

-- Prevent duplicate entries for the same transaction on the same account
        CONSTRAINT uq_transaction_account UNIQUE (transaction_id, account_id, entry_type)
);

CREATE INDEX idx_entries_transaction  ON ledger_entries (transaction_id);
CREATE INDEX idx_entries_account      ON ledger_entries (account_id, created_at DESC);
CREATE INDEX idx_entries_group        ON ledger_entries (entry_group_id);
CREATE INDEX idx_entries_created      ON ledger_entries (created_at DESC);

-- ── Immutability enforcement
-- Revoke UPDATE and DELETE at the database level.
REVOKE UPDATE, DELETE ON ledger_entries FROM payflow_ledger;

COMMIT;
