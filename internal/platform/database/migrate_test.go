package database_test

import (
	"io/fs"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestMigrationsEmbed_ContainsUsersMigration(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least up and down migration files, got %d", len(entries))
	}
}

func TestMigrateUp_InvalidDSN(t *testing.T) {
	err := database.MigrateUp("not-a-valid-dsn", migrations.FS)
	if err == nil {
		t.Fatal("expected error for invalid DSN")
	}
}
