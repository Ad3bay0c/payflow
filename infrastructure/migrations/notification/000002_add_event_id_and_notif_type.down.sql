BEGIN;
DROP INDEX IF EXISTS idx_notifications_event_type;
ALTER TABLE notifications
DROP COLUMN IF EXISTS event_id,
    DROP COLUMN IF EXISTS notif_type,
    DROP COLUMN IF EXISTS wallet_id_ref;
COMMIT;
