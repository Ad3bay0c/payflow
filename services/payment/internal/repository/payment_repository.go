// internal/repository/payment_repository.go

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ad3bay0c/payflow/payment/internal/domain"
	gendb "github.com/Ad3bay0c/payflow/payment/internal/gen/db"
	"github.com/Ad3bay0c/payflow/pkg/pgconv"
)

type PaymentRepository interface {
	CreateWallet(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
	GetWalletByID(ctx context.Context, id uuid.UUID) (*domain.Wallet, error)
	GetWalletByIDAndUserID(ctx context.Context, id, userID uuid.UUID) (*domain.Wallet, error)
	GetWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
	GetWalletByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Wallet, error)
	UpdateWalletBalance(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, newBalance, version int64) (*domain.Wallet, error)
	FundWallet(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, amount int64) (*domain.Wallet, error)

	CreateTransaction(ctx context.Context, tx pgx.Tx, params CreateTransactionParams) (*domain.Transaction, error)
	GetTransactionByID(ctx context.Context, id uuid.UUID) (*domain.Transaction, error)
	GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error)
	UpdateTransactionStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.TransactionStatus, completedAt, failedAt *time.Time) (*domain.Transaction, error)
	ListTransactionsByWallet(ctx context.Context, walletID uuid.UUID, limit, offset int32) ([]*domain.Transaction, int64, error)

	BeginTx(ctx context.Context) (pgx.Tx, error)

	GetTierLimit(ctx context.Context, tier int16) (*domain.TierLimit, error)
	GetDailyTransferTotal(ctx context.Context, walletID uuid.UUID) (int64, error)
	UpdateDailyTransferTotal(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, amount int64) error

	GetFeeTiers(ctx context.Context) ([]domain.FeeTier, error)
}

type CreateTransactionParams struct {
	IdempotencyKey   string
	SenderWalletID   *uuid.UUID
	ReceiverWalletID *uuid.UUID
	Amount           int64
	Fee              int64
	Currency         string
	Type             domain.TransactionType
	Description      *string
	Metadata         map[string]any
}

type postgresPaymentRepository struct {
	pool    *pgxpool.Pool
	queries *gendb.Queries
}

func NewPaymentRepository(pool *pgxpool.Pool) PaymentRepository {
	return &postgresPaymentRepository{
		pool:    pool,
		queries: gendb.New(pool),
	}
}

func (r *postgresPaymentRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.pool.BeginTx(ctx, pgx.TxOptions{})
}

func (r *postgresPaymentRepository) CreateWallet(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	now := time.Now().UTC()
	row, err := r.queries.CreateWallet(ctx, gendb.CreateWalletParams{
		ID:        pgconv.ToPgUUID(uuid.New()),
		UserID:    pgconv.ToPgUUID(userID),
		Currency:  "NGN",
		CreatedAt: pgconv.ToPgTimestamp(now),
		UpdatedAt: pgconv.ToPgTimestamp(now),
	})
	if err != nil {
		return nil, fmt.Errorf("creating wallet: %w", err)
	}
	return toDomainWallet(row), nil
}

func (r *postgresPaymentRepository) GetWalletByID(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	row, err := r.queries.GetWalletByID(ctx, pgconv.ToPgUUID(id))
	if err != nil {
		return nil, nil // not found
	}
	return toDomainWallet(row), nil
}

func (r *postgresPaymentRepository) GetWalletByIDAndUserID(ctx context.Context, id, userID uuid.UUID) (*domain.Wallet, error) {
	row, err := r.queries.GetWalletByIDAndUserID(ctx, gendb.GetWalletByIDAndUserIDParams{
		ID:     pgconv.ToPgUUID(id),
		UserID: pgconv.ToPgUUID(userID),
	})
	if err != nil {
		return nil, nil // not found
	}
	return toDomainWallet(row), nil
}

func (r *postgresPaymentRepository) GetWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	row, err := r.queries.GetWalletByUserID(ctx, pgconv.ToPgUUID(userID))
	if err != nil {
		return nil, nil // not found
	}
	return toDomainWallet(row), nil
}

// GetWalletByIDForUpdate acquires a row-level lock on the wallet.
// MUST be called within a transaction — the lock is held until commit/rollback.
// Used exclusively for the transfer debit path.
func (r *postgresPaymentRepository) GetWalletByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Wallet, error) {
	qtx := r.queries.WithTx(tx)
	row, err := qtx.GetWalletByIDForUpdate(ctx, pgconv.ToPgUUID(id))
	if err != nil {
		return nil, fmt.Errorf("locking wallet: %w", err)
	}
	return toDomainWallet(row), nil
}

