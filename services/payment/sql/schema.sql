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

CREATE TABLE transactions (
      id                 UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
      idempotency_key    VARCHAR(64) NOT NULL,
      sender_wallet_id   UUID        REFERENCES wallets(id),
      receiver_wallet_id UUID        REFERENCES wallets(id),
      amount             BIGINT      NOT NULL CHECK (amount > 0),
      fee                BIGINT      NOT NULL DEFAULT 0 CHECK (fee >= 0),
      currency           CHAR(3)     NOT NULL DEFAULT 'NGN',
      status             VARCHAR(20) NOT NULL DEFAULT 'pending'
          CHECK (status IN ('pending', 'completed', 'failed')),
      type               VARCHAR(20) NOT NULL
          CHECK (type IN ('transfer', 'funding', 'withdrawal', 'reversal')),
      description        TEXT,
      metadata           JSONB,
      completed_at       TIMESTAMPTZ,
      failed_at          TIMESTAMPTZ,
      created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);