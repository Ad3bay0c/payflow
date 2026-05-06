// internal/repository/user_repository.go

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ad3bay0c/payflow/auth/internal/domain"
	"github.com/Ad3bay0c/payflow/auth/internal/gen/db"

	"github.com/Ad3bay0c/payflow/pkg/pgconv"
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	FindByPhone(ctx context.Context, phone string) (*domain.User, error)
	FindPasswordHash(ctx context.Context, userID uuid.UUID) ([]byte, error)
	UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status domain.KYCStatus, tier domain.Tier) error
	SoftDelete(ctx context.Context, userID uuid.UUID) error
	CreateAuditLog(ctx context.Context, params CreateAuditLogParams) error
}

// CreateAuditLogParams holds the data needed to record an auth event.
type CreateAuditLogParams struct {
	UserID    *uuid.UUID
	Event     string
	IPAddress string
	UserAgent string
	Metadata  []byte
}

type postgresUserRepository struct {
	queries *db.Queries
	pool    *pgxpool.Pool
}

// NewUserRepository returns a PostgreSQL-backed UserRepository.
// Accepts a pgxpool.Pool — connection pooling handled at this layer.
func NewUserRepository(pool *pgxpool.Pool) UserRepository {
	return &postgresUserRepository{
		queries: db.New(pool),
		pool:    pool,
	}
}

// Create inserts a new user and their credentials atomically.
func (r *postgresUserRepository) Create(ctx context.Context, user *domain.User) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := r.queries.WithTx(tx)

	err = qtx.CreateUser(ctx, db.CreateUserParams{
		ID:          pgconv.ToPgUUID(user.ID),
		PhoneNumber: user.PhoneNumber,
		Email:       pgconv.ToPgTextPtr(user.Email),
		FullName:    user.FullName,
		KycStatus:   string(user.KYCStatus),
		Tier:        int16(user.Tier),
		IsActive:    user.IsActive,
		CreatedAt:   pgconv.ToPgTimestamp(user.CreatedAt),
		UpdatedAt:   pgconv.ToPgTimestamp(user.UpdatedAt),
	})
	if err != nil {
		return fmt.Errorf("inserting user: %w", err)
	}

	err = qtx.CreateUserCredentials(ctx, db.CreateUserCredentialsParams{
		UserID:       pgconv.ToPgUUID(user.ID),
		PasswordHash: user.PasswordHash,
		CreatedAt:    pgconv.ToPgTimestamp(user.CreatedAt),
		UpdatedAt:    pgconv.ToPgTimestamp(user.UpdatedAt),
	})
	if err != nil {
		return fmt.Errorf("inserting credentials: %w", err)
	}

	return tx.Commit(ctx)
}

// FindByID retrieves a user by UUID. Returns nil, nil if not found.
func (r *postgresUserRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	row, err := r.queries.FindUserByID(ctx, pgconv.ToPgUUID(id))
	if err != nil {
		return nil, nil // not found
	}
	return toDomainUser(row), nil
}

// FindByPhone retrieves a user by phone number. Returns nil, nil if not found.
func (r *postgresUserRepository) FindByPhone(ctx context.Context, phone string) (*domain.User, error) {
	row, err := r.queries.FindUserByPhone(ctx, phone)
	if err != nil {
		return nil, nil // not found
	}
	return toDomainUser(row), nil
}

// FindPasswordHash retrieves the bcrypt hash for a user.
func (r *postgresUserRepository) FindPasswordHash(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	hash, err := r.queries.FindPasswordHash(ctx, pgconv.ToPgUUID(userID))
	if err != nil {
		return nil, nil // not found
	}
	return hash, nil
}

// UpdateKYCStatus upgrades a user's verification level.
func (r *postgresUserRepository) UpdateKYCStatus(
	ctx context.Context,
	userID uuid.UUID,
	status domain.KYCStatus,
	tier domain.Tier,
) error {
	return r.queries.UpdateKYCStatus(ctx, db.UpdateKYCStatusParams{
		ID:        pgconv.ToPgUUID(userID),
		KycStatus: string(status),
		Tier:      int16(tier),
		UpdatedAt: pgconv.ToPgTimestamp(time.Now().UTC()),
	})
}

// SoftDelete marks a user deleted without removing their data.
func (r *postgresUserRepository) SoftDelete(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()
	return r.queries.SoftDeleteUser(ctx, db.SoftDeleteUserParams{
		ID:        pgconv.ToPgUUID(userID),
		DeletedAt: pgconv.ToPgTimestamp(now),
	})
}

// CreateAuditLog records a security event for CBN compliance.
func (r *postgresUserRepository) CreateAuditLog(ctx context.Context, params CreateAuditLogParams) error {
	userID := pgtype.UUID{}
	if params.UserID != nil {
		userID = pgconv.ToPgUUID(*params.UserID)
	}

	return r.queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ID:        pgconv.ToPgUUID(uuid.New()),
		UserID:    userID,
		Event:     params.Event,
		IpAddress: pgtype.Text{String: params.IPAddress, Valid: params.IPAddress != ""},
		UserAgent: pgtype.Text{String: params.UserAgent, Valid: params.UserAgent != ""},
		Metadata:  params.Metadata,
		CreatedAt: pgconv.ToPgTimestamp(time.Now().UTC()),
	})
}

func toDomainUser(row db.User) *domain.User {
	user := &domain.User{
		ID:          row.ID.Bytes,
		PhoneNumber: row.PhoneNumber,
		FullName:    row.FullName,
		KYCStatus:   domain.KYCStatus(row.KycStatus),
		Tier:        domain.Tier(row.Tier),
		IsActive:    row.IsActive,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}

	// Translate sql.NullString → *string
	if row.Email.Valid {
		user.Email = &row.Email.String
	}

	return user
}
