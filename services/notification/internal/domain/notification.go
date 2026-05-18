// internal/domain/notification.go

package domain

import (
	"time"

	"github.com/google/uuid"
)

// Channel is the delivery mechanism for a notification.
type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelPush  Channel = "push"
	ChannelEmail Channel = "email"
)

// NotificationStatus tracks delivery state.
type NotificationStatus string

const (
	StatusPending   NotificationStatus = "pending"
	StatusSent      NotificationStatus = "sent"
	StatusFailed    NotificationStatus = "failed"
	StatusDelivered NotificationStatus = "delivered"
)

// Notification is a record of a notification attempt.
type Notification struct {
	ID            uuid.UUID          `json:"id"`
	TransactionID uuid.UUID          `json:"transaction_id"`
	UserID        uuid.UUID          `json:"user_id"`
	Recipient     string             `json:"recipient"`
	Channel       Channel            `json:"channel"`
	Subject       *string            `json:"subject,omitempty"`
	Body          string             `json:"body"`
	Status        NotificationStatus `json:"status"`
	Attempts      int                `json:"attempts"`
	ProviderRef   *string            `json:"provider_ref,omitempty"`
	ErrorMessage  *string            `json:"error_message,omitempty"`
	SentAt        *time.Time         `json:"sent_at,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

// PaymentEvent is the Kafka event the notification service consumes.
// Must match exactly what the payment service publishes.
type PaymentEvent struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	TransactionID string    `json:"transaction_id"`
	Type          string    `json:"type"`
	Status        string    `json:"status"`
	Amount        int64     `json:"amount"`
	Fee           int64     `json:"fee"`
	Currency      string    `json:"currency"`
	SenderID      *string   `json:"sender_wallet_id,omitempty"`
	ReceiverID    *string   `json:"receiver_wallet_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// NotificationRequest is what the service layer sends to providers.
type NotificationRequest struct {
	TransactionID uuid.UUID
	UserID        uuid.UUID
	Recipient     string
	Channel       Channel
	Subject       string
	Body          string
}

// AmountInNaira converts kobo to naira for display in notifications.
func AmountInNaira(kobo int64) float64 {
	return float64(kobo) / 100
}
