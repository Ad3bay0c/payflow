// internal/repository/notification_repository.go

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ad3bay0c/payflow/notification/internal/domain"
	"github.com/Ad3bay0c/payflow/notification/internal/gen/db"
	"github.com/Ad3bay0c/payflow/pkg/pgconv"
)

type NotificationRepository interface {
	Create(ctx context.Context, req domain.NotificationRequest) (*domain.Notification, error)
	MarkSent(ctx context.Context, id uuid.UUID, providerRef string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	GetByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.Notification, error)
	CreatePendingNotification(ctx context.Context, req domain.NotificationRequest) error
	GetPendingNotifications(ctx context.Context, limit int32) ([]*domain.Notification, error)
	UpdateRecipient(ctx context.Context, id uuid.UUID, phone string, userID uuid.UUID) error
}

type postgresNotificationRepository struct {
	queries *db.Queries
}

func NewNotificationRepository(pool *pgxpool.Pool) NotificationRepository {
	return &postgresNotificationRepository{
		queries: db.New(pool),
	}
}

func (r *postgresNotificationRepository) Create(ctx context.Context, req domain.NotificationRequest) (*domain.Notification, error) {
	now := time.Now().UTC()

	var subject *string
	if req.Subject != "" {
		subject = &req.Subject
	}

	row, err := r.queries.CreateNotification(ctx, db.CreateNotificationParams{
		ID:            pgconv.ToPgUUID(uuid.New()),
		TransactionID: pgconv.ToPgUUID(req.TransactionID),
		UserID:        pgconv.ToPgUUID(req.UserID),
		Recipient:     req.Recipient,
		Channel:       string(req.Channel),
		Subject:       pgconv.ToPgTextPtr(subject),
		Body:          req.Body,
		CreatedAt:     pgconv.ToPgTimestamp(now),
	})
	if err != nil {
		return nil, fmt.Errorf("creating notification: %w", err)
	}

	return toDomainNotification(row), nil
}

func (r *postgresNotificationRepository) MarkSent(ctx context.Context, id uuid.UUID, providerRef string) error {
	return r.queries.UpdateNotificationSent(ctx, db.UpdateNotificationSentParams{
		ID:          pgconv.ToPgUUID(id),
		ProviderRef: pgconv.ToPgText(providerRef),
	})
}

func (r *postgresNotificationRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return r.queries.UpdateNotificationFailed(ctx, db.UpdateNotificationFailedParams{
		ID:           pgconv.ToPgUUID(id),
		ErrorMessage: pgconv.ToPgText(errMsg),
	})
}

func (r *postgresNotificationRepository) GetByTransactionID(ctx context.Context, transactionID uuid.UUID) ([]*domain.Notification, error) {
	rows, err := r.queries.GetNotificationsByTransaction(ctx, pgconv.ToPgUUID(transactionID))
	if err != nil {
		return nil, fmt.Errorf("getting notifications: %w", err)
	}

	notifications := make([]*domain.Notification, len(rows))
	for i, row := range rows {
		notifications[i] = toDomainNotification(row)
	}
	return notifications, nil
}

func (r *postgresNotificationRepository) CreatePendingNotification(ctx context.Context, req domain.NotificationRequest) error {
	now := time.Now().UTC()
	_, err := r.queries.CreatePendingNotification(ctx, db.CreatePendingNotificationParams{
		ID:            pgconv.ToPgUUID(uuid.New()),
		TransactionID: pgconv.ToPgUUID(req.TransactionID),
		UserID:        pgconv.ToPgUUID(req.UserID),
		Recipient:     req.Recipient, // wallet_id placeholder
		Channel:       string(req.Channel),
		Body:          req.Body,
		EventID:       pgconv.ToPgText(req.EventID),
		NotifType:     pgconv.ToPgText(req.NotifType),
		WalletIDRef:   pgconv.ToPgText(req.Recipient), // store wallet_id
		CreatedAt:     pgconv.ToPgTimestamp(now),
	})
	if err != nil {
		return fmt.Errorf("creating pending notification: %w", err)
	}
	return nil
}

func (r *postgresNotificationRepository) GetPendingNotifications(ctx context.Context, limit int32) ([]*domain.Notification, error) {
	rows, err := r.queries.GetPendingNotifications(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("getting pending notifications: %w", err)
	}
	notifications := make([]*domain.Notification, len(rows))
	for i, row := range rows {
		notifications[i] = toDomainNotification(row)
	}
	return notifications, nil
}

func (r *postgresNotificationRepository) UpdateRecipient(ctx context.Context, id uuid.UUID, phone string, userID uuid.UUID) error {
	return r.queries.UpdateNotificationRecipient(ctx, db.UpdateNotificationRecipientParams{
		ID:        pgconv.ToPgUUID(id),
		Recipient: phone,
		UserID:    pgconv.ToPgUUID(userID),
	})
}

func toDomainNotification(row db.Notification) *domain.Notification {
	n := &domain.Notification{
		ID:            pgconv.FromPgUUID(row.ID),
		TransactionID: pgconv.FromPgUUID(row.TransactionID),
		UserID:        pgconv.FromPgUUID(row.UserID),
		Recipient:     row.Recipient,
		Channel:       domain.Channel(row.Channel),
		Body:          row.Body,
		Status:        domain.NotificationStatus(row.Status),
		Attempts:      int(row.Attempts),
		CreatedAt:     pgconv.FromPgTimestamp(row.CreatedAt),
		UpdatedAt:     pgconv.FromPgTimestamp(row.UpdatedAt),
	}

	if row.Subject.Valid {
		n.Subject = &row.Subject.String
	}
	if row.ProviderRef.Valid {
		n.ProviderRef = &row.ProviderRef.String
	}
	if row.ErrorMessage.Valid {
		n.ErrorMessage = &row.ErrorMessage.String
	}
	if row.SentAt.Valid {
		t := row.SentAt.Time
		n.SentAt = &t
	}

	return n
}
