BEGIN;

ALTER TABLE notifications
    ADD COLUMN event_id   VARCHAR(100),
    ADD COLUMN notif_type VARCHAR(50),
    -- wallet_id_ref stores the wallet ID before phone resolution
    ADD COLUMN wallet_id_ref VARCHAR(100);

-- Idempotency — prevent duplicate records for same event + type
CREATE UNIQUE INDEX idx_notifications_event_type
    ON notifications (event_id, notif_type)
    WHERE event_id IS NOT NULL;

COMMIT;
