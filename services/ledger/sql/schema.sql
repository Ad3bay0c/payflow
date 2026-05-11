CREATE TABLE accounts (
      id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
      wallet_id    UUID         UNIQUE,
      account_type VARCHAR(20)  NOT NULL,
      name         VARCHAR(100) NOT NULL UNIQUE,
      currency     CHAR(3)      NOT NULL DEFAULT 'NGN',
      is_active    BOOLEAN      NOT NULL DEFAULT true,
      created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE ledger_entries (
        id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
        transaction_id UUID        NOT NULL,
        entry_group_id UUID        NOT NULL,
        account_id     UUID        NOT NULL REFERENCES accounts(id),
        entry_type     VARCHAR(10) NOT NULL,
        amount         BIGINT      NOT NULL,
        balance_after  BIGINT      NOT NULL,
        currency       CHAR(3)     NOT NULL DEFAULT 'NGN',
        description    TEXT,
        created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
