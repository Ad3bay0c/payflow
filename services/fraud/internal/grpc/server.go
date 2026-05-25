// internal/grpc/server.go

package grpc

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Ad3bay0c/payflow/fraud/internal/domain"
	"github.com/Ad3bay0c/payflow/fraud/internal/service"
	fraudpb "github.com/Ad3bay0c/payflow/proto/gen/fraud"
)

// FraudGRPCServer implements the generated FraudServiceServer interface.
type FraudGRPCServer struct {
	fraudpb.UnimplementedFraudServiceServer
	fraudSvc service.FraudService
	logger   *zap.Logger
}

func NewFraudGRPCServer(fraudSvc service.FraudService, logger *zap.Logger) *FraudGRPCServer {
	return &FraudGRPCServer{
		fraudSvc: fraudSvc,
		logger:   logger,
	}
}

// Check evaluates a payment request for fraud risk.
func (s *FraudGRPCServer) Check(ctx context.Context, req *fraudpb.CheckRequest) (*fraudpb.CheckResponse, error) {
	if req.TransactionId == "" || req.SenderWalletId == "" || req.ReceiverWalletId == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id, sender_wallet_id and receiver_wallet_id are required")
	}

	requestedAt, err := time.Parse(time.RFC3339, req.RequestedAt)
	if err != nil {
		requestedAt = time.Now().UTC()
	}

	domainReq := domain.CheckRequest{
		SenderWalletID:   mustParseUUID(req.SenderWalletId),
		ReceiverWalletID: mustParseUUID(req.ReceiverWalletId),
		SenderUserID:     mustParseUUID(req.SenderUserId),
		TransactionID:    mustParseUUID(req.TransactionId),
		Amount:           req.AmountKobo,
		Currency:         req.Currency,
		SenderTier:       int16(req.SenderTier),
		SenderKYCStatus:  req.SenderKycStatus,
		IPAddress:        req.IpAddress,
		DeviceID:         req.DeviceId,
		RequestedAt:      requestedAt,
	}

	result := s.fraudSvc.Check(ctx, domainReq)

	return &fraudpb.CheckResponse{
		TransactionId: result.TransactionID.String(),
		Decision:      string(result.Decision),
		RiskScore:     int32(result.RiskScore),
		RiskLevel:     string(result.RiskLevel),
		Reasons:       result.Reasons,
		LatencyMs:     result.LatencyMs,
	}, nil
}

// RecordApproved updates velocity counters after a successful transfer.
func (s *FraudGRPCServer) RecordApproved(ctx context.Context, req *fraudpb.CheckRequest) (*fraudpb.RecordResponse, error) {
	requestedAt, err := time.Parse(time.RFC3339, req.RequestedAt)
	if err != nil {
		requestedAt = time.Now().UTC()
	}

	domainReq := domain.CheckRequest{
		SenderWalletID:   mustParseUUID(req.SenderWalletId),
		ReceiverWalletID: mustParseUUID(req.ReceiverWalletId),
		SenderUserID:     mustParseUUID(req.SenderUserId),
		TransactionID:    mustParseUUID(req.TransactionId),
		Amount:           req.AmountKobo,
		Currency:         req.Currency,
		SenderTier:       int16(req.SenderTier),
		SenderKYCStatus:  req.SenderKycStatus,
		RequestedAt:      requestedAt,
	}

	s.fraudSvc.RecordApprovedTransfer(ctx, domainReq)

	return &fraudpb.RecordResponse{Recorded: true}, nil
}
