// internal/grpc/server.go
//
// gRPC server for the payment service.
// Exposes wallet operations to internal services.
// The notification service calls GetWalletOwner to resolve
// wallet_id → user_id before fetching the phone from auth service.

package grpc

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Ad3bay0c/payflow/payment/internal/service"
	paymentpb "github.com/Ad3bay0c/payflow/proto/gen/payment"
	"github.com/google/uuid"
)

// PaymentGRPCServer implements the generated PaymentServiceServer interface.
type PaymentGRPCServer struct {
	paymentpb.UnimplementedPaymentServiceServer
	paymentSvc service.PaymentService
	logger     *zap.Logger
}

func NewPaymentGRPCServer(paymentSvc service.PaymentService, logger *zap.Logger) *PaymentGRPCServer {
	return &PaymentGRPCServer{
		paymentSvc: paymentSvc,
		logger:     logger,
	}
}

// GetWalletOwner returns the user ID that owns a given wallet.
func (s *PaymentGRPCServer) GetWalletOwner(
	ctx context.Context,
	req *paymentpb.GetWalletOwnerRequest,
) (*paymentpb.GetWalletOwnerResponse, error) {
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, "wallet_id is required")
	}

	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid wallet_id format")
	}

	wallet, err := s.paymentSvc.GetWallet(ctx, walletID)
	if err != nil {
		s.logger.Error("failed to get wallet",
			zap.String("wallet_id", req.WalletId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to retrieve wallet")
	}
	if wallet == nil {
		return nil, status.Error(codes.NotFound, "wallet not found")
	}

	s.logger.Debug("wallet owner resolved via gRPC",
		zap.String("wallet_id", req.WalletId),
		zap.String("user_id", wallet.UserID.String()),
	)

	return &paymentpb.GetWalletOwnerResponse{
		WalletId: wallet.ID.String(),
		UserId:   wallet.UserID.String(),
	}, nil
}
