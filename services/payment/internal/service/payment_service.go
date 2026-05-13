// internal/service/payment_service.go

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/payment/internal/domain"
	"github.com/Ad3bay0c/payflow/payment/internal/repository"
)

// PaymentService defines all payment operations.
type PaymentService interface {
	CreateWallet(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
	GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error)
	GetWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
	FundWallet(ctx context.Context, req domain.FundWalletRequest) (*domain.Transaction, error)
	Transfer(ctx context.Context, req domain.TransferRequest) (*domain.Transaction, error)
	GetTransaction(ctx context.Context, id uuid.UUID) (*domain.Transaction, error)
	ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int32) ([]*domain.Transaction, int64, error)
}

// maxTransferRetries is how many times we retry on optimistic lock conflict.
const maxTransferRetries = 3

const versionConflictError = "version conflict: wallet was modified concurrently"

type paymentService struct {
	repo   repository.PaymentRepository
	logger *zap.Logger
	cache  *referenceDataCache
}

func NewPaymentService(
	repo repository.PaymentRepository,
	logger *zap.Logger,
) PaymentService {
	return &paymentService{
		repo:   repo,
		logger: logger,
		cache:  newReferenceDataCache(5 * time.Minute),
	}
}

// CreateWallet creates a new wallet for a user.
// Each user can only have one wallet — enforced by the UNIQUE constraint on user_id.
func (s *paymentService) CreateWallet(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	// Check wallet doesn't already exist
	existing, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("checking existing wallet: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("wallet already exists for this user")
	}

	wallet, err := s.repo.CreateWallet(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("creating wallet: %w", err)
	}

	s.logger.Info("wallet created",
		zap.String("wallet_id", wallet.ID.String()),
		zap.String("user_id", userID.String()),
	)

	return wallet, nil
}

func (s *paymentService) GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error) {
	wallet, err := s.repo.GetWalletByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("getting wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("wallet not found")
	}
	return wallet, nil
}

func (s *paymentService) GetWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	wallet, err := s.repo.GetWalletByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("getting wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("wallet not found")
	}
	return wallet, nil
}

// FundWallet adds money to a wallet from an external source.
// Idempotent — duplicate requests with the same key return the original transaction.
func (s *paymentService) FundWallet(
	ctx context.Context,
	req domain.FundWalletRequest,
) (*domain.Transaction, error) {
	// If we've seen this key before, return the existing transaction.
	// This handles network retries safely — never fund twice.
	existing, err := s.repo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info("duplicate funding request — returning existing transaction",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("transaction_id", existing.ID.String()),
		)
		return existing, nil
	}

	// Validate wallet exists
	wallet, err := s.repo.GetWalletByID(ctx, req.WalletID)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("wallet not found")
	}

	// Start DB transaction
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Create the transaction record (pending)
	desc := req.Description
	txn, err := s.repo.CreateTransaction(ctx, tx, repository.CreateTransactionParams{
		IdempotencyKey:   req.IdempotencyKey,
		ReceiverWalletID: &req.WalletID,
		Amount:           req.Amount,
		Fee:              0, // no fee on funding
		Currency:         "NGN",
		Type:             domain.TypeFunding,
		Description:      &desc,
	})
	if err != nil {
		return nil, fmt.Errorf("creating transaction record: %w", err)
	}

	// Credit the wallet
	// FundWallet uses atomic SQL (balance = balance + amount)
	// No pessimistic lock needed — adding money is always safe to retry
	_, err = s.repo.FundWallet(ctx, tx, req.WalletID, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("crediting wallet: %w", err)
	}

	// Mark transaction completed
	now := time.Now().UTC()
	txn, err = s.repo.UpdateTransactionStatus(ctx, tx, txn.ID, domain.StatusCompleted, &now, nil)
	if err != nil {
		return nil, fmt.Errorf("completing transaction: %w", err)
	}

	if err := s.writeOutboxEvent(ctx, tx, "payment.completed", txn); err != nil {
		return nil, fmt.Errorf("writing outbox event: %w", err)
	}

	// Commit
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	s.logger.Info("wallet funded",
		zap.String("wallet_id", req.WalletID.String()),
		zap.Int64("amount_kobo", req.Amount),
		zap.String("transaction_id", txn.ID.String()),
	)

	return txn, nil
}

