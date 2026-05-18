BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Notification log — every notification attempt is recorded here.
CREATE TABLE notifications (
       id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
-- Links to the payment service transaction
       transaction_id  UUID        NOT NULL,
-- The user being notified
       user_id         UUID        NOT NULL,
-- Contact details at the time of notification
-- Stored here because the user might change their phone later —
-- we need the exact number/token used for this notification
       recipient       VARCHAR(255) NOT NULL,  -- phone number or FCM token
       channel         VARCHAR(20) NOT NULL
           CHECK (channel IN ('sms', 'push', 'email')),
-- The notification content
       subject         VARCHAR(255),           -- for email/push title
       body            TEXT        NOT NULL,
-- Delivery tracking
       status          VARCHAR(20) NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'sent', 'failed', 'delivered')),
       attempts        INT         NOT NULL DEFAULT 0,
       last_attempt    TIMESTAMPTZ,
-- Provider response — useful for debugging failed deliveries
       provider_ref    VARCHAR(255),           -- Termii message ID etc
       error_message   TEXT,
       sent_at         TIMESTAMPTZ,
       created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_transaction ON notifications (transaction_id);
CREATE INDEX idx_notifications_user        ON notifications (user_id, created_at DESC);
CREATE INDEX idx_notifications_status      ON notifications (status)
    WHERE status IN ('pending', 'failed');

COMMIT;
