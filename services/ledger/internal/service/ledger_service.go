// internal/service/ledger_service.go
//
// Core ledger business logic.
// Consumes payment events and writes immutable double-entry bookkeeping records.
// Every payment produces a balanced set of ledger entries —
// total debits always equal total credits.

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/ledger/internal/domain"
	"github.com/Ad3bay0c/payflow/ledger/internal/repository"
)

type LedgerService interface {
	ProcessPaymentEvent(ctx context.Context, event domain.PaymentEvent) error
	GetEntriesByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.LedgerEntry, error)
	GetCurrentBalance(ctx context.Context, walletID uuid.UUID) (int64, error)
}

type ledgerService struct {
	repo   repository.LedgerRepository
	logger *zap.Logger
}

func NewLedgerService(repo repository.LedgerRepository, logger *zap.Logger) LedgerService {
	return &ledgerService{
		repo:   repo,
		logger: logger,
	}
}

// ProcessPaymentEvent is the main entry point — called by the Kafka consumer
// for every payment.completed event.
// It is idempotent — safe to call multiple times with the same event.
func (s *ledgerService) ProcessPaymentEvent(ctx context.Context, event domain.PaymentEvent) error {
	transactionID, err := uuid.Parse(event.TransactionID)
	if err != nil {
		return fmt.Errorf("invalid transaction id: %w", err)
	}

	// Route to the correct entry builder based on transaction type
	switch event.Type {
	case "transfer":
		return s.processTransfer(ctx, transactionID, event)
	case "funding":
		return s.processFunding(ctx, transactionID, event)
	case "withdrawal":
		return s.processWithdrawal(ctx, transactionID, event)
	default:
		s.logger.Warn("unknown transaction type — skipping",
			zap.String("type", event.Type),
			zap.String("transaction_id", event.TransactionID),
		)
		return nil
	}
}

// processTransfer creates 3 ledger entries for a wallet-to-wallet transfer:
//
//	Entry 1: DEBIT  sender account    amount + fee
//	Entry 2: CREDIT receiver account  amount
//	Entry 3: CREDIT revenue account   fee
//
// Total debits must equal Total credits
func (s *ledgerService) processTransfer(ctx context.Context, transactionID uuid.UUID, event domain.PaymentEvent) error {
	if event.SenderID == nil || event.ReceiverID == nil {
		return fmt.Errorf("transfer event missing sender or receiver wallet id")
	}

	senderWalletID, err := uuid.Parse(*event.SenderID)
	if err != nil {
		return fmt.Errorf("invalid sender wallet id: %w", err)
	}

	receiverWalletID, err := uuid.Parse(*event.ReceiverID)
	if err != nil {
		return fmt.Errorf("invalid receiver wallet id: %w", err)
	}

	senderAccount, err := s.repo.GetAccountByWalletID(ctx, senderWalletID)
	if err != nil {
		return fmt.Errorf("getting sender account: %w", err)
	}

	// Idempotency check — have we already processed this transaction?
	// Check the sender's debit entry — if it exists, the whole group was written
	if senderAccount != nil {
		exists, err := s.repo.EntryExists(ctx, transactionID, senderAccount.ID, domain.EntryDebit)
		if err != nil {
			return fmt.Errorf("checking idempotency: %w", err)
		}
		if exists {
			s.logger.Info("transfer already processed — skipping",
				zap.String("transaction_id", transactionID.String()),
			)
			return nil
		}
	}

	// Get the PayFlow revenue system account
	revenueAccount, err := s.repo.GetAccountByName(ctx, domain.AccountPayFlowRevenue)
	if err != nil || revenueAccount == nil {
		return fmt.Errorf("getting revenue account: %w", err)
	}

	// Begin database transaction — Atomicity
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Get or create ledger accounts for both wallets
	sender, err := s.repo.GetOrCreateUserAccount(ctx, tx, senderWalletID)
	if err != nil {
		return fmt.Errorf("getting sender ledger account: %w", err)
	}

	receiver, err := s.repo.GetOrCreateUserAccount(ctx, tx, receiverWalletID)
	if err != nil {
		return fmt.Errorf("getting receiver ledger account: %w", err)
	}

	// Calculate current balances from latest entries
	senderBalance := s.getCurrentBalanceFromRepo(ctx, sender.ID)
	receiverBalance := s.getCurrentBalanceFromRepo(ctx, receiver.ID)
	revenueBalance := s.getCurrentBalanceFromRepo(ctx, revenueAccount.ID)

	totalDebit := event.Amount + event.Fee

	// Build the entry group
	groupID := uuid.New()
	now := time.Now().UTC()

	group := &domain.EntryGroup{
		ID:            groupID,
		TransactionID: transactionID,
		Entries: []domain.LedgerEntry{
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     sender.ID,
				EntryType:     domain.EntryDebit,
				Amount:        totalDebit,
				BalanceAfter:  senderBalance - totalDebit,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Transfer sent — txn %s", transactionID),
				CreatedAt:     now,
			},
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     receiver.ID,
				EntryType:     domain.EntryCredit,
				Amount:        event.Amount,
				BalanceAfter:  receiverBalance + event.Amount,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Transfer received — txn %s", transactionID),
				CreatedAt:     now,
			},
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     revenueAccount.ID,
				EntryType:     domain.EntryCredit,
				Amount:        event.Fee,
				BalanceAfter:  revenueBalance + event.Fee,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Transfer fee — txn %s", transactionID),
				CreatedAt:     now,
			},
		},
	}

	// Validate balance before writing
	if err := group.Validate(); err != nil {
		s.logger.Error("CRITICAL: imbalanced entry group detected",
			zap.String("transaction_id", transactionID.String()),
			zap.Error(err),
		)
		return nil
	}

	// Write all entries atomically
	for i := range group.Entries {
		if _, err := s.repo.CreateLedgerEntry(ctx, tx, &group.Entries[i]); err != nil {
			return fmt.Errorf("writing ledger entry: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing ledger entries: %w", err)
	}

	s.logger.Info("transfer recorded in ledger",
		zap.String("transaction_id", transactionID.String()),
		zap.Int64("amount_kobo", event.Amount),
		zap.Int64("fee_kobo", event.Fee),
		zap.String("sender_wallet", *event.SenderID),
		zap.String("receiver_wallet", *event.ReceiverID),
	)

	return nil
}

