// internal/domain/ledger.go

package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EntryType represents the direction of money movement.
type EntryType string

const (
	EntryDebit  EntryType = "debit"
	EntryCredit EntryType = "credit"
)

// AccountType distinguishes user accounts from system accounts.
type AccountType string

const (
	AccountTypeUser   AccountType = "user"
	AccountTypeSystem AccountType = "system"
)

// System account names — these always exist in the ledger.
const (
	AccountPayFlowRevenue  = "PAYFLOW_REVENUE"
	AccountPayFlowSuspense = "PAYFLOW_SUSPENSE"
	AccountPayFlowExternal = "PAYFLOW_EXTERNAL"
)

// Account represents any entity that can hold money in the ledger.
// User wallets and system accounts both have an Account record.
type Account struct {
	ID          uuid.UUID   `json:"id"`
	WalletID    *uuid.UUID  `json:"wallet_id,omitempty"` // nil for system accounts
	AccountType AccountType `json:"account_type"`
	Name        string      `json:"name"`
	Currency    string      `json:"currency"`
	IsActive    bool        `json:"is_active"`
	CreatedAt   time.Time   `json:"created_at"`
}

// LedgerEntry is a single immutable record of money movement.
// Once written it is never modified or deleted — CBN regulatory requirement.
type LedgerEntry struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"` // links to payment service
	EntryGroupID  uuid.UUID `json:"entry_group_id"` // groups related entries
	AccountID     uuid.UUID `json:"account_id"`
	EntryType     EntryType `json:"entry_type"`
	Amount        int64     `json:"amount"`        // in kobo
	BalanceAfter  int64     `json:"balance_after"` // account balance after this entry
	Currency      string    `json:"currency"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}

// EntryGroup is a set of related ledger entries that together form
// one complete financial transaction. All entries in a group are
// written atomically — all succeed or all fail.
type EntryGroup struct {
	ID            uuid.UUID     `json:"id"`
	TransactionID uuid.UUID     `json:"transaction_id"`
	Entries       []LedgerEntry `json:"entries"`
}

// Validate checks that the entry group is balanced.
// Sum of all debits must equal sum of all credits.
// This is the mathematical invariant that proves no money was created or lost.
func (g *EntryGroup) Validate() error {
	var totalDebits, totalCredits int64

	for _, entry := range g.Entries {
		switch entry.EntryType {
		case EntryDebit:
			totalDebits += entry.Amount
		case EntryCredit:
			totalCredits += entry.Amount
		}
	}

	if totalDebits != totalCredits {
		return &ImbalancedEntryGroupError{
			GroupID:      g.ID,
			TotalDebits:  totalDebits,
			TotalCredits: totalCredits,
		}
	}

	return nil
}

// ImbalancedEntryGroupError is returned when debits don't equal credits.
// This should never happen in production — it indicates a code bug.
type ImbalancedEntryGroupError struct {
	GroupID      uuid.UUID
	TotalDebits  int64
	TotalCredits int64
}

func (e *ImbalancedEntryGroupError) Error() string {
	return fmt.Sprintf(
		"imbalanced entry group %s: debits=%d credits=%d difference=%d",
		e.GroupID,
		e.TotalDebits,
		e.TotalCredits,
		e.TotalDebits-e.TotalCredits,
	)
}

// PaymentEvent is the Kafka event the ledger consumes from the payment service.
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
