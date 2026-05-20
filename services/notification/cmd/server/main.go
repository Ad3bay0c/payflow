// cmd/server/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/Ad3bay0c/payflow/notification/internal/config"
	"github.com/Ad3bay0c/payflow/notification/internal/consumer"
	"github.com/Ad3bay0c/payflow/notification/internal/lookup"
	"github.com/Ad3bay0c/payflow/notification/internal/processor"
	"github.com/Ad3bay0c/payflow/notification/internal/provider"
	"github.com/Ad3bay0c/payflow/notification/internal/repository"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger, err := buildLogger(cfg.Environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting notification service",
		zap.String("environment", cfg.Environment),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	pool, err := buildDBPool(ctx, cfg)
	if err != nil {
		logger.Fatal("database connection failed", zap.Error(err))
	}
	defer pool.Close()

	// Providers
	var smsSender provider.SMSProvider
	if cfg.Environment == "production" && cfg.SMS.APIKey != "" {
		smsSender = provider.NewTermiiSMSProvider(cfg.SMS.APIKey, cfg.SMS.SenderID)
		logger.Info("using Termii SMS provider")
	} else {
		smsSender = provider.NewLoggerSMSProvider(logger)
		logger.Info("using logger SMS provider (development)")
	}

	userLookup := lookup.NewAuthServiceLookup(
		cfg.AuthServiceURL,
		cfg.PaymentServiceURL,
		cfg.AdminKey,
	)

	notifRepo := repository.NewNotificationRepository(pool)

	// Consumer — writes pending records only
	paymentConsumer := consumer.NewPaymentConsumer(
		cfg.Kafka.Brokers,
		cfg.Kafka.GroupID,
		notifRepo,
		logger,
	)
	defer paymentConsumer.Close()

	// Processor — delivers notifications from DB
	notifProcessor := processor.NewNotificationProcessor(
		notifRepo,
		smsSender,
		userLookup,
		logger,
	)

	// Health server
	router := buildRouter(cfg.Environment)
	srv := &http.Server{
		Addr:        cfg.Addr(),
		Handler:     router,
		ReadTimeout: 15 * time.Second,
	}

	go func() {
		logger.Info("health server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("health server error", zap.Error(err))
		}
	}()

	// Start Kafka consumer
	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- paymentConsumer.Start(ctx)
	}()

	go func() { notifProcessor.Start(ctx) }()

	logger.Info("notification service ready — consuming payment events")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logger.Info("shutdown signal received")
	case err := <-consumerErr:
		if err != nil {
			logger.Error("consumer error", zap.Error(err))
		}
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx) //nolint:errcheck

	logger.Info("notification service stopped")
}

func buildRouter(env string) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "notification"})
	})
	return router
}

func buildDBPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing dsn: %w", err)
	}
	poolCfg.MaxConns = cfg.Database.MaxConns
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute

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

func buildLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}
