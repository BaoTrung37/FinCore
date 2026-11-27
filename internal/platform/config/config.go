package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const minJWTSecretLen = 32

// Config holds runtime configuration for the FinCore API.
type Config struct {
	AppEnv                string
	HTTPPort              string
	DatabaseURL           string
	JWTSecret             string
	JWTExpiration         time.Duration
	LogLevel              string
	AutoMigrate           bool
	MockBankWebhookSecret string
	CORSAllowedOrigins    []string
	RateLimitRPS          float64
	RateLimitBurst        int
}

// Load reads configuration from environment variables (and an optional .env file).
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	jwtExp, err := parseDurationEnv("JWT_EXPIRATION", 24*time.Hour)
	if err != nil {
		return nil, err
	}

	autoMigrate, err := parseBoolEnv("AUTO_MIGRATE", true)
	if err != nil {
		return nil, err
	}

	rateLimitRPS, err := parseFloatEnv("RATE_LIMIT_RPS", 100)
	if err != nil {
		return nil, err
	}
	rateLimitBurst, err := parseIntEnv("RATE_LIMIT_BURST", 200)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		AppEnv:                getEnv("APP_ENV", "local"),
		HTTPPort:              getEnv("HTTP_PORT", "8080"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		JWTSecret:             os.Getenv("JWT_SECRET"),
		JWTExpiration:         jwtExp,
		LogLevel:              getEnv("LOG_LEVEL", "info"),
		AutoMigrate:           autoMigrate,
		MockBankWebhookSecret: os.Getenv("MOCK_BANK_WEBHOOK_SECRET"),
		CORSAllowedOrigins:    parseCSVEnv("CORS_ALLOWED_ORIGINS", []string{"*"}),
		RateLimitRPS:          rateLimitRPS,
		RateLimitBurst:        rateLimitBurst,
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < minJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d characters", minJWTSecretLen)
	}
	if cfg.JWTExpiration <= 0 {
		return nil, errors.New("JWT_EXPIRATION must be positive")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return d, nil
}

func parseBoolEnv(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return b, nil
}

func parseFloatEnv(key string, fallback float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return f, nil
}

func parseIntEnv(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return n, nil
}

func parseCSVEnv(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
