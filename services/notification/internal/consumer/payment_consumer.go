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

	"github.com/Ad3bay0c/payflow/notification/internal/domain"
	"github.com/Ad3bay0c/payflow/notification/internal/service"
)

type PaymentConsumer struct {
	reader *kafka.Reader
	svc    service.NotificationService
	logger *zap.Logger
}

func NewPaymentConsumer(
	brokers []string,
	groupID string,
	svc service.NotificationService,
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
			CommitInterval: 0, // manual commit
			ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
				logger.Error("kafka error", zap.String("message", fmt.Sprintf(msg, args...)))
			}),
		}),
		svc:    svc,
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
				return nil
			}
			c.logger.Error("fetch error", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}

		if err := c.process(ctx, msg); err != nil {
			c.logger.Error("processing error",
				zap.String("topic", msg.Topic),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			continue
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.Error("commit error", zap.Error(err))
		}
	}
}

func (c *PaymentConsumer) process(ctx context.Context, msg kafka.Message) error {
	var event domain.PaymentEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		c.logger.Error("malformed message — skipping",
			zap.ByteString("value", msg.Value),
			zap.Error(err),
		)
		return nil // commit offset — don't loop on malformed messages
	}

	return c.svc.ProcessPaymentEvent(ctx, event)
}

func (c *PaymentConsumer) Close() error {
	return c.reader.Close()
}
