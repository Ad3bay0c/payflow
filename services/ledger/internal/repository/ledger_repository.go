// internal/repository/ledger_repository.go

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ad3bay0c/payflow/ledger/internal/domain"
	"github.com/Ad3bay0c/payflow/ledger/internal/gen/db"
	"github.com/Ad3bay0c/payflow/pkg/pgconv"
)

type LedgerRepository interface {
	// Account methods
	GetAccountByWalletID(ctx context.Context, walletID uuid.UUID) (*domain.Account, error)
	GetAccountByName(ctx context.Context, name string) (*domain.Account, error)
	CreateAccount(ctx context.Context, tx pgx.Tx, account *domain.Account) (*domain.Account, error)
	GetOrCreateUserAccount(ctx context.Context, tx pgx.Tx, walletID uuid.UUID) (*domain.Account, error)

	// Entry methods
	CreateLedgerEntry(ctx context.Context, tx pgx.Tx, entry *domain.LedgerEntry) (*domain.LedgerEntry, error)
	GetLatestEntry(ctx context.Context, accountID uuid.UUID) (*domain.LedgerEntry, error)
	GetEntriesByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.LedgerEntry, error)
	EntryExists(ctx context.Context, transactionID, accountID uuid.UUID, entryType domain.EntryType) (bool, error)

	// Transaction
	BeginTx(ctx context.Context) (pgx.Tx, error)
}

type postgresLedgerRepository struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewLedgerRepository(pool *pgxpool.Pool) LedgerRepository {
	return &postgresLedgerRepository{
		pool:    pool,
		queries: db.New(pool),
	}
}

func (r *postgresLedgerRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.pool.BeginTx(ctx, pgx.TxOptions{})
}

func (r *postgresLedgerRepository) GetAccountByWalletID(ctx context.Context, walletID uuid.UUID) (*domain.Account, error) {
	row, err := r.queries.GetAccountByWalletID(ctx, pgconv.ToNullPgUUID(&walletID))
	if err != nil {
		return nil, nil // not found
	}
	return toDomainAccount(row), nil
}

func (r *postgresLedgerRepository) GetAccountByName(ctx context.Context, name string) (*domain.Account, error) {
	row, err := r.queries.GetAccountByName(ctx, name)
	if err != nil {
		return nil, nil
	}
	return toDomainAccount(row), nil
}

func (r *postgresLedgerRepository) CreateAccount(ctx context.Context, tx pgx.Tx, account *domain.Account) (*domain.Account, error) {
	qtx := r.queries.WithTx(tx)
	row, err := qtx.CreateAccount(ctx, db.CreateAccountParams{
		ID:          pgconv.ToPgUUID(account.ID),
		WalletID:    pgconv.ToNullPgUUID(account.WalletID),
		AccountType: string(account.AccountType),
		Name:        account.Name,
		Currency:    account.Currency,
		CreatedAt:   pgconv.ToPgTimestamp(account.CreatedAt),
	})
	if err != nil {
		return nil, fmt.Errorf("creating account: %w", err)
	}
	return toDomainAccount(row), nil
}

// GetOrCreateUserAccount returns the ledger account for a wallet,
// creating it if it doesn't exist yet.
// This handles the case where a user makes their first transaction —
// their ledger account is created lazily on first use.
func (r *postgresLedgerRepository) GetOrCreateUserAccount(ctx context.Context, tx pgx.Tx, walletID uuid.UUID) (*domain.Account, error) {
	// Try to get existing account first
	account, err := r.GetAccountByWalletID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("getting account: %w", err)
	}
	if account != nil {
		return account, nil
	}

	// Create new account for this wallet
	now := time.Now().UTC()
	newAccount := &domain.Account{
		ID:          uuid.New(),
		WalletID:    &walletID,
		AccountType: domain.AccountTypeUser,
		Name:        fmt.Sprintf("WALLET_%s", walletID.String()),
		Currency:    "NGN",
		IsActive:    true,
		CreatedAt:   now,
	}

	return r.CreateAccount(ctx, tx, newAccount)
}

