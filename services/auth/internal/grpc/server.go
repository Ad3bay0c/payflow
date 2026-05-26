// internal/grpc/server.go

package grpc

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Ad3bay0c/payflow/auth/internal/service"
	authpb "github.com/Ad3bay0c/payflow/proto/gen/auth"
	"github.com/google/uuid"
)

// AuthGRPCServer implements the generated AuthServiceServer interface.
type AuthGRPCServer struct {
	authpb.UnimplementedAuthServiceServer
	authSvc service.AuthService
	logger  *zap.Logger
}

func NewAuthGRPCServer(authSvc service.AuthService, logger *zap.Logger) *AuthGRPCServer {
	return &AuthGRPCServer{
		authSvc: authSvc,
		logger:  logger,
	}
}

// GetUserByID returns a user profile by their user ID.
func (s *AuthGRPCServer) GetUserByID(ctx context.Context, req *authpb.GetUserByIDRequest) (*authpb.UserResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id format")
	}

	user, err := s.authSvc.GetUser(ctx, userID)
	if err != nil {
		s.logger.Error("failed to get user",
			zap.String("user_id", req.UserId),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to retrieve user")
	}
	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &authpb.UserResponse{
		UserId:      user.ID.String(),
		PhoneNumber: user.PhoneNumber,
		FullName:    user.FullName,
		KycStatus:   string(user.KYCStatus),
		Tier:        int32(user.Tier),
	}, nil
}

// GetPhoneByUserID returns just the phone number for a user.
func (s *AuthGRPCServer) GetPhoneByUserID(ctx context.Context, req *authpb.GetPhoneByUserIDRequest) (*authpb.PhoneResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user_id format")
	}

	user, err := s.authSvc.GetUser(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve user")
	}
	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	s.logger.Debug("phone lookup via gRPC",
		zap.String("user_id", req.UserId),
	)

	return &authpb.PhoneResponse{
		UserId:      user.ID.String(),
		PhoneNumber: user.PhoneNumber,
	}, nil
}
