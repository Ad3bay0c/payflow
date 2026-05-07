BEGIN;

-- Fee tiers define what PayFlow charges per transfer based on amount.
-- Stored in kobo. Evaluated in order — first matching tier wins.
-- max_amount_kobo of 0 means no upper bound (catch-all tier).
CREATE TABLE fee_tiers (
       id               SMALLINT    PRIMARY KEY,
       max_amount_kobo  BIGINT      NOT NULL DEFAULT 0,
       fee_kobo         BIGINT      NOT NULL,
       description      TEXT,
       is_active        BOOLEAN     NOT NULL DEFAULT true,
       updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed with current fee schedule
-- ₦0 — ₦5,000     → ₦10 fee
-- ₦5,001 — ₦50,000 → ₦25 fee
-- ₦50,001+          → ₦50 fee (capped)
INSERT INTO fee_tiers (id, max_amount_kobo, fee_kobo, description)
VALUES
   (1, 500000,  1000, '₦0 — ₦5,000'),
   (2, 5000000, 2500, '₦5,001 — ₦50,000'),
   (3, 0,       5000, '₦50,001 and above');

COMMIT;