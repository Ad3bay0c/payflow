// internal/config/config.go

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment    string
	Port           int
	AuthServiceURL string
	Database       DatabaseConfig
	JWT            JWTConfig
	Kafka          KafkaConfig
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

type JWTConfig struct {
	PublicKey []byte // RSA public key PEM — shared from auth service
}

type KafkaConfig struct {
	Brokers []string
	GroupID string
}

func Load() (*Config, error) {
	port, err := getInt("PORT", 8082)
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

	// Load the auth service public key
	// Same key used by auth service to sign tokens
	// Payment service uses it to verify — never to sign
	publicKey, err := loadFile("JWT_PUBLIC_KEY_PATH")
	if err != nil {
		return nil, fmt.Errorf("loading public key: %w", err)
	}

	brokersStr := getString("KAFKA_BROKERS", "localhost:9092")
	brokers := strings.Split(brokersStr, ",")

	return &Config{
		Environment:    getString("ENVIRONMENT", "development"),
		Port:           port,
		AuthServiceURL: getString("AUTH_SERVICE_URL", "http://localhost:8081"),
		Database: DatabaseConfig{
			Host:     getString("DB_HOST", "localhost"),
			Port:     dbPort,
			Name:     require("DB_NAME"),
			User:     require("DB_USER"),
			Password: require("DB_PASSWORD"),
			SSLMode:  getString("DB_SSL_MODE", "disable"),
			MaxConns: int32(dbMaxConns),
		},
		JWT: JWTConfig{
			PublicKey: publicKey,
		},
		Kafka: KafkaConfig{
			Brokers: brokers,
			GroupID: getString("KAFKA_GROUP_ID", "payment-service"),
		},
	}, nil
}

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

func (c *Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

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
		return 0, fmt.Errorf("env %q must be a duration, got %q", key, v)
	}
	return d, nil
}

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
