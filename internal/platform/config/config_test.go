package config_test

import (
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/platform/config"
)

func TestLoad_Success(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fincore?sslmode=disable")
	t.Setenv("JWT_SECRET", "12345678901234567890123456789012")
	t.Setenv("JWT_EXPIRATION", "30m")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("AUTO_MIGRATE", "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.HTTPPort != "9090" || cfg.JWTExpiration != 30*time.Minute || cfg.AutoMigrate {
		t.Fatalf("unexpected config values: %+v", cfg)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "12345678901234567890123456789012")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoad_WeakJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fincore")
	t.Setenv("JWT_SECRET", "too-short")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for weak JWT_SECRET (< 32 chars)")
	}
}