// Transfer moves money from one wallet to another.
// it must be:
// - Idempotent     (same idempotency key = same result, never double charge)
// - Atomic         (debit and credit both succeed or neither does)
// - Consistent     (balance never goes negative)
// - Isolated       (concurrent transfers don't interfere)
func (s *paymentService) Transfer(ctx context.Context, req domain.TransferRequest) (*domain.Transaction, error) {
	// Idempotency check
	existing, err := s.repo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("checking idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info("duplicate transfer request — returning existing transaction",
			zap.String("idempotency_key", req.IdempotencyKey),
		)
		return existing, nil
	}

	// Check sender and receiver are different
	if req.SenderWalletID == req.ReceiverWalletID {
		return nil, fmt.Errorf("cannot transfer to the same wallet")
	}

	if err := s.checkTierLimits(ctx, req); err != nil {
		return nil, err
	}

	// Retry on optimistic lock conflict
	// The retry loop handles the case where another concurrent transfer
	// modifies the sender wallet between our read and write.
	// maxTransferRetries prevents infinite loops.
	var txn *domain.Transaction
	for attempt := 1; attempt <= maxTransferRetries; attempt++ {
		txn, err = s.executeTransfer(ctx, req)
		if err == nil {
			break
		}

		// Only retry on version conflict — all other errors are fatal
		if err.Error() != versionConflictError {
			return nil, err
		}

		s.logger.Warn("transfer version conflict — retrying",
			zap.Int("attempt", attempt),
			zap.String("sender_wallet_id", req.SenderWalletID.String()),
		)
	}

	if err != nil {
		return nil, fmt.Errorf("transfer failed after %d attempts: %w", maxTransferRetries, err)
	}

	return txn, nil
}

// executeTransfer performs one attempt at the transfer.
// Returns a version conflict error if the wallet was modified concurrently.
func (s *paymentService) executeTransfer(ctx context.Context, req domain.TransferRequest) (*domain.Transaction, error) {
	// Start database transaction
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock sender wallet (pessimistic)
	// FOR UPDATE acquires an exclusive row lock.
	// Any other transaction trying to modify this wallet will wait until we commit or rollback.
	// We lock sender only — receiver credit is safe without a lock because we're adding, not subtracting.
	senderWallet, err := s.repo.GetWalletByIDForUpdate(ctx, tx, req.SenderWalletID)
	if err != nil || senderWallet == nil {
		return nil, fmt.Errorf("sender wallet not found")
	}

	fee, err := s.calculateFee(ctx, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("calculating fee: %w", err)
	}

	// Check sufficient balance (amount + fee)
	if !senderWallet.HasSufficientBalance(req.Amount, fee) {
		return nil, fmt.Errorf("insufficient balance: have %d kobo, need %d kobo",
			senderWallet.Balance,
			req.Amount+fee,
		)
	}

	// Check receiver exists
	receiverWallet, err := s.repo.GetWalletByID(ctx, req.ReceiverWalletID)
	if err != nil || receiverWallet == nil {
		return nil, fmt.Errorf("receiver wallet not found")
	}

	// Create transaction pending record
	desc := req.Description
	txn, err := s.repo.CreateTransaction(ctx, tx, repository.CreateTransactionParams{
		IdempotencyKey:   req.IdempotencyKey,
		SenderWalletID:   &req.SenderWalletID,
		ReceiverWalletID: &req.ReceiverWalletID,
		Amount:           req.Amount,
		Fee:              fee,
		Currency:         "NGN",
		Type:             domain.TypeTransfer,
		Description:      &desc,
	})
	if err != nil {
		return nil, fmt.Errorf("creating transaction record: %w", err)
	}

	// Debit sender (amount + fee) with (Optimistic Lock)
	// We already hold the pessimistic lock from FOR UPDATE.
	// The version check is a second line of defence.
	newSenderBalance := senderWallet.Balance - req.Amount - fee
	updatedSender, err := s.repo.UpdateWalletBalance(ctx, tx,
		req.SenderWalletID,
		newSenderBalance,
		senderWallet.Version, // must match — detects any concurrent modification
	)
	if err != nil {
		return nil, fmt.Errorf("debiting sender: %w", err)
	}
	if updatedSender == nil {
		// Version mismatch — wallet was modified between our read and write
		return nil, fmt.Errorf(versionConflictError)
	}

	// Credit receiver
	// atomic SQL: balance = balance + amount
	// Safe without a lock — adding never causes an overdraft
	_, err = s.repo.FundWallet(ctx, tx, req.ReceiverWalletID, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("crediting receiver: %w", err)
	}

	// Update daily transfer summary
	// Done inside the transaction so it rolls back if anything fails.
	if err = s.repo.UpdateDailyTransferTotal(ctx, tx, req.SenderWalletID, req.Amount); err != nil {
		return nil, fmt.Errorf("updating daily summary: %w", err)
	}

	// Mark transaction completed
	now := time.Now().UTC()
	txn, err = s.repo.UpdateTransactionStatus(ctx, tx, txn.ID, domain.StatusCompleted, &now, nil)
	if err != nil {
		return nil, fmt.Errorf("completing transaction: %w", err)
	}

	if err := s.writeOutboxEvent(ctx, tx, "payment.completed", txn); err != nil {
		return nil, fmt.Errorf("writing outbox event: %w", err)
	}

	// Commit
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	s.logger.Info("transfer completed",
		zap.String("transaction_id", txn.ID.String()),
		zap.String("sender", req.SenderWalletID.String()),
		zap.String("receiver", req.ReceiverWalletID.String()),
		zap.Int64("amount_kobo", req.Amount),
	)

	return txn, nil
}

