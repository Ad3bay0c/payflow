// cmd/server/main.go
//
// Entry point for the PayFlow payment service.
// Wires all dependencies and starts the HTTP server.
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
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	authclient "github.com/Ad3bay0c/payflow/payment/internal/auth"
	"github.com/Ad3bay0c/payflow/payment/internal/config"
	"github.com/Ad3bay0c/payflow/payment/internal/events"
	"github.com/Ad3bay0c/payflow/payment/internal/handler"
	"github.com/Ad3bay0c/payflow/payment/internal/repository"
	"github.com/Ad3bay0c/payflow/payment/internal/service"
)

func main() {
	_ = godotenv.Load()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Logger
	logger, err := buildLogger(cfg.Environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting payment service",
		zap.String("environment", cfg.Environment),
		zap.Int("port", cfg.Port),
	)

	// Database
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

	// Kafka publisher
	// Ensure topics exist before the service starts accepting traffic
	if err := ensureKafkaTopics(cfg.Kafka.Brokers, logger, cfg.Environment); err != nil {
		logger.Warn("failed to ensure kafka topics — continuing",
			zap.Error(err),
		)
	}

	publisher := events.NewKafkaPublisher(cfg.Kafka.Brokers)

	logger.Info("kafka publisher ready",
		zap.Strings("brokers", cfg.Kafka.Brokers),
	)

	validator := authclient.NewTokenValidator(cfg.AuthServiceURL)

	paymentRepo := repository.NewPaymentRepository(pool)

	paymentSvc := service.NewPaymentService(
		paymentRepo,
		publisher,
		logger,
	)

	paymentHandler := handler.NewPaymentHandler(
		paymentSvc,
		validator,
		logger,
	)

	// Build HTTP router
	router := buildRouter(cfg, paymentHandler)

	adminHandler := handler.NewAdminHandler(paymentSvc, logger)

	adminRouter := buildAdminRouter(cfg, adminHandler)

	adminSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.AdminPort),
		Handler:      adminRouter,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("admin server listening",
			zap.String("addr", adminSrv.Addr),
			zap.String("warning", "never expose this port to the public internet"),
		)
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("admin server error", zap.Error(err))
		}
	}()

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("payment service listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down payment service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("forced shutdown", zap.Error(err))
	}

	logger.Info("payment service stopped")
}

func buildRouter(
	cfg *config.Config,
	paymentHandler *handler.PaymentHandler,
) *gin.Engine {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(handler.TraceID())

	// Health checks
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "payment",
		})
	})

	// API routes
	v1 := router.Group("/api/v1")
	paymentHandler.RegisterRoutes(v1, cfg.JWT.PublicKey)

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

// ensureKafkaTopics creates the required Kafka topics if they don't exist.
// This is a convenience for local development — in production topics
// are created via Terraform before the service deploys.
func ensureKafkaTopics(brokers []string, logger *zap.Logger, env string) error {
	if env != "production" {
		logger.Debug("skipping topic creation in non-production environment")
		return nil
	}

	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("connecting to kafka: %w", err)
	}
	defer conn.Close()

	topicConfigs := []kafka.TopicConfig{
		{
			Topic:             events.TopicPaymentCompleted,
			NumPartitions:     3,
			ReplicationFactor: 1, // 1 for local dev, 3 in production
		},
		{
			Topic:             events.TopicPaymentFailed,
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
	}

	err = conn.CreateTopics(topicConfigs...)
	if err != nil && err.Error() != "Topic with this name already exists" {
		return fmt.Errorf("creating topics: %w", err)
	}

	return nil
}

func buildLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}

func buildAdminRouter(
	cfg *config.Config,
	adminHandler *handler.AdminHandler,
) *gin.Engine {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(handler.TraceID())

	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Admin routes — protected by API key
	admin := router.Group("/admin/v1")
	adminHandler.RegisterRoutes(admin)

	return router
}
