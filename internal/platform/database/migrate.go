package database

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// MigrateUp applies all pending up migrations from migrationsFS using the pgx/v5 driver.
func MigrateUp(connString string, migrationsFS fs.FS) error {
	if migrationsFS == nil {
		return errors.New("migrations filesystem is required")
	}

	parsed, err := url.Parse(connString)
	if err != nil {
		return errors.New("invalid database url")
	}
	if parsed.Scheme == "" {
		return errors.New("invalid database url: missing scheme")
	}
	if parsed.Scheme == "postgres" || parsed.Scheme == "postgresql" {
		parsed.Scheme = "pgx5"
	}

	src, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, parsed.String())
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
