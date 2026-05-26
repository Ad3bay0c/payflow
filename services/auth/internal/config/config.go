package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment string
	Port        int
	GRPCPort    int

	Database DatabaseConfig
	Redis    RedisConfig
	JWT      JWTConfig
}

type DatabaseConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string
	MaxConns int32
}

type RedisConfig struct {
	Addr     string
	Password string
}

type JWTConfig struct {
	PrivateKey []byte // raw PEM bytes loaded from file
	PublicKey  []byte // raw PEM bytes loaded from file
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Issuer     string
}

// Load reads all config from environment variables.
func Load() (*Config, error) {
	port, err := getInt("PORT", 8081)
	if err != nil {
		return nil, err
	}

	grpcPort, err := getInt("GRPC_PORT", 9091)
	if err != nil {
		return nil, err
	}

	dbPort, err := getInt("DB_PORT", 5433)
	if err != nil {
		return nil, err
	}

	dbMaxConns, err := getInt("DB_MAX_CONNS", 20)
	if err != nil {
		return nil, err
	}

	accessTTL, err := getDuration("JWT_ACCESS_TTL", 15*time.Minute)
	if err != nil {
		return nil, err
	}

	refreshTTL, err := getDuration("JWT_REFRESH_TTL", 720*time.Hour)
	if err != nil {
		return nil, err
	}

	// Load RSA keys from PEM files
	privateKey, err := loadFile("JWT_PRIVATE_KEY_PATH")
	if err != nil {
		return nil, fmt.Errorf("loading private key: %w", err)
	}

	publicKey, err := loadFile("JWT_PUBLIC_KEY_PATH")
	if err != nil {
		return nil, fmt.Errorf("loading public key: %w", err)
	}

	return &Config{
		Environment: getString("ENVIRONMENT", "development"),
		Port:        port,
		GRPCPort:    grpcPort,
		Database: DatabaseConfig{
			Host:     getString("DB_HOST", "localhost"),
			Port:     dbPort,
			Name:     require("DB_NAME"),
			User:     require("DB_USER"),
			Password: require("DB_PASSWORD"),
			SSLMode:  getString("DB_SSL_MODE", "disable"),
			MaxConns: int32(dbMaxConns),
		},
		Redis: RedisConfig{
			Addr:     getString("REDIS_ADDR", "localhost:6379"),
			Password: getString("REDIS_PASSWORD", ""),
		},
		JWT: JWTConfig{
			PrivateKey: privateKey,
			PublicKey:  publicKey,
			AccessTTL:  accessTTL,
			RefreshTTL: refreshTTL,
			Issuer:     getString("JWT_ISSUER", "payflow"),
		},
	}, nil
}

// DSN returns the PostgreSQL connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		c.Database.Host,
		c.Database.Port,
		c.Database.Name,
		c.Database.User,
		c.Database.Password,
		c.Database.SSLMode,
	)
}

// Addr returns the server listen address.
func (c *Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// require panics if the environment variable is not set.
func require(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("env %q must be an integer, got %q", key, v)
	}
	return i, nil
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("env %q must be a duration (e.g. 15m), got %q", key, v)
	}
	return d, nil
}

// loadFile reads the file path from an env var and returns its contents.
func loadFile(pathEnvKey string) ([]byte, error) {
	path := os.Getenv(pathEnvKey)
	if path == "" {
		return nil, fmt.Errorf("environment variable %q is not set", pathEnvKey)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file at %q: %w", path, err)
	}
	return data, nil
}
