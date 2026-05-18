CREATE TABLE notifications (
       id              UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
       transaction_id  UUID         NOT NULL,
       user_id         UUID         NOT NULL,
       recipient       VARCHAR(255) NOT NULL,
       channel         VARCHAR(20)  NOT NULL,
       subject         VARCHAR(255),
       body            TEXT         NOT NULL,
       status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
       attempts        INT          NOT NULL DEFAULT 0,
       last_attempt    TIMESTAMPTZ,
       provider_ref    VARCHAR(255),
       error_message   TEXT,
       sent_at         TIMESTAMPTZ,
       created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
       updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