// UpdateWalletBalance updates the balance with optimistic locking.
// Returns nil if the version check fails (concurrent modification).
// The caller must retry with fresh wallet data.
func (r *postgresPaymentRepository) UpdateWalletBalance(
	ctx context.Context,
	tx pgx.Tx,
	walletID uuid.UUID,
	newBalance int64,
	version int64,
) (*domain.Wallet, error) {
	qtx := r.queries.WithTx(tx)
	row, err := qtx.UpdateWalletBalance(ctx, gendb.UpdateWalletBalanceParams{
		ID:        pgconv.ToPgUUID(walletID),
		Balance:   newBalance,
		UpdatedAt: pgconv.ToPgTimestamp(time.Now().UTC()),
		Version:   version,
	})
	if err != nil {
		return nil, fmt.Errorf("updating wallet balance: %w", err)
	}
	return toDomainWallet(row), nil
}

func (r *postgresPaymentRepository) FundWallet(
	ctx context.Context,
	tx pgx.Tx,
	walletID uuid.UUID,
	amount int64,
) (*domain.Wallet, error) {
	var qtx *gendb.Queries
	if tx != nil {
		qtx = r.queries.WithTx(tx)
	} else {
		qtx = r.queries
	}

	row, err := qtx.FundWallet(ctx, gendb.FundWalletParams{
		ID:        pgconv.ToPgUUID(walletID),
		Balance:   amount,
		UpdatedAt: pgconv.ToPgTimestamp(time.Now().UTC()),
	})
	if err != nil {
		return nil, fmt.Errorf("funding wallet: %w", err)
	}
	return toDomainWallet(row), nil
}

func (r *postgresPaymentRepository) CreateTransaction(
	ctx context.Context,
	tx pgx.Tx,
	params CreateTransactionParams,
) (*domain.Transaction, error) {
	qtx := r.queries.WithTx(tx)

	// Serialise metadata to JSON if present
	var metadata []byte
	if params.Metadata != nil {
		var err error
		metadata, err = json.Marshal(params.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshalling metadata: %w", err)
		}
	}

	now := time.Now().UTC()
	row, err := qtx.CreateTransaction(ctx, gendb.CreateTransactionParams{
		ID:               pgconv.ToPgUUID(uuid.New()),
		IdempotencyKey:   params.IdempotencyKey,
		SenderWalletID:   pgconv.ToNullPgUUID(params.SenderWalletID),
		ReceiverWalletID: pgconv.ToNullPgUUID(params.ReceiverWalletID),
		Amount:           params.Amount,
		Fee:              params.Fee,
		Currency:         params.Currency,
		Status:           string(domain.StatusPending),
		Type:             string(params.Type),
		Description:      pgconv.ToPgTextPtr(params.Description),
		Metadata:         metadata,
		CreatedAt:        pgconv.ToPgTimestamp(now),
		UpdatedAt:        pgconv.ToPgTimestamp(now),
	})
	if err != nil {
		return nil, fmt.Errorf("creating transaction: %w", err)
	}

	return toDomainTransaction(row), nil
}

func (r *postgresPaymentRepository) GetTransactionByID(ctx context.Context, id uuid.UUID) (*domain.Transaction, error) {
	row, err := r.queries.GetTransactionByID(ctx, pgconv.ToPgUUID(id))
	if err != nil {
		return nil, nil
	}
	return toDomainTransaction(row), nil
}

func (r *postgresPaymentRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error) {
	row, err := r.queries.GetTransactionByIdempotencyKey(ctx, key)
	if err != nil {
		return nil, nil
	}
	return toDomainTransaction(row), nil
}

func (r *postgresPaymentRepository) UpdateTransactionStatus(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	status domain.TransactionStatus,
	completedAt *time.Time,
	failedAt *time.Time,
) (*domain.Transaction, error) {
	var qtx *gendb.Queries
	if tx != nil {
		qtx = r.queries.WithTx(tx)
	} else {
		qtx = r.queries
	}

	row, err := qtx.UpdateTransactionStatus(ctx, gendb.UpdateTransactionStatusParams{
		ID:          pgconv.ToPgUUID(id),
		Status:      string(status),
		CompletedAt: pgconv.ToPgTimestampPtr(completedAt),
		FailedAt:    pgconv.ToPgTimestampPtr(failedAt),
		UpdatedAt:   pgconv.ToPgTimestamp(time.Now().UTC()),
	})
	if err != nil {
		return nil, fmt.Errorf("updating transaction status: %w", err)
	}
	return toDomainTransaction(row), nil
}

