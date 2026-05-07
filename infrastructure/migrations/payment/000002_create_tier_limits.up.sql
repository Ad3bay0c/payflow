BEGIN;

-- Tier limits define the maximum single transaction amount and daily
-- cumulative limit per KYC tier. Stored in kobo.
-- Using a table instead of hardcoded values means compliance or product
-- can update limits without a code deployment.
CREATE TABLE tier_limits (
     tier               SMALLINT    PRIMARY KEY CHECK (tier IN (1, 2, 3)),
     max_transfer_kobo  BIGINT      NOT NULL,  -- max single transfer
     daily_limit_kobo   BIGINT      NOT NULL,  -- max total transfers per day
     description        TEXT,
     updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed with CBN-aligned defaults
-- Tier 1 (basic KYC — phone verified):   ₦50,000 single / ₦100,000 daily
-- Tier 2 (BVN verified):                 ₦500,000 single / ₦1,000,000 daily
-- Tier 3 (full KYC):                     ₦5,000,000 single / ₦10,000,000 daily
INSERT INTO tier_limits (tier, max_transfer_kobo, daily_limit_kobo, description)
VALUES(1, 5000000,   10000000,  'Basic KYC — phone verified'),
     (2, 50000000,  100000000, 'BVN verified'),
     (3, 500000000, 1000000000,'Full KYC — document verified');

-- Daily transfer summary — tracks cumulative daily spend per wallet
-- Used to enforce the daily limit without scanning all transactions
CREATE TABLE daily_transfer_summary (
        wallet_id   UUID        NOT NULL REFERENCES wallets(id),
        date        DATE        NOT NULL DEFAULT CURRENT_DATE,
        total_kobo  BIGINT      NOT NULL DEFAULT 0,
        updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        PRIMARY KEY (wallet_id, date)
);

CREATE INDEX idx_daily_summary_wallet_date
    ON daily_transfer_summary (wallet_id, date);

COMMIT;
