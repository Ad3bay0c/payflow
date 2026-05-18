// internal/provider/push.go
//
// Push notification provider interface.
// FCM (Firebase Cloud Messaging) for production.
// Logger implementation for development.

package provider

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// PushProvider sends push notifications.
type PushProvider interface {
	Send(ctx context.Context, token, title, body string) (providerRef string, err error)
}

// For development

type loggerPushProvider struct {
	logger *zap.Logger
}

func NewLoggerPushProvider(logger *zap.Logger) PushProvider {
	return &loggerPushProvider{logger: logger}
}

func (p *loggerPushProvider) Send(ctx context.Context, token, title, body string) (string, error) {
	p.logger.Info("Push notification (development — not sent)",
		zap.String("title", title),
		zap.String("body", body),
	)
	return fmt.Sprintf("dev-push-ref-%d", time.Now().UnixNano()), nil
}

// For production

type fcmPushProvider struct {
	serverKey string
}

func NewFCMPushProvider(serverKey string) PushProvider {
	return &fcmPushProvider{serverKey: serverKey}
}

func (p *fcmPushProvider) Send(ctx context.Context, token, title, body string) (string, error) {
	// TODO: implement FCM HTTP v1 API
	// POST https://fcm.googleapis.com/v1/projects/{project}/messages:send
	return "", fmt.Errorf("FCM not yet implemented")
}