func (r *postgresPaymentRepository) ListTransactionsByWallet(
	ctx context.Context,
	walletID uuid.UUID,
	limit, offset int32,
) ([]*domain.Transaction, int64, error) {
	rows, err := r.queries.ListTransactionsByWallet(ctx, gendb.ListTransactionsByWalletParams{
		WalletID: pgconv.ToPgUUID(walletID),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listing transactions: %w", err)
	}

	count, err := r.queries.CountTransactionsByWallet(ctx, pgconv.ToPgUUID(walletID))
	if err != nil {
		return nil, 0, fmt.Errorf("counting transactions: %w", err)
	}

	txns := make([]*domain.Transaction, len(rows))
	for i, row := range rows {
		txns[i] = toDomainTransaction(row)
	}

	return txns, count, nil
}

func toDomainWallet(row gendb.Wallet) *domain.Wallet {
	return &domain.Wallet{
		ID:        pgconv.FromPgUUID(row.ID),
		UserID:    pgconv.FromPgUUID(row.UserID),
		Balance:   row.Balance,
		Currency:  row.Currency,
		IsActive:  row.IsActive,
		Version:   row.Version,
		CreatedAt: pgconv.FromPgTimestamp(row.CreatedAt),
		UpdatedAt: pgconv.FromPgTimestamp(row.UpdatedAt),
	}
}

func toDomainTransaction(row gendb.Transaction) *domain.Transaction {
	txn := &domain.Transaction{
		ID:             pgconv.FromPgUUID(row.ID),
		IdempotencyKey: row.IdempotencyKey,
		Amount:         row.Amount,
		Fee:            row.Fee,
		Currency:       row.Currency,
		Status:         domain.TransactionStatus(row.Status),
		Type:           domain.TransactionType(row.Type),
		CreatedAt:      pgconv.FromPgTimestamp(row.CreatedAt),
		UpdatedAt:      pgconv.FromPgTimestamp(row.UpdatedAt),
	}

	// Nullable fields
	if row.SenderWalletID.Valid {
		id := pgconv.FromPgUUID(pgtype.UUID{Bytes: row.SenderWalletID.Bytes, Valid: true})
		txn.SenderWalletID = &id
	}
	if row.ReceiverWalletID.Valid {
		id := pgconv.FromPgUUID(pgtype.UUID{Bytes: row.ReceiverWalletID.Bytes, Valid: true})
		txn.ReceiverWalletID = &id
	}
	if row.Description.Valid {
		txn.Description = &row.Description.String
	}
	if row.CompletedAt.Valid {
		t := row.CompletedAt.Time
		txn.CompletedAt = &t
	}
	if row.FailedAt.Valid {
		t := row.FailedAt.Time
		txn.FailedAt = &t
	}
	if row.Metadata != nil {
		var meta map[string]any
		if err := json.Unmarshal(row.Metadata, &meta); err == nil {
			txn.Metadata = meta
		}
	}

	return txn
}

func (r *postgresPaymentRepository) GetTierLimit(ctx context.Context, tier int16) (*domain.TierLimit, error) {
	row, err := r.queries.GetTierLimit(ctx, tier)
	if err != nil {
		return nil, fmt.Errorf("getting tier limit: %w", err)
	}
	return &domain.TierLimit{
		Tier:            row.Tier,
		MaxTransferKobo: row.MaxTransferKobo,
		DailyLimitKobo:  row.DailyLimitKobo,
		Description:     row.Description.String,
	}, nil
}

func (r *postgresPaymentRepository) GetDailyTransferTotal(ctx context.Context, walletID uuid.UUID) (int64, error) {
	total, err := r.queries.GetDailyTransferTotal(ctx, pgconv.ToPgUUID(walletID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("getting daily total: %w", err)
	}
	return total, nil
}

func (r *postgresPaymentRepository) UpdateDailyTransferTotal(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, amount int64) error {
	qtx := r.queries.WithTx(tx)
	return qtx.UpsertDailyTransferSummary(ctx, gendb.UpsertDailyTransferSummaryParams{
		WalletID:  pgconv.ToPgUUID(walletID),
		TotalKobo: amount,
	})
}

func (r *postgresPaymentRepository) GetFeeTiers(ctx context.Context) ([]domain.FeeTier, error) {
	rows, err := r.queries.GetFeeTiers(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting fee tiers: %w", err)
	}

	tiers := make([]domain.FeeTier, len(rows))
	for i, row := range rows {
		tiers[i] = domain.FeeTier{
			ID:            row.ID,
			MaxAmountKobo: row.MaxAmountKobo,
			FeeKobo:       row.FeeKobo,
			Description:   row.Description.String,
		}
	}
	return tiers, nil
}
