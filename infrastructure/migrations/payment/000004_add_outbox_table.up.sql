BEGIN;

-- Outbox table stores events that need to be published to Kafka.
CREATE TABLE outbox_events (
       id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
-- The Kafka topic this event should be published to
       topic        VARCHAR(100) NOT NULL,
-- The Kafka message key — used for partition routing
-- We use transaction_id so all events for one transaction
-- go to the same partition (ordering guarantee)
       message_key  VARCHAR(100) NOT NULL,
-- The serialised event payload — exactly what Kafka will receive
       payload      JSONB        NOT NULL,

       status       VARCHAR(20)  NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'published', 'failed')),
-- How many times the relay has attempted to publish this event
       attempts     INT          NOT NULL DEFAULT 0,
-- When the relay last attempted to publish
       last_attempt TIMESTAMPTZ,
-- When the event was successfully published
       published_at TIMESTAMPTZ,
       created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outbox_pending ON outbox_events (created_at ASC)
    WHERE status = 'pending';

CREATE INDEX idx_outbox_failed ON outbox_events (created_at DESC)
    WHERE status = 'failed';

COMMIT;
