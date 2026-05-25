// cmd/server/main.go
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
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/Ad3bay0c/payflow/fraud/internal/config"
	fraudgrpc "github.com/Ad3bay0c/payflow/fraud/internal/grpc"
	"github.com/Ad3bay0c/payflow/fraud/internal/handler"
	"github.com/Ad3bay0c/payflow/fraud/internal/service"
	fraudpb "github.com/Ad3bay0c/payflow/proto/gen/fraud"
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

	logger.Info("starting fraud service",
		zap.String("environment", cfg.Environment),
		zap.Int("http_port", cfg.Port),
		zap.Int("grpc_port", cfg.GRPCPort),
	)

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           1,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	defer redisClient.Close()

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatal("redis connection failed", zap.Error(err))
	}
	logger.Info("connected to redis")

	fraudSvc := service.NewFraudService(redisClient, logger)

	// gRPC server
	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
		}),
		// In production add interceptors here:
		// - logging interceptor (trace_id on every call)
		// - recovery interceptor (panic → gRPC error, not crash)
		// - auth interceptor (mTLS certificate validation)
	)

	fraudpb.RegisterFraudServiceServer(grpcServer, fraudgrpc.NewFraudGRPCServer(fraudSvc, logger))

	// Enable reflection in development — lets tools like grpcurl
	// discover available services without the .proto file
	if cfg.Environment != "production" {
		reflection.Register(grpcServer)
	}

	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		logger.Fatal("failed to bind gRPC port", zap.Error(err))
	}

	go func() {
		logger.Info("fraud gRPC server listening",
			zap.Int("port", cfg.GRPCPort),
		)
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal("gRPC server error", zap.Error(err))
		}
	}()

	// HTTP server health check
	router := buildRouter(cfg)

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("fraud HTTP server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("HTTP server error", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down fraud service...")

	// Stop gRPC gracefully — waits for in-flight RPCs to complete
	grpcServer.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx) //nolint:errcheck

	logger.Info("fraud service stopped")
}

func buildRouter(cfg *config.Config) *gin.Engine {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(handler.TraceID())

	// Health checks only — gRPC handles all business logic
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "fraud"})
	})

	return router
}

func buildLogger(env string) (*zap.Logger, error) {
	if env == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}
