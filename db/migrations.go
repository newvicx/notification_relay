package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations applies all pending up-migrations using embedded SQL files.
// It is idempotent and safe to call on every startup.
// Must be called on the writer DB before any application reads or writes begin.
func RunMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrationFS)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
