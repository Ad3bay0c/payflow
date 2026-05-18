// internal/service/notification_service.go

package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/notification/internal/domain"
	"github.com/Ad3bay0c/payflow/notification/internal/provider"
	"github.com/Ad3bay0c/payflow/notification/internal/repository"
)

// UserLookup retrieves user contact details for notification delivery.
// In production this calls the auth service API.
// We define it as an interface so it's swappable and testable.
type UserLookup interface {
	GetPhoneByWalletID(ctx context.Context, walletID string) (userID uuid.UUID, phone string, err error)
}

type NotificationService interface {
	ProcessPaymentEvent(ctx context.Context, event domain.PaymentEvent) error
}

type notificationService struct {
	repo       repository.NotificationRepository
	sms        provider.SMSProvider
	push       provider.PushProvider
	userLookup UserLookup
	logger     *zap.Logger
}

func NewNotificationService(
	repo repository.NotificationRepository,
	sms provider.SMSProvider,
	push provider.PushProvider,
	userLookup UserLookup,
	logger *zap.Logger,
) NotificationService {
	return &notificationService{
		repo:       repo,
		sms:        sms,
		push:       push,
		userLookup: userLookup,
		logger:     logger,
	}
}

// ProcessPaymentEvent determines which notifications to send
// based on the payment event type.
func (s *notificationService) ProcessPaymentEvent(ctx context.Context, event domain.PaymentEvent) error {
	txnID, err := uuid.Parse(event.TransactionID)
	if err != nil {
		return fmt.Errorf("invalid transaction id: %w", err)
	}

	switch event.Type {
	case "transfer":
		return s.processTransferNotifications(ctx, txnID, event)
	case "funding":
		return s.processFundingNotification(ctx, txnID, event)
	default:
		s.logger.Debug("no notification configured for event type",
			zap.String("type", event.Type),
		)
		return nil
	}
}

// processTransferNotifications sends notifications to both
// the sender (debit alert) and receiver (credit alert).
func (s *notificationService) processTransferNotifications(ctx context.Context, txnID uuid.UUID, event domain.PaymentEvent) error {
	// Notify sender — debit alert
	if event.SenderID != nil {
		userID, phone, err := s.userLookup.GetPhoneByWalletID(ctx, *event.SenderID)
		if err != nil {
			s.logger.Error("failed to lookup sender for notification",
				zap.String("wallet_id", *event.SenderID),
				zap.Error(err),
			)
		} else {
			message := fmt.Sprintf(
				"PayFlow: Your account has been debited ₦%.2f. "+
					"New balance reflected in your app. Txn ref: %s",
				domain.AmountInNaira(event.Amount+event.Fee),
				txnID.String()[:8],
			)
			s.sendSMS(ctx, txnID, userID, phone, message)
		}
	}

	// Notify receiver — credit alert
	if event.ReceiverID != nil {
		userID, phone, err := s.userLookup.GetPhoneByWalletID(ctx, *event.ReceiverID)
		if err != nil {
			s.logger.Error("failed to lookup receiver for notification",
				zap.String("wallet_id", *event.ReceiverID),
				zap.Error(err),
			)
		} else {
			message := fmt.Sprintf(
				"PayFlow: You have received ₦%.2f. "+
					"Your wallet has been credited. Txn ref: %s",
				domain.AmountInNaira(event.Amount),
				txnID.String()[:8],
			)
			s.sendSMS(ctx, txnID, userID, phone, message)
		}
	}

	return nil
}

// processFundingNotification notifies the wallet owner of a credit.
func (s *notificationService) processFundingNotification(ctx context.Context, txnID uuid.UUID, event domain.PaymentEvent) error {
	if event.ReceiverID == nil {
		return nil
	}

	userID, phone, err := s.userLookup.GetPhoneByWalletID(ctx, *event.ReceiverID)
	if err != nil {
		s.logger.Error("failed to lookup wallet owner for funding notification",
			zap.String("wallet_id", *event.ReceiverID),
			zap.Error(err),
		)
		return nil
	}

	message := fmt.Sprintf(
		"PayFlow: Your wallet has been funded with ₦%.2f. Txn ref: %s",
		domain.AmountInNaira(event.Amount),
		txnID.String()[:8],
	)

	s.sendSMS(ctx, txnID, userID, phone, message)
	return nil
}

// sendSMS creates a notification record and sends the SMS.
// Logs errors but does not fail the caller — notification failure
// must never affect the payment flow.
func (s *notificationService) sendSMS(
	ctx context.Context,
	txnID uuid.UUID,
	userID uuid.UUID,
	phone string,
	message string,
) {
	// Create notification record first — captures the attempt even if sending fails
	notification, err := s.repo.Create(ctx, domain.NotificationRequest{
		TransactionID: txnID,
		UserID:        userID,
		Recipient:     phone,
		Channel:       domain.ChannelSMS,
		Body:          message,
	})
	if err != nil {
		s.logger.Error("failed to create notification record",
			zap.Error(err),
		)
		return
	}

	// Send via SMS provider
	providerRef, err := s.sms.Send(ctx, phone, message)
	if err != nil {
		s.logger.Error("failed to send SMS",
			zap.String("notification_id", notification.ID.String()),
			zap.String("phone", maskPhone(phone)),
			zap.Error(err),
		)
		s.repo.MarkFailed(ctx, notification.ID, err.Error()) //nolint:errcheck
		return
	}

	s.repo.MarkSent(ctx, notification.ID, providerRef) //nolint:errcheck

	s.logger.Info("SMS notification sent",
		zap.String("notification_id", notification.ID.String()),
		zap.String("phone", maskPhone(phone)),
	)
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:7] + "***" + phone[len(phone)-3:]
}
