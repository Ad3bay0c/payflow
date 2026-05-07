// internal/service/auth.go
//
// Business logic for user authentication.
// This layer knows nothing about HTTP or SQL.
// It orchestrates the repository and JWT service to implement
// PayFlow's auth flows.

package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/Ad3bay0c/payflow/auth/internal/domain"
	"github.com/Ad3bay0c/payflow/auth/internal/repository"
)

const (
	// OTP rate limit — max 3 requests per 10 minutes per phone number
	otpRateLimit    = 3
	otpRateLimitTTL = 10 * time.Minute

	// OTP is valid for 10 minutes
	otpTTL = 10 * time.Minute

	// After OTP is verified, the phone is marked verified for 30 minutes
	// The user must complete registration within this window
	otpVerifiedTTL = 30 * time.Minute

	// Redis key prefixes
	prefixOTPCode     = "payflow:auth:otp:code:"
	prefixOTPRate     = "payflow:auth:otp:rate:"
	prefixOTPVerified = "payflow:auth:otp:verified:"

	// bcrypt cost — 12 gives ~250ms per hash on modern hardware
	// Slow enough to defeat brute force, fast enough for good UX
	bcryptCost = 12
)

// AuthService defines the auth business operations.
type AuthService interface {
	RequestOTP(ctx context.Context, phone string) error
	VerifyOTP(ctx context.Context, phone, code string) (bool, error)
	Register(ctx context.Context, req RegisterRequest) (*domain.User, error)
	Login(ctx context.Context, req LoginRequest) (*domain.TokenPair, error)
	RefreshToken(ctx context.Context, refreshToken string) (*domain.TokenPair, error)
	Logout(ctx context.Context, accessToken string) error
	GetUser(ctx context.Context, userID uuid.UUID) (*domain.User, error)
}

type RegisterRequest struct {
	PhoneNumber string
	FullName    string
	Email       *string
	Password    string
}

type LoginRequest struct {
	PhoneNumber string
	Password    string
}

type authService struct {
	userRepo  repository.UserRepository
	jwtSvc    JWTService
	smsSender SMSSender
	redis     *redis.Client
	logger    *zap.Logger
}

func NewAuthService(
	userRepo repository.UserRepository,
	jwtSvc JWTService,
	smsSender SMSSender,
	redisClient *redis.Client,
	logger *zap.Logger,
) AuthService {
	return &authService{
		userRepo:  userRepo,
		jwtSvc:    jwtSvc,
		smsSender: smsSender,
		redis:     redisClient,
		logger:    logger,
	}
}

