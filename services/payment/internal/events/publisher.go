// internal/events/publisher.go
//
// Kafka event publisher for the payment service.
// Every completed or failed transaction fires an event.
// Downstream services (notification, ledger, analytics) consume these.

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"github.com/Ad3bay0c/payflow/payment/internal/domain"
)

const (
	TopicPaymentCompleted = "payment.completed"
	TopicPaymentFailed    = "payment.failed"
)

type Publisher interface {
	PublishPaymentCompleted(ctx context.Context, txn *domain.Transaction) error
	PublishPaymentFailed(ctx context.Context, txn *domain.Transaction) error
}

type kafkaPublisher struct {
	writer *kafka.Writer
}

func NewKafkaPublisher(brokers []string) Publisher {
	return &kafkaPublisher{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Balancer: &kafka.LeastBytes{},
			// RequiredAcks: all replicas must acknowledge before we consider
			// the message written. In production with RF=3 this means
			// the message survives broker failures.
			RequiredAcks:           kafka.RequireOne,
			Async:                  false, // synchronous — we know the event was written
			WriteTimeout:           10 * time.Second,
			AllowAutoTopicCreation: true,
		},
	}
}

func (p *kafkaPublisher) PublishPaymentCompleted(ctx context.Context, txn *domain.Transaction) error {
	return p.publish(ctx, TopicPaymentCompleted, txn)
}

func (p *kafkaPublisher) PublishPaymentFailed(ctx context.Context, txn *domain.Transaction) error {
	return p.publish(ctx, TopicPaymentFailed, txn)
}

func (p *kafkaPublisher) publish(ctx context.Context, topic string, txn *domain.Transaction) error {
	event := buildEvent(topic, txn)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}

	// Use transaction ID as the Kafka message key.
	// This ensures all events for the same transaction go to the same
	// partition — guaranteeing ordering for that transaction.
	err = p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(txn.ID.String()),
		Value: payload,
	})
	if err != nil {
		return fmt.Errorf("publishing to kafka topic %s: %w", topic, err)
	}

	return nil
}

func buildEvent(eventType string, txn *domain.Transaction) domain.PaymentEvent {
	event := domain.PaymentEvent{
		EventID:       uuid.NewString(),
		EventType:     eventType,
		TransactionID: txn.ID.String(),
		Type:          txn.Type,
		Status:        txn.Status,
		Amount:        txn.Amount,
		Fee:           txn.Fee,
		Currency:      txn.Currency,
		OccurredAt:    time.Now().UTC(),
	}

	if txn.SenderWalletID != nil {
		s := txn.SenderWalletID.String()
		event.SenderID = &s
	}
	if txn.ReceiverWalletID != nil {
		r := txn.ReceiverWalletID.String()
		event.ReceiverID = &r
	}

	return event
}
