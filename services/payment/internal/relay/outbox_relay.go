// internal/relay/outbox_relay.go
//
// The outbox relay reads pending events from the outbox table
// and publishes them to Kafka. It runs as a background goroutine
// inside the payment service.
//
// Design:
// - Polls the outbox every second for pending events
// - Publishes each event to Kafka
// - Marks as published on success, increments attempt count on failure
// - After 5 failed attempts, marks as failed (requires manual intervention)
// - SKIP LOCKED means multiple instances can run safely (no duplicate publishing)

package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/payment/internal/repository"
	"github.com/Ad3bay0c/payflow/pkg/pgconv"
)

const (
	batchSize    = 100         // events per poll cycle
	pollInterval = time.Second // how often to check for pending events
	maxAttempts  = 5           // after this many failures, mark as failed
)

type OutboxRelay struct {
	repo   repository.PaymentRepository
	writer *kafka.Writer
	logger *zap.Logger
}

func NewOutboxRelay(
	repo repository.PaymentRepository,
	brokers []string,
	logger *zap.Logger,
) *OutboxRelay {
	return &OutboxRelay{
		repo: repo,
		writer: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Balancer:               &kafka.LeastBytes{},
			RequiredAcks:           kafka.RequireOne,
			Async:                  false,
			WriteTimeout:           10 * time.Second,
			AllowAutoTopicCreation: true,
		},
		logger: logger,
	}
}

// Start begins polling the outbox table for pending events.
// Blocks until ctx is cancelled.
func (r *OutboxRelay) Start(ctx context.Context) {
	r.logger.Info("outbox relay started",
		zap.Duration("poll_interval", pollInterval),
		zap.Int("batch_size", batchSize),
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopping")
			return
		case <-ticker.C:
			r.processBatch(ctx)
		}
	}
}

// processBatch fetches and publishes one batch of pending events.
func (r *OutboxRelay) processBatch(ctx context.Context) {
	events, err := r.repo.GetPendingOutboxEvents(ctx, batchSize)
	if err != nil {
		r.logger.Error("failed to fetch outbox events", zap.Error(err))
		return
	}

	if len(events) == 0 {
		return // nothing to do
	}

	r.logger.Debug("processing outbox batch", zap.Int("count", len(events)))

	for _, event := range events {
		eventID := pgconv.FromPgUUID(event.ID)
		r.publishEvent(ctx, eventID, event.Topic, event.MessageKey, event.Payload)
	}
}

// publishEvent publishes a single outbox event to Kafka.
func (r *OutboxRelay) publishEvent(
	ctx context.Context,
	eventID uuid.UUID,
	topic string,
	messageKey string,
	payload []byte,
) {
	// Verify the payload is valid JSON before sending
	if !json.Valid(payload) {
		r.logger.Error("invalid JSON payload in outbox — marking failed",
			zap.String("event_id", eventID.String()),
		)
		_ = r.repo.MarkOutboxEventFailed(ctx, eventID) //nolint:errcheck
		return
	}

	err := r.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(messageKey),
		Value: payload,
	})

	if err != nil {
		r.logger.Error("failed to publish outbox event",
			zap.String("event_id", eventID.String()),
			zap.String("topic", topic),
			zap.Error(err),
		)
		// Increment attempt count — after maxAttempts the relay marks it failed
		if incrementErr := r.repo.IncrementOutboxAttempt(ctx, eventID); incrementErr != nil {
			r.logger.Error("failed to increment attempt count",
				zap.String("event_id", eventID.String()),
				zap.Error(incrementErr),
			)
		}
		return
	}

	// Successfully published — mark as done
	if err := r.repo.MarkOutboxEventPublished(ctx, eventID); err != nil {
		// The event was published to Kafka but we couldn't mark it done.
		// On the next poll, SKIP LOCKED won't skip it (it's not locked anymore)
		// but the 30-second cooldown on last_attempt prevents immediate retry.
		// When retried, Kafka receives a duplicate — the ledger service
		// handles duplicates via idempotency. Safe.
		r.logger.Error("failed to mark outbox event as published",
			zap.String("event_id", eventID.String()),
			zap.Error(err),
		)
	}

	r.logger.Debug("outbox event published",
		zap.String("event_id", eventID.String()),
		zap.String("topic", topic),
	)
}

// Close shuts down the Kafka writer.
func (r *OutboxRelay) Close() error {
	return r.writer.Close()
}
