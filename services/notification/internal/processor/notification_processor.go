// internal/processor/notification_processor.go
//
// Processes pending notification records from the database.
// Runs as a background goroutine — polls every second.
//
// Flow per notification:
//  1. Fetch pending records (SKIP LOCKED — safe for multiple instances)
//  2. Resolve wallet_id → phone number via payment + auth services
//  3. Send SMS via provider
//  4. Mark as sent or increment failure count
//  5. After 3 failures → mark as failed (requires manual intervention)

package processor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/notification/internal/domain"
	"github.com/Ad3bay0c/payflow/notification/internal/provider"
	"github.com/Ad3bay0c/payflow/notification/internal/repository"
	"github.com/Ad3bay0c/payflow/notification/internal/service"
)

const (
	batchSize    = 50
	pollInterval = time.Second
)

type NotificationProcessor struct {
	repo       repository.NotificationRepository
	sms        provider.SMSProvider
	userLookup service.UserLookup
	logger     *zap.Logger
}

func NewNotificationProcessor(
	repo repository.NotificationRepository,
	sms provider.SMSProvider,
	userLookup service.UserLookup,
	logger *zap.Logger,
) *NotificationProcessor {
	return &NotificationProcessor{
		repo:       repo,
		sms:        sms,
		userLookup: userLookup,
		logger:     logger,
	}
}

// Start polls the database for pending notifications.
// Blocks until ctx is cancelled.
func (p *NotificationProcessor) Start(ctx context.Context) {
	p.logger.Info("notification processor started",
		zap.Duration("poll_interval", pollInterval),
		zap.Int("batch_size", batchSize),
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("notification processor stopping")
			return
		case <-ticker.C:
			p.processBatch(ctx)
		}
	}
}

func (p *NotificationProcessor) processBatch(ctx context.Context) {
	notifications, err := p.repo.GetPendingNotifications(ctx, batchSize)
	if err != nil {
		p.logger.Error("failed to fetch pending notifications", zap.Error(err))
		return
	}

	if len(notifications) == 0 {
		return
	}

	p.logger.Debug("processing notification batch",
		zap.Int("count", len(notifications)),
	)

	for _, notif := range notifications {
		p.processOne(ctx, notif)
	}
}

func (p *NotificationProcessor) processOne(ctx context.Context, notif *domain.Notification) {
	phone := notif.Recipient
	userID := notif.UserID

	// If recipient looks like a UUID it's still a walletID — resolve it
	if isWalletID(notif.Recipient) {
		resolvedUserID, resolvedPhone, err := p.userLookup.GetPhoneByWalletID(ctx, notif.Recipient)
		if err != nil {
			p.logger.Error("failed to resolve wallet to phone",
				zap.String("notification_id", notif.ID.String()),
				zap.String("wallet_id", notif.Recipient),
				zap.Error(err),
			)
			p.repo.MarkFailed(ctx, notif.ID, err.Error()) //nolint:errcheck
			return
		}

		phone = resolvedPhone
		userID = resolvedUserID

		// Update the record with the real phone number
		if err := p.repo.UpdateRecipient(ctx, notif.ID, phone, userID); err != nil {
			p.logger.Error("failed to update recipient",
				zap.String("notification_id", notif.ID.String()),
				zap.Error(err),
			)
		}
	}

	// Send SMS
	providerRef, err := p.sms.Send(ctx, phone, notif.Body)
	if err != nil {
		p.logger.Error("failed to send SMS",
			zap.String("notification_id", notif.ID.String()),
			zap.String("phone", maskPhone(phone)),
			zap.Error(err),
		)
		p.repo.MarkFailed(ctx, notif.ID, err.Error()) //nolint:errcheck
		return
	}

	if err := p.repo.MarkSent(ctx, notif.ID, providerRef); err != nil {
		p.logger.Error("failed to mark notification sent",
			zap.String("notification_id", notif.ID.String()),
			zap.Error(err),
		)
		return
	}

	p.logger.Info("notification delivered",
		zap.String("notification_id", notif.ID.String()),
		zap.String("phone", maskPhone(phone)),
		zap.String("notif_type", notif.NotifType),
	)
}

// isWalletID checks if a string looks like a UUID (wallet_id placeholder)
// vs a real phone number.
func isWalletID(s string) bool {
	return len(s) == 36 && s[8] == '-'
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:7] + "***" + phone[len(phone)-3:]
}