// processFunding creates 2 ledger entries for a wallet funding:
//
//	Entry 1: DEBIT  external account   amount  (money came from outside)
//	Entry 2: CREDIT user account       amount  (money entered PayFlow)
//
// Total debits must equal Total credits
func (s *ledgerService) processFunding(ctx context.Context, transactionID uuid.UUID, event domain.PaymentEvent) error {
	if event.ReceiverID == nil {
		return fmt.Errorf("funding event missing receiver wallet id")
	}

	receiverWalletID, err := uuid.Parse(*event.ReceiverID)
	if err != nil {
		return fmt.Errorf("invalid receiver wallet id: %w", err)
	}

	receiverAccount, err := s.repo.GetAccountByWalletID(ctx, receiverWalletID)
	if err != nil {
		return fmt.Errorf("getting receiver account: %w", err)
	}

	// Idempotency check
	if receiverAccount != nil {
		exists, err := s.repo.EntryExists(ctx, transactionID, receiverAccount.ID, domain.EntryCredit)
		if err != nil {
			return fmt.Errorf("checking idempotency: %w", err)
		}
		if exists {
			s.logger.Info("funding already processed — skipping",
				zap.String("transaction_id", transactionID.String()),
			)
			return nil
		}
	}

	externalAccount, err := s.repo.GetAccountByName(ctx, domain.AccountPayFlowExternal)
	if err != nil || externalAccount == nil {
		return fmt.Errorf("getting external account: %w", err)
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	receiver, err := s.repo.GetOrCreateUserAccount(ctx, tx, receiverWalletID)
	if err != nil {
		return fmt.Errorf("getting receiver ledger account: %w", err)
	}

	receiverBalance := s.getCurrentBalanceFromRepo(ctx, receiver.ID)
	externalBalance := s.getCurrentBalanceFromRepo(ctx, externalAccount.ID)

	groupID := uuid.New()
	now := time.Now().UTC()

	group := &domain.EntryGroup{
		ID:            groupID,
		TransactionID: transactionID,
		Entries: []domain.LedgerEntry{
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     externalAccount.ID,
				EntryType:     domain.EntryDebit,
				Amount:        event.Amount,
				BalanceAfter:  externalBalance - event.Amount,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Wallet funding from external — txn %s", transactionID),
				CreatedAt:     now,
			},
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     receiver.ID,
				EntryType:     domain.EntryCredit,
				Amount:        event.Amount,
				BalanceAfter:  receiverBalance + event.Amount,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Wallet funded — txn %s", transactionID),
				CreatedAt:     now,
			},
		},
	}

	if err := group.Validate(); err != nil {
		s.logger.Error("CRITICAL: imbalanced funding entry group",
			zap.String("transaction_id", transactionID.String()),
			zap.Error(err),
		)
		return nil
	}

	for i := range group.Entries {
		if _, err := s.repo.CreateLedgerEntry(ctx, tx, &group.Entries[i]); err != nil {
			return fmt.Errorf("writing ledger entry: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing ledger entries: %w", err)
	}

	s.logger.Info("funding recorded in ledger",
		zap.String("transaction_id", transactionID.String()),
		zap.Int64("amount_kobo", event.Amount),
	)

	return nil
}