func (s *paymentService) GetTransaction(ctx context.Context, id uuid.UUID) (*domain.Transaction, error) {
	txn, err := s.repo.GetTransactionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting transaction: %w", err)
	}
	if txn == nil {
		return nil, fmt.Errorf("transaction not found")
	}
	return txn, nil
}

func (s *paymentService) ListTransactions(ctx context.Context, walletID uuid.UUID, limit, offset int32) ([]*domain.Transaction, int64, error) {
	return s.repo.ListTransactionsByWallet(ctx, walletID, limit, offset)
}

// checkTierLimits validates the transfer amount against the sender's
// tier limits — both single transaction and daily cumulative.
func (s *paymentService) checkTierLimits(ctx context.Context, req domain.TransferRequest) error {
	// Fetch from cache first
	limit := s.cache.getTierLimit(req.SenderTier)

	if limit == nil {
		// Cache miss — fetch from database
		var err error
		limit, err = s.repo.GetTierLimit(ctx, req.SenderTier)
		if err != nil {
			return fmt.Errorf("fetching tier limits: %w", err)
		}
		s.cache.setTierLimit(limit)

		s.logger.Debug("tier limit cache miss — fetched from database",
			zap.Int("tier", int(req.SenderTier)),
		)
	}

	// Single transaction limit
	if req.Amount > limit.MaxTransferKobo {
		return fmt.Errorf(
			"transfer amount ₦%.2f exceeds your tier %d single transfer limit of ₦%.2f. "+
				"Complete KYC verification to increase your limit",
			float64(req.Amount)/100,
			req.SenderTier,
			float64(limit.MaxTransferKobo)/100,
		)
	}

	// Daily cumulative limit
	dailyTotal, err := s.repo.GetDailyTransferTotal(ctx, req.SenderWalletID)
	if err != nil {
		return fmt.Errorf("fetching daily total: %w", err)
	}

	if dailyTotal+req.Amount > limit.DailyLimitKobo {
		remaining := limit.DailyLimitKobo - dailyTotal
		return fmt.Errorf(
			"transfer would exceed your daily limit. "+
				"Daily limit: ₦%.2f. Already transferred: ₦%.2f. Remaining: ₦%.2f",
			float64(limit.DailyLimitKobo)/100,
			float64(dailyTotal)/100,
			float64(remaining)/100,
		)
	}

	return nil
}

// calculateFee fetches fee tiers from the database and returns
// the applicable fee for the given transfer amount.
func (s *paymentService) calculateFee(ctx context.Context, amount int64) (int64, error) {
	// fetch from cache first
	tiers := s.cache.getFeeTiers()
	if tiers == nil {
		// Cache miss — fetch from database
		var err error
		tiers, err = s.repo.GetFeeTiers(ctx)
		if err != nil {
			return 0, fmt.Errorf("fetching fee tiers: %w", err)
		}
		s.cache.setFeeTiers(tiers)

		s.logger.Debug("fee tiers cache miss — fetched from database")
	}

	for _, tier := range tiers {
		// MaxAmountKobo of 0 = catch-all tier (no upper bound)
		if tier.MaxAmountKobo == 0 || amount <= tier.MaxAmountKobo {
			return tier.FeeKobo, nil
		}
	}

	// Should never reach here — last tier always has MaxAmountKobo = 0
	return 0, fmt.Errorf("no matching fee tier found for amount %d", amount)
}

// writeOutboxEvent writes a payment event to the outbox table
// inside the same database transaction as the payment.
func (s *paymentService) writeOutboxEvent(
	ctx context.Context,
	tx pgx.Tx,
	topic string,
	txn *domain.Transaction,
) error {
	event := buildPaymentEvent(topic, txn)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}

	return s.repo.CreateOutboxEvent(ctx, tx, topic, txn.ID.String(), payload)
}

func buildPaymentEvent(eventType string, txn *domain.Transaction) domain.PaymentEvent {
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
