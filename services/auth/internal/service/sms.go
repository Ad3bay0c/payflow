// internal/service/sms.go
//
// SMS gateway interface for OTP delivery.
// The interface lets us swap providers (Termii, Twilio, AWS SNS)
// without touching the auth service logic.
// In development, the logger implementation is used — no real SMS sent.

package service

import (
	"context"

	"go.uber.org/zap"
)

// SMSSender is the interface the auth service depends on.
// It knows nothing about which provider is underneath.
type SMSSender interface {
	Send(ctx context.Context, phone, message string) error
}

// Development implementation

// loggerSMSSender logs the OTP instead of sending a real SMS.
// Used in development and testing.
type loggerSMSSender struct {
	logger *zap.Logger
}

func NewLoggerSMSSender(logger *zap.Logger) SMSSender {
	return &loggerSMSSender{logger: logger}
}

func (s *loggerSMSSender) Send(ctx context.Context, phone, message string) error {
	s.logger.Info("SMS (development — not sent)",
		zap.String("phone", maskPhone(phone)),
		zap.String("message", message),
	)

	// TODO: Send via SMS gateway in production
	// In development we log it — remove this line before going to production
	return nil
}