// processWithdrawal creates 2 ledger entries for a withdrawal:
//
//	Entry 1: DEBIT  user account       amount  (money leaves PayFlow)
//	Entry 2: CREDIT external account   amount  (money goes to external bank)
//
// Total debits must equal Total credits
func (s *ledgerService) processWithdrawal(ctx context.Context, transactionID uuid.UUID, event domain.PaymentEvent) error {
	if event.SenderID == nil {
		return fmt.Errorf("withdrawal event missing sender wallet id")
	}

	senderWalletID, err := uuid.Parse(*event.SenderID)
	if err != nil {
		return fmt.Errorf("invalid sender wallet id: %w", err)
	}

	// Idempotency check
	senderAccount, err := s.repo.GetAccountByWalletID(ctx, senderWalletID)
	if err != nil {
		return fmt.Errorf("getting sender account: %w", err)
	}

	if senderAccount != nil {
		exists, err := s.repo.EntryExists(ctx, transactionID, senderAccount.ID, domain.EntryDebit)
		if err != nil {
			return fmt.Errorf("checking idempotency: %w", err)
		}
		if exists {
			s.logger.Info("withdrawal already processed — skipping",
				zap.String("transaction_id", transactionID.String()),
			)
			return nil
		}
	}

	externalAccount, err := s.repo.GetAccountByName(ctx, domain.AccountPayFlowExternal)
	if err != nil || externalAccount == nil {
		return fmt.Errorf("getting external account: %w", err)
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	sender, err := s.repo.GetOrCreateUserAccount(ctx, tx, senderWalletID)
	if err != nil {
		return fmt.Errorf("getting sender ledger account: %w", err)
	}

	senderBalance := s.getCurrentBalanceFromRepo(ctx, sender.ID)
	externalBalance := s.getCurrentBalanceFromRepo(ctx, externalAccount.ID)

	groupID := uuid.New()
	now := time.Now().UTC()

	group := &domain.EntryGroup{
		ID:            groupID,
		TransactionID: transactionID,
		Entries: []domain.LedgerEntry{
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     sender.ID,
				EntryType:     domain.EntryDebit,
				Amount:        event.Amount,
				BalanceAfter:  senderBalance - event.Amount,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Withdrawal — txn %s", transactionID),
				CreatedAt:     now,
			},
			{
				ID:            uuid.New(),
				TransactionID: transactionID,
				EntryGroupID:  groupID,
				AccountID:     externalAccount.ID,
				EntryType:     domain.EntryCredit,
				Amount:        event.Amount,
				BalanceAfter:  externalBalance + event.Amount,
				Currency:      event.Currency,
				Description:   fmt.Sprintf("Withdrawal to external — txn %s", transactionID),
				CreatedAt:     now,
			},
		},
	}

	if err := group.Validate(); err != nil {
		s.logger.Error("CRITICAL: imbalanced withdrawal entry group",
			zap.String("transaction_id", transactionID.String()),
			zap.Error(err),
		)
		return nil
	}

	for i := range group.Entries {
		if _, err := s.repo.CreateLedgerEntry(ctx, tx, &group.Entries[i]); err != nil {
			return fmt.Errorf("writing ledger entry: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing ledger entries: %w", err)
	}

	s.logger.Info("withdrawal recorded in ledger",
		zap.String("transaction_id", transactionID.String()),
		zap.Int64("amount_kobo", event.Amount),
	)

	return nil
}

func (s *ledgerService) GetEntriesByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.LedgerEntry, error) {
	entries, err := s.repo.GetEntriesByTransactionID(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("getting entries: %w", err)
	}
	return entries, nil
}

func (s *ledgerService) GetCurrentBalance(ctx context.Context, walletID uuid.UUID) (int64, error) {
	account, err := s.repo.GetAccountByWalletID(ctx, walletID)
	if err != nil || account == nil {
		return 0, nil // no entries yet — balance is zero
	}

	latest, err := s.repo.GetLatestEntry(ctx, account.ID)
	if err != nil || latest == nil {
		return 0, nil
	}

	return latest.BalanceAfter, nil
}

// getCurrentBalanceFromRepo returns the current balance for an account.
// Returns 0 if no entries exist yet — new accounts start at zero.
func (s *ledgerService) getCurrentBalanceFromRepo(ctx context.Context, accountID uuid.UUID) int64 {
	latest, err := s.repo.GetLatestEntry(ctx, accountID)
	if err != nil || latest == nil {
		return 0
	}
	return latest.BalanceAfter
}