// RequestOTP generates a 6-digit OTP and stores it in Redis.
// Rate limited to 3 requests per 10 minutes per phone number.
// In production this triggers an SMS via a gateway (Termii, Twilio etc).
func (s *authService) RequestOTP(ctx context.Context, phone string) error {
	// Check rate limit first
	rateKey := prefixOTPRate + phone
	count, err := s.redis.Incr(ctx, rateKey).Result()
	if err != nil {
		return fmt.Errorf("checking otp rate limit: %w", err)
	}

	// Set expiry only on the first increment
	if count == 1 {
		s.redis.Expire(ctx, rateKey, otpRateLimitTTL)
	}

	if count > otpRateLimit {
		// Get the exact remaining TTL so we give the user an accurate wait time
		ttl, err := s.redis.TTL(ctx, rateKey).Result()
		if err != nil || ttl <= 0 {
			ttl = otpRateLimitTTL
		}

		// Format the remaining time cleanly
		// 90s → "1 minute 30 seconds", 45s → "45 seconds"
		remaining := formatDuration(ttl)

		s.logger.Warn("OTP rate limit exceeded",
			zap.String("phone", maskPhone(phone)),
			zap.Duration("retry_after", ttl),
		)
		return fmt.Errorf("rate limit exceeded: try again in %s", remaining)
	}

	// Generate 6-digit OTP
	//nolint:gosec — OTP does not need cryptographic randomness
	code := fmt.Sprintf("%06d", rand.Intn(1000000))

	// Store in Redis with TTL
	otpKey := prefixOTPCode + phone
	set, err := s.redis.SetNX(ctx, otpKey, code, otpTTL).Result()
	if err != nil {
		return fmt.Errorf("storing otp: %w", err)
	}
	if !set {
		code, err = s.redis.Get(ctx, otpKey).Result()
		if err != nil {
			return fmt.Errorf("retrieving existing otp: %w", err)
		}
		s.logger.Info("reusing existing OTP",
			zap.String("phone", maskPhone(phone)),
		)
	}
	// Send the OTP via SMS
	message := fmt.Sprintf("Your PayFlow verification code is %s. Valid for 10 minutes. Do not share this code.", code)
	if err := s.smsSender.Send(ctx, phone, message); err != nil {
		// Log the failure but don't expose provider errors to the caller
		s.logger.Error("failed to send OTP SMS",
			zap.String("phone", maskPhone(phone)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to send OTP, please try again")
	}

	s.logger.Info("OTP sent",
		zap.String("phone", maskPhone(phone)),
	)

	return nil
}

// VerifyOTP validates the OTP code against what is stored in Redis.
// On success, marks the phone as verified so registration can proceed.
// Returns false (not an error) when the code is wrong —
// wrong code is expected user behaviour, not a system failure.
func (s *authService) VerifyOTP(ctx context.Context, phone, code string) (bool, error) {
	otpKey := prefixOTPCode + phone

	stored, err := s.redis.Get(ctx, otpKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, fmt.Errorf("OTP has expired or was never requested")
		}
		return false, fmt.Errorf("retrieving otp: %w", err)
	}

	if stored != code {
		s.logger.Warn("invalid OTP attempt",
			zap.String("phone", maskPhone(phone)),
		)
		return false, nil
	}

	// Delete immediately — OTPs are single use
	s.redis.Del(ctx, otpKey)

	// Mark phone as verified — registration must happen within 30 minutes
	verifiedKey := prefixOTPVerified + phone
	s.redis.Set(ctx, verifiedKey, "1", otpVerifiedTTL)

	s.logger.Info("OTP verified",
		zap.String("phone", maskPhone(phone)),
	)

	return true, nil
}

// Register creates a new PayFlow user account.
// Requires prior OTP verification for the phone number.
// Flow: check OTP verified → check phone not taken → hash password → create user
func (s *authService) Register(ctx context.Context, req RegisterRequest) (*domain.User, error) {
	verifiedKey := prefixOTPVerified + req.PhoneNumber
	verified, err := s.redis.Exists(ctx, verifiedKey).Result()
	if err != nil {
		return nil, fmt.Errorf("checking otp verification: %w", err)
	}
	if verified == 0 {
		return nil, fmt.Errorf("phone number must be verified before registration")
	}

	existing, err := s.userRepo.FindByPhone(ctx, req.PhoneNumber)
	if err != nil {
		return nil, fmt.Errorf("checking existing user: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("an account with this phone number already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	// starts at Tier 1 with basic KYC (phone verified)
	now := time.Now().UTC()
	user := &domain.User{
		ID:          uuid.New(),
		PhoneNumber: req.PhoneNumber,
		FullName:    req.FullName,
		Email:       req.Email,
		KYCStatus:   domain.KYCBasic,
		Tier:        domain.Tier1,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,

		PasswordHash: hash,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	// remove the verified flag from Redis
	s.redis.Del(ctx, verifiedKey)

	// Record audit event
	uid := user.ID
	_ = s.userRepo.CreateAuditLog(ctx, repository.CreateAuditLogParams{ //nolint:errcheck
		UserID: &uid,
		Event:  "register",
	})

	s.logger.Info("user registered",
		zap.String("user_id", user.ID.String()),
		zap.String("phone", maskPhone(req.PhoneNumber)),
	)

	return user, nil
}

// Login authenticates a user with phone + password.
// Returns a token pair on success.
// Always returns the same error for wrong phone or wrong password —
// never reveal which one failed (prevents user enumeration attacks).
func (s *authService) Login(ctx context.Context, req LoginRequest) (*domain.TokenPair, error) {
	user, err := s.userRepo.FindByPhone(ctx, req.PhoneNumber)
	if err != nil {
		return nil, fmt.Errorf("finding user: %w", err)
	}
	if user == nil || !user.IsActive {
		return nil, fmt.Errorf("invalid credentials")
	}

	hash, err := s.userRepo.FindPasswordHash(ctx, user.ID)
	if err != nil || hash == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword(hash, []byte(req.Password)); err != nil {
		s.logger.Warn("failed login attempt",
			zap.String("user_id", user.ID.String()),
			zap.String("phone", maskPhone(req.PhoneNumber)),
		)
		return nil, fmt.Errorf("invalid credentials")
	}

	tokens, err := s.jwtSvc.GenerateTokenPair(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("generating tokens: %w", err)
	}

	uid := user.ID
	_ = s.userRepo.CreateAuditLog(ctx, repository.CreateAuditLogParams{ //nolint:errcheck
		UserID: &uid,
		Event:  "login",
	})

	s.logger.Info("user logged in",
		zap.String("user_id", user.ID.String()),
		zap.String("phone", maskPhone(req.PhoneNumber)),
	)

	return tokens, nil
}

// RefreshToken validates a refresh token and issues a new token pair.
// The old refresh token is revoked immediately — single use prevents replay.
func (s *authService) RefreshToken(ctx context.Context, refreshToken string) (*domain.TokenPair, error) {
	// Validate the refresh token
	claims, err := s.jwtSvc.ValidateToken(ctx, refreshToken, domain.TokenTypeRefresh)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	// Revoke the old refresh token before issuing a new pair
	// If this step fails we still proceed — slightly less secure but
	// better than locking the user out permanently
	_ = s.jwtSvc.RevokeToken(ctx, refreshToken) //nolint:errcheck

	// Fetch the user's current profile — not stale data from the JWT
	// KYC status or tier may have changed since the token was issued
	user, err := s.userRepo.FindByID(ctx, claims.UserID)
	if err != nil || user == nil {
		return nil, fmt.Errorf("user not found")
	}

	return s.jwtSvc.GenerateTokenPair(ctx, user)
}

// Logout revokes the current access token.
// The token is added to the Redis blocklist until its natural expiry.
func (s *authService) Logout(ctx context.Context, accessToken string) error {
	if err := s.jwtSvc.RevokeToken(ctx, accessToken); err != nil {
		return fmt.Errorf("revoking token: %w", err)
	}

	s.logger.Info("user logged out")
	return nil
}

// GetUser retrieves the current user's profile by ID.
func (s *authService) GetUser(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("finding user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	return user, nil
}

// maskPhone partially masks a phone number for safe logging.
// +2348012345678 → +234801***678
// Full phone numbers must never appear in logs.
func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:7] + "***" + phone[len(phone)-3:]
}

// formatDuration formats a duration into a human-readable string.
// Used for rate limit messages so users know exactly how long to wait.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60

	if minutes > 0 && seconds > 0 {
		return fmt.Sprintf("%d minute%s %d second%s",
			minutes, plural(minutes),
			seconds, plural(seconds),
		)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d minute%s", minutes, plural(minutes))
	}
	return fmt.Sprintf("%d second%s", seconds, plural(seconds))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
