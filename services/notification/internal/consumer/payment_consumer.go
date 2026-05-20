// internal/consumer/payment_consumer.go
//
// Kafka consumer for the notification service:
//   1. Consume the Kafka message
//   2. Write a pending notification record to the database
//   3. Commit the Kafka offset

package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/notification/internal/domain"
	"github.com/Ad3bay0c/payflow/notification/internal/repository"
)

type PaymentConsumer struct {
	reader *kafka.Reader
	repo   repository.NotificationRepository
	logger *zap.Logger
}

func NewPaymentConsumer(
	brokers []string,
	groupID string,
	repo repository.NotificationRepository,
	logger *zap.Logger,
) *PaymentConsumer {
	return &PaymentConsumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:        brokers,
			GroupID:        groupID,
			GroupTopics:    []string{"payment.completed"},
			StartOffset:    kafka.FirstOffset,
			MinBytes:       1,
			MaxBytes:       10e6,
			MaxWait:        500 * time.Millisecond,
			CommitInterval: 0, // manual commit only
			ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
				logger.Error("kafka reader error",
					zap.String("message", fmt.Sprintf(msg, args...)),
				)
			}),
		}),
		repo:   repo,
		logger: logger,
	}
}

func (c *PaymentConsumer) Start(ctx context.Context) error {
	c.logger.Info("notification consumer started",
		zap.Strings("topics", []string{"payment.completed"}),
	)

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.logger.Info("notification consumer stopping")
				return nil
			}
			c.logger.Error("failed to fetch message", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}

		// Write pending record then commit offset.
		// If writing fails we do NOT commit — Kafka redelivers the message.
		// If writing succeeds but commit fails — message redelivered,
		// duplicate write attempt is handled by idempotency in the repo.
		if err := c.persist(ctx, msg); err != nil {
			c.logger.Error("failed to persist notification — not committing offset",
				zap.String("topic", msg.Topic),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)

			time.Sleep(time.Second) // wait before retry
			continue
		}

		// Only commit after successful database write
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.Error("failed to commit offset — message will be redelivered",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}

		c.logger.Debug("message persisted and offset committed",
			zap.String("topic", msg.Topic),
			zap.Int64("offset", msg.Offset),
		)
	}
}

// persist deserialises the Kafka message and writes pending
// notification records to the database.
// This is idempotent — duplicate messages produce no duplicate records
// because we check for existing records by event_id.
func (c *PaymentConsumer) persist(ctx context.Context, msg kafka.Message) error {
	var event domain.PaymentEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// Malformed message — log and commit offset to avoid looping forever
		c.logger.Error("malformed kafka message — skipping",
			zap.ByteString("value", msg.Value),
			zap.Error(err),
		)
		// Return nil so caller commits this offset
		return nil
	}

	c.logger.Info("persisting notification records for payment event",
		zap.String("event_type", event.EventType),
		zap.String("transaction_id", event.TransactionID),
		zap.String("type", event.Type),
	)

	txnID, err := uuid.Parse(event.TransactionID)
	if err != nil {
		c.logger.Error("invalid transaction id in event — skipping",
			zap.String("transaction_id", event.TransactionID),
		)
		return nil
	}

	// Determine which wallet IDs need notifications
	// We store one pending record per recipient
	// The processor will look up the actual phone number when delivering
	var walletIDs []pendingNotif

	switch event.Type {
	case "transfer":
		if event.SenderID != nil {
			walletIDs = append(walletIDs, pendingNotif{
				walletID:      *event.SenderID,
				notifType:     "transfer_debit",
				amountKobo:    event.Amount,
				feeKobo:       event.Fee,
				displayAmount: event.Amount + event.Fee,
			})
		}
		if event.ReceiverID != nil {
			walletIDs = append(walletIDs, pendingNotif{
				walletID:      *event.ReceiverID,
				notifType:     "transfer_credit",
				amountKobo:    event.Amount,
				displayAmount: event.Amount,
			})
		}
	case "funding":
		if event.ReceiverID != nil {
			walletIDs = append(walletIDs, pendingNotif{
				walletID:      *event.ReceiverID,
				notifType:     "wallet_funded",
				amountKobo:    event.Amount,
				displayAmount: event.Amount,
			})
		}
	case "withdrawal":
		if event.SenderID != nil {
			walletIDs = append(walletIDs, pendingNotif{
				walletID:      *event.SenderID,
				notifType:     "withdrawal_debit",
				amountKobo:    event.Amount,
				displayAmount: event.Amount,
			})
		}
	default:
		c.logger.Debug("no notification configured for event type",
			zap.String("type", event.Type),
		)
		return nil
	}

	// Write one pending record per recipient
	for _, n := range walletIDs {
		req := domain.NotificationRequest{
			TransactionID: txnID,
			UserID:        uuid.Nil,   // this will be resolved by the processor
			Recipient:     n.walletID, // wallet_id as placeholder
			Channel:       domain.ChannelSMS,
			Body:          buildMessageBody(n.notifType, n.amountKobo, n.feeKobo, txnID.String()[:8]),
			EventID:       event.EventID, // for idempotency
			NotifType:     n.notifType,
		}

		if err := c.repo.CreatePendingNotification(ctx, req); err != nil {
			return fmt.Errorf("creating pending notification: %w", err)
		}
	}

	return nil
}

type pendingNotif struct {
	walletID      string
	notifType     string
	amountKobo    int64
	displayAmount int64
	feeKobo       int64
}

func buildMessageBody(notifType string, amountKobo, feeKobo int64, txnRef string) string {
	naira := float64(amountKobo) / 100
	switch notifType {
	case "transfer_debit":
		fee := float64(feeKobo) / 100
		total := float64(amountKobo+feeKobo) / 100
		return fmt.Sprintf(
			"PayFlow: Your account has been debited ₦%.2f. \nAmount: ₦%.2f. \nFee: ₦%.2f. \nTxn ref: %s",
			total, naira, fee, txnRef,
		)
	case "transfer_credit":
		return fmt.Sprintf(
			"PayFlow: You have received ₦%.2f. Txn ref: %s",
			naira, txnRef,
		)
	case "wallet_funded":
		return fmt.Sprintf(
			"PayFlow: Your wallet has been funded with ₦%.2f. Txn ref: %s",
			naira, txnRef,
		)
	default:
		return fmt.Sprintf("PayFlow: Transaction ₦%.2f. Ref: %s", naira, txnRef)
	}
}

func (c *PaymentConsumer) Close() error {
	return c.reader.Close()
}
