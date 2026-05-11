// cmd/server/main.go
//
// Entry point for the PayFlow ledger service.
// Unlike other services, this one has no public HTTP API —
// it is purely event-driven, consuming from Kafka.
// It exposes only health check endpoints for Kubernetes probes.
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

	"github.com/Ad3bay0c/payflow/ledger/internal/config"
	"github.com/Ad3bay0c/payflow/ledger/internal/consumer"
	"github.com/Ad3bay0c/payflow/ledger/internal/repository"
	"github.com/Ad3bay0c/payflow/ledger/internal/service"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := buildLogger(cfg.Environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting ledger service",
		zap.String("environment", cfg.Environment),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := buildDBPool(ctx, cfg)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	logger.Info("connected to database")

	ledgerRepo := repository.NewLedgerRepository(pool)
	ledgerSvc := service.NewLedgerService(ledgerRepo, logger)

	paymentConsumer := consumer.NewPaymentConsumer(
		cfg.Kafka.Brokers,
		cfg.Kafka.GroupID,
		ledgerSvc,
		logger,
	)
	defer paymentConsumer.Close()

	// The ledger service has no public API — only health endpoints
	// so Kubernetes knows when it is ready to receive traffic.
	router := buildRouter(cfg.Environment)
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		logger.Info("health server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("health server error", zap.Error(err))
		}
	}()

	// Run in a goroutine — blocks until context is cancelled
	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- paymentConsumer.Start(ctx)
	}()

	logger.Info("ledger service ready — consuming payment events")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logger.Info("shutdown signal received")
	case err := <-consumerErr:
		if err != nil {
			logger.Error("consumer stopped unexpectedly", zap.Error(err))
		}
	}

	logger.Info("shutting down ledger service...")
	cancel() // stop the consumer

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("health server shutdown error", zap.Error(err))
	}

	logger.Info("ledger service stopped")
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
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "ledger",
		})
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

func buildLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}
