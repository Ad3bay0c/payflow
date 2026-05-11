// internal/domain/payment.go

package domain

import (
	"time"

	"github.com/google/uuid"
)

// TransactionStatus represents where a transaction is in its lifecycle.
// pending → completed (success path)
// pending → failed    (error path)
// Once a transaction reaches completed or failed it is immutable.
type TransactionStatus string

const (
	StatusPending   TransactionStatus = "pending"
	StatusCompleted TransactionStatus = "completed"
	StatusFailed    TransactionStatus = "failed"
)

// TransactionType categorises what kind of money movement occurred.
type TransactionType string

const (
	TypeTransfer   TransactionType = "transfer"
	TypeFunding    TransactionType = "funding"
	TypeWithdrawal TransactionType = "withdrawal"
	TypeReversal   TransactionType = "reversal"
)

// Wallet represents a user's PayFlow wallet.
// Balance is always in kobo — the smallest unit of Nigerian Naira.
// ₦1,500.75 = 150075 kobo. Never store naira as a float.
type Wallet struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Balance   int64     `json:"balance"`  // in kobo
	Currency  string    `json:"currency"` // always "NGN" for now
	IsActive  bool      `json:"is_active"`
	Version   int64     `json:"version"` // optimistic lock version
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BalanceInNaira returns the balance as a float for display purposes only.
// Never use this for arithmetic — always work in kobo internally.
func (w *Wallet) BalanceInNaira() float64 {
	return float64(w.Balance) / 100
}

// HasSufficientBalance checks if the wallet can cover amount + fee.
func (w *Wallet) HasSufficientBalance(amount, fee int64) bool {
	return w.Balance >= amount+fee
}

// Transaction is an immutable record of a payment event.
// Once created, only the status, completed_at, and failed_at fields change.
type Transaction struct {
	ID               uuid.UUID         `json:"id"`
	IdempotencyKey   string            `json:"idempotency_key"`
	SenderWalletID   *uuid.UUID        `json:"sender_wallet_id,omitempty"`
	ReceiverWalletID *uuid.UUID        `json:"receiver_wallet_id,omitempty"`
	Amount           int64             `json:"amount"` // in kobo
	Fee              int64             `json:"fee"`    // in kobo
	Currency         string            `json:"currency"`
	Status           TransactionStatus `json:"status"`
	Type             TransactionType   `json:"type"`
	Description      *string           `json:"description,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
	CompletedAt      *time.Time        `json:"completed_at,omitempty"`
	FailedAt         *time.Time        `json:"failed_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// AmountInNaira returns the transaction amount in naira for display.
func (t *Transaction) AmountInNaira() float64 {
	return float64(t.Amount) / 100
}

// IsTerminal returns true if the transaction has reached a final state.
// Terminal transactions cannot be modified.
func (t *Transaction) IsTerminal() bool {
	return t.Status == StatusCompleted || t.Status == StatusFailed
}

// TransferRequest is the input for a wallet-to-wallet transfer.
type TransferRequest struct {
	IdempotencyKey   string
	SenderWalletID   uuid.UUID
	ReceiverWalletID uuid.UUID
	Amount           int64
	Description      string
	SenderTier       int16 // from JWT claims
}

// FundWalletRequest is the input for topping up a wallet.
type FundWalletRequest struct {
	IdempotencyKey string
	WalletID       uuid.UUID
	Amount         int64 // in kobo
	Description    string
}

// PaymentEvent is published to Kafka after every completed or failed transaction.
// Downstream services (notification, ledger, analytics) consume this.
type PaymentEvent struct {
	EventID       string            `json:"event_id"`
	EventType     string            `json:"event_type"` // "payment.completed" | "payment.failed"
	TransactionID string            `json:"transaction_id"`
	Type          TransactionType   `json:"type"`
	Status        TransactionStatus `json:"status"`
	Amount        int64             `json:"amount"`
	Fee           int64             `json:"fee"`
	Currency      string            `json:"currency"`
	SenderID      *string           `json:"sender_wallet_id,omitempty"`
	ReceiverID    *string           `json:"receiver_wallet_id,omitempty"`
	OccurredAt    time.Time         `json:"occurred_at"`
}
