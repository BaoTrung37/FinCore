package logger_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/logger"
)

func TestNew_RespectsLevel(t *testing.T) {
	log := logger.New("warn", "production")
	if log.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected info level to be disabled when level=warn")
	}
	if !log.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("expected warn level to be enabled")
	}
}
