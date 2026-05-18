// internal/config/config.go

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Environment       string
	Port              int
	Database          DatabaseConfig
	Kafka             KafkaConfig
	AuthServiceURL    string
	PaymentServiceURL string
	AdminKey          string
	SMS               SMSConfig
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

type KafkaConfig struct {
	Brokers []string
	GroupID string
}

type SMSConfig struct {
	Provider string // "logger" | "termii"
	APIKey   string
	SenderID string
}

func Load() (*Config, error) {
	port, err := getInt("PORT", 8085)
	if err != nil {
		return nil, err
	}
	dbPort, err := getInt("DB_PORT", 5433)
	if err != nil {
		return nil, err
	}
	dbMaxConns, err := getInt("DB_MAX_CONNS", 10)
	if err != nil {
		return nil, err
	}

	brokers := strings.Split(getString("KAFKA_BROKERS", "127.0.0.1:9092"), ",")

	return &Config{
		Environment:       getString("ENVIRONMENT", "development"),
		Port:              port,
		AuthServiceURL:    getString("AUTH_SERVICE_URL", "http://localhost:8081"),
		PaymentServiceURL: getString("PAYMENT_SERVICE_URL", "http://localhost:8083"),
		AdminKey:          getString("ADMIN_API_KEY", ""),
		Database: DatabaseConfig{
			Host:     getString("DB_HOST", "localhost"),
			Port:     dbPort,
			Name:     require("DB_NAME"),
			User:     require("DB_USER"),
			Password: require("DB_PASSWORD"),
			SSLMode:  getString("DB_SSL_MODE", "disable"),
			MaxConns: int32(dbMaxConns),
		},
		Kafka: KafkaConfig{
			Brokers: brokers,
			GroupID: getString("KAFKA_GROUP_ID", "notification-service"),
		},
		SMS: SMSConfig{
			Provider: getString("SMS_PROVIDER", "logger"),
			APIKey:   getString("TERMII_API_KEY", ""),
			SenderID: getString("TERMII_SENDER_ID", "PayFlow"),
		},
	}, nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		c.Database.Host, c.Database.Port, c.Database.Name,
		c.Database.User, c.Database.Password, c.Database.SSLMode,
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
		return 0, fmt.Errorf("env %q must be an integer", key)
	}
	return i, nil
}
