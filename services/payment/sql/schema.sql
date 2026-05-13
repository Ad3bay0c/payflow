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

CREATE TABLE tier_limits (
         tier               SMALLINT    PRIMARY KEY CHECK (tier IN (1, 2, 3)),
         max_transfer_kobo  BIGINT      NOT NULL,  -- max single transfer
         daily_limit_kobo   BIGINT      NOT NULL,  -- max total transfers per day
         description        TEXT,
         updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE daily_transfer_summary (
        wallet_id   UUID        NOT NULL REFERENCES wallets(id),
        date        DATE        NOT NULL DEFAULT CURRENT_DATE,
        total_kobo  BIGINT      NOT NULL DEFAULT 0,
        updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        PRIMARY KEY (wallet_id, date)
);

CREATE TABLE fee_tiers (
       id               SMALLINT    PRIMARY KEY,
       max_amount_kobo  BIGINT      NOT NULL DEFAULT 0,
       fee_kobo         BIGINT      NOT NULL,
       description      TEXT,
       is_active        BOOLEAN     NOT NULL DEFAULT true,
       updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE outbox_events (
       id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
       topic        VARCHAR(100) NOT NULL,
       message_key  VARCHAR(100) NOT NULL,
       payload      JSONB        NOT NULL,
       status       VARCHAR(20)  NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'published', 'failed')),
       attempts     INT          NOT NULL DEFAULT 0,
       last_attempt TIMESTAMPTZ,
       published_at TIMESTAMPTZ,
       created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