func (r *postgresLedgerRepository) CreateLedgerEntry(ctx context.Context, tx pgx.Tx, entry *domain.LedgerEntry) (*domain.LedgerEntry, error) {
	qtx := r.queries.WithTx(tx)

	row, err := qtx.CreateLedgerEntry(ctx, db.CreateLedgerEntryParams{
		ID:            pgconv.ToPgUUID(entry.ID),
		TransactionID: pgconv.ToPgUUID(entry.TransactionID),
		EntryGroupID:  pgconv.ToPgUUID(entry.EntryGroupID),
		AccountID:     pgconv.ToPgUUID(entry.AccountID),
		EntryType:     string(entry.EntryType),
		Amount:        entry.Amount,
		BalanceAfter:  entry.BalanceAfter,
		Currency:      entry.Currency,
		Description:   pgconv.ToPgText(entry.Description),
		CreatedAt:     pgconv.ToPgTimestamp(entry.CreatedAt),
	})
	if err != nil {
		return nil, fmt.Errorf("creating ledger entry: %w", err)
	}

	return toDomainEntry(row), nil
}

// GetLatestEntry returns the most recent entry for an account.
func (r *postgresLedgerRepository) GetLatestEntry(ctx context.Context, accountID uuid.UUID) (*domain.LedgerEntry, error) {
	row, err := r.queries.GetLatestEntry(ctx, pgconv.ToPgUUID(accountID))
	if err != nil {
		return nil, nil // no entries yet — balance is 0
	}
	return toDomainEntry(row), nil
}

func (r *postgresLedgerRepository) GetEntriesByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.LedgerEntry, error) {
	rows, err := r.queries.GetEntriesByTransactionID(ctx, pgconv.ToPgUUID(transactionID))
	if err != nil {
		return nil, fmt.Errorf("getting entries by transaction: %w", err)
	}

	entries := make([]*domain.LedgerEntry, len(rows))
	for i, row := range rows {
		entries[i] = toDomainEntry(row)
	}
	return entries, nil
}

// EntryExists checks if a ledger entry already exists for this transaction.
// Used for idempotency — prevents duplicate entries on Kafka message redelivery.
func (r *postgresLedgerRepository) EntryExists(ctx context.Context, transactionID, accountID uuid.UUID, entryType domain.EntryType) (bool, error) {
	_, err := r.queries.GetEntryByTransactionAndAccount(ctx, db.GetEntryByTransactionAndAccountParams{
		TransactionID: pgconv.ToPgUUID(transactionID),
		AccountID:     pgconv.ToPgUUID(accountID),
		EntryType:     string(entryType),
	})
	if err != nil {
		return false, nil // not found — entry doesn't exist
	}
	return true, nil
}

func toDomainAccount(row db.Account) *domain.Account {
	account := &domain.Account{
		ID:          pgconv.FromPgUUID(row.ID),
		AccountType: domain.AccountType(row.AccountType),
		Name:        row.Name,
		Currency:    row.Currency,
		IsActive:    row.IsActive,
		CreatedAt:   pgconv.FromPgTimestamp(row.CreatedAt),
	}
	if row.WalletID.Valid {
		id := pgconv.FromPgUUID(row.WalletID)
		account.WalletID = &id
	}
	return account
}

func toDomainEntry(row db.LedgerEntry) *domain.LedgerEntry {
	return &domain.LedgerEntry{
		ID:            pgconv.FromPgUUID(row.ID),
		TransactionID: pgconv.FromPgUUID(row.TransactionID),
		EntryGroupID:  pgconv.FromPgUUID(row.EntryGroupID),
		AccountID:     pgconv.FromPgUUID(row.AccountID),
		EntryType:     domain.EntryType(row.EntryType),
		Amount:        row.Amount,
		BalanceAfter:  row.BalanceAfter,
		Currency:      row.Currency,
		Description:   pgconv.FromPgText(row.Description),
		CreatedAt:     pgconv.FromPgTimestamp(row.CreatedAt),
	}
}
