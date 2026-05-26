// cmd/server/main.go
//
// Entry point for the PayFlow auth service.
// Responsibilities:
//   1. Load configuration
//   2. Connect to infrastructure (PostgreSQL, Redis)
//   3. Wire dependencies (repository → service → handler)
//   4. Start the HTTP server
//   5. Handle graceful shutdown

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/Ad3bay0c/payflow/auth/internal/config"
	authgrpc "github.com/Ad3bay0c/payflow/auth/internal/grpc"
	"github.com/Ad3bay0c/payflow/auth/internal/handler"
	"github.com/Ad3bay0c/payflow/auth/internal/repository"
	"github.com/Ad3bay0c/payflow/auth/internal/service"
	authpb "github.com/Ad3bay0c/payflow/proto/gen/auth"
)

func main() {

	_ = godotenv.Load()

	// Load and validate configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := buildLogger(cfg.Environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting auth service",
		zap.String("environment", cfg.Environment),
		zap.Int("port", cfg.Port),
	)

	// Connect through PgBouncer (port 5433) not directly to PostgreSQL.
	// PgBouncer multiplexes our connections — this is the production pattern.
	ctx := context.Background()

	pool, err := buildDBPool(ctx, cfg)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	logger.Info("connected to database",
		zap.String("host", cfg.Database.Host),
		zap.Int("port", cfg.Database.Port),
	)

	// Connect to Redis
	redisClient, err := buildRedisClient(cfg)
	if err != nil {
		logger.Fatal("failed to connect to redis", zap.Error(err))
	}
	defer redisClient.Close()

	logger.Info("connected to redis",
		zap.String("addr", cfg.Redis.Addr),
	)

	// wire dependencies
	userRepo := repository.NewUserRepository(pool)

	jwtSvc, err := service.NewJWTService(
		cfg.JWT.PrivateKey,
		cfg.JWT.PublicKey,
		cfg.JWT.Issuer,
		cfg.JWT.AccessTTL,
		cfg.JWT.RefreshTTL,
		redisClient,
	)
	if err != nil {
		logger.Fatal("failed to initialise jwt service", zap.Error(err))
	}

	// Use logger SMS sender in development, Termii in production
	var smsSender service.SMSSender
	if cfg.Environment == "production" {
		// TODO: plug in actual sms gateway
		//smsSender = service.NewLoggerSMSSender(
		//	os.Getenv("TERMII_API_KEY"),
		//	os.Getenv("TERMII_SENDER_ID"),
		//)
	} else {
		smsSender = service.NewLoggerSMSSender(logger)
	}

	authSvc := service.NewAuthService(
		userRepo,
		jwtSvc,
		smsSender,
		redisClient,
		logger,
	)

	authHandler := handler.NewAuthHandler(authSvc, jwtSvc, logger)

	router := buildRouter(cfg.Environment, authHandler)

	// Start the server with graceful shutdown
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine so it doesn't block the shutdown logic
	go func() {
		logger.Info("auth service listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
		}),
	)
	authpb.RegisterAuthServiceServer(grpcServer, authgrpc.NewAuthGRPCServer(authSvc, logger))

	// Enable reflection in development for tools like grpcurl detect available services
	if cfg.Environment != "production" {
		reflection.Register(grpcServer)
	}

	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		logger.Fatal("failed to bind gRPC port", zap.Error(err))
	}

	go func() {
		logger.Info("auth gRPC server listening",
			zap.Int("port", cfg.GRPCPort),
		)
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal("gRPC server error", zap.Error(err))
		}
	}()

	// Block until we receive a shutdown signal (CTRL+C or Kubernetes SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down auth service...")

	// Stop gRPC gracefully — waits for in-flight RPCs to complete
	grpcServer.GracefulStop()

	// Give in-flight requests 30 seconds to complete before forcing shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("forced shutdown", zap.Error(err))
	}

	logger.Info("auth service stopped")
}

// buildRouter creates and configures the Gin HTTP router.
func buildRouter(
	env string,
	authHandler *handler.AuthHandler,
) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Global middleware
	router.Use(gin.Recovery())    // recover from panics
	router.Use(handler.TraceID()) // inject trace_id on every request

	// Health check endpoints
	// /health/live  — is the process running? (liveness probe)
	// /health/ready — is the service ready to serve traffic? (readiness probe)
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "auth",
		})
	})

	// API v1 routes
	v1 := router.Group("/api/v1/auth")
	authHandler.RegisterRoutes(v1)

	//internal := router.Group("/internal")
	//authHandler.RegisterInternalRoutes(internal)

	return router
}

// buildDBPool creates a pgxpool connection pool.
func buildDBPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing dsn: %w", err)
	}

	poolCfg.MaxConns = cfg.Database.MaxConns
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// buildRedisClient creates and verifies a Redis connection.
func buildRedisClient(cfg *config.Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           0,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("pinging redis: %w", err)
	}

	return client, nil
}

// buildLogger creates a zap logger appropriate for the environment.
func buildLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}
