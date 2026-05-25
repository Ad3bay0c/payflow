// internal/config/config.go

package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Environment   string
	Port          int
	ServiceAPIKey string
	Redis         RedisConfig
}

type RedisConfig struct {
	Addr     string
	Password string
}

func Load() (*Config, error) {
	port, err := getInt("PORT", 8086)
	if err != nil {
		return nil, err
	}

	return &Config{
		Environment:   getString("ENVIRONMENT", "development"),
		Port:          port,
		ServiceAPIKey: getString("SERVICE_API_KEY", ""),
		Redis: RedisConfig{
			Addr:     getString("REDIS_ADDR", "localhost:6379"),
			Password: getString("REDIS_PASSWORD", ""),
		},
	}, nil
}

func (c *Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
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
