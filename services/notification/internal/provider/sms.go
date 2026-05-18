// internal/provider/sms.go

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// SMSProvider sends SMS messages.
type SMSProvider interface {
	Send(ctx context.Context, phone, message string) (providerRef string, err error)
}

// For development
type loggerSMSProvider struct {
	logger *zap.Logger
}

func NewLoggerSMSProvider(logger *zap.Logger) SMSProvider {
	return &loggerSMSProvider{logger: logger}
}

func (p *loggerSMSProvider) Send(ctx context.Context, phone, message string) (string, error) {
	p.logger.Info("SMS (development — not sent)",
		zap.String("phone", maskPhone(phone)),
		zap.String("message", message),
	)
	return fmt.Sprintf("dev-ref-%d", time.Now().UnixNano()), nil
}

// For Production
type termiiSMSProvider struct {
	apiKey     string
	senderID   string
	baseURL    string
	httpClient *http.Client
}

func NewTermiiSMSProvider(apiKey, senderID string) SMSProvider {
	return &termiiSMSProvider{
		apiKey:   apiKey,
		senderID: senderID,
		baseURL:  "https://api.ng.termii.com/api",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *termiiSMSProvider) Send(ctx context.Context, phone, message string) (string, error) {
	payload := map[string]any{
		"to":      phone,
		"from":    p.senderID,
		"sms":     message,
		"type":    "plain",
		"api_key": p.apiKey,
		"channel": "dnd", // "dnd" bypasses Do Not Disturb — required for transaction SMS
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/sms/send", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending SMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("termii returned status %d", resp.StatusCode)
	}

	var result struct {
		MessageID string `json:"message_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return result.MessageID, nil
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:7] + "***" + phone[len(phone)-3:]
}
