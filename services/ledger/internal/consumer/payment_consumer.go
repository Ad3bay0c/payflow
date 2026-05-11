// internal/consumer/payment_consumer.go

package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/ledger/internal/domain"
	"github.com/Ad3bay0c/payflow/ledger/internal/service"
)

// PaymentConsumer consumes payment events from Kafka and
// passes them to the ledger service for processing.
type PaymentConsumer struct {
	reader *kafka.Reader
	ledger service.LedgerService
	logger *zap.Logger
}

func NewPaymentConsumer(
	brokers []string,
	groupID string,
	ledger service.LedgerService,
	logger *zap.Logger,
) *PaymentConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		// Consumer group — Kafka tracks which messages this group has processed.
		// If we run multiple ledger service instances, Kafka distributes
		// partitions between them automatically.
		GroupID: groupID,
		// Subscribe to both topics — completed payments and failed ones.
		// Failed payments may still need ledger entries (e.g. reversal records).
		GroupTopics: []string{
			"payment.completed",
			"payment.failed",
		},
		// Start from the earliest unprocessed message on first run.
		// After that, Kafka tracks the committed offset per group.
		StartOffset: kafka.FirstOffset,
		// MinBytes/MaxBytes control fetch batching.
		// MinBytes: wait until at least 1 byte is available (low latency).
		// MaxBytes: fetch up to 10MB per batch.
		MinBytes: 1,
		MaxBytes: 10e6, // 10MB
		// How long to wait for MinBytes before returning anyway.
		MaxWait: 500 * time.Millisecond,
		// Commit offsets manually — we control exactly when an offset is committed.
		// Auto-commit would commit before we've written to the database — unsafe.
		CommitInterval: 0,                                                          // disable auto-commit
		Logger:         kafka.LoggerFunc(func(msg string, args ...interface{}) {}), // silence kafka-go internal logs
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
			logger.Error("kafka reader error", zap.String("message", fmt.Sprintf(msg, args...)))
		}),
	})

	return &PaymentConsumer{
		reader: reader,
		ledger: ledger,
		logger: logger,
	}
}

// Start begins consuming messages. Blocks until ctx is cancelled.
func (c *PaymentConsumer) Start(ctx context.Context) error {
	c.logger.Info("ledger consumer started",
		zap.Strings("topics", []string{"payment.completed", "payment.failed"}),
	)

	for {
		// FetchMessage fetches the next message without committing the offset.
		// The offset is only committed after we successfully write to the database.
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.logger.Info("ledger consumer stopping — context cancelled")
				return nil
			}
			c.logger.Error("failed to fetch message",
				zap.Error(err),
			)
			// brief wait
			time.Sleep(time.Second)
			continue
		}

		c.logger.Debug("message received",
			zap.String("topic", msg.Topic),
			zap.Int64("offset", msg.Offset),
			zap.Int("partition", msg.Partition),
		)

		// Process the message
		if err := c.processMessage(ctx, msg); err != nil {
			c.logger.Error("failed to process message",
				zap.String("topic", msg.Topic),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			// We do NOT commit the offset on failure.
			// Kafka will redeliver this message on the next fetch.
			// The service layer handles duplicate messages via idempotency.
			//
			// In production you'd implement a dead-letter queue here:
			// after N retries, move the message to a DLQ for manual inspection
			// rather than blocking the consumer indefinitely.
			continue
		}

		// Commit the offset ONLY after successful database write.
		// This is the guarantee: if we crash between write and commit,
		// Kafka redelivers the message and the service layer deduplicates it.
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.Error("failed to commit offset",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			// Non-fatal — the message was processed successfully.
			// On restart Kafka will redeliver and idempotency will handle it.
		}

		c.logger.Info("message processed and offset committed",
			zap.String("topic", msg.Topic),
			zap.Int64("offset", msg.Offset),
		)
	}
}

// processMessage deserializes the Kafka message and calls the ledger service.
func (c *PaymentConsumer) processMessage(ctx context.Context, msg kafka.Message) error {
	var event domain.PaymentEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// Malformed message — log and skip.
		// We commit this offset so we don't loop forever on an unparseable message.
		c.logger.Error("malformed message — skipping",
			zap.String("topic", msg.Topic),
			zap.Int64("offset", msg.Offset),
			zap.ByteString("value", msg.Value),
			zap.Error(err),
		)
		// Return nil so the caller commits the offset and moves on
		return nil
	}

	c.logger.Info("processing payment event",
		zap.String("event_type", event.EventType),
		zap.String("transaction_id", event.TransactionID),
		zap.String("type", event.Type),
		zap.Int64("amount_kobo", event.Amount),
	)

	return c.ledger.ProcessPaymentEvent(ctx, event)
}

// Close shuts down the Kafka reader gracefully.
func (c *PaymentConsumer) Close() error {
	return c.reader.Close()
}
