package testutil

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"notification_relay/db"
)

// OpenDB opens a fresh SQLite database in a temporary file, runs all
// migrations, and returns the connection and a Queries handle. The file is
// automatically deleted when the test ends. A single connection is used
// (MaxOpenConns=1) so no writer/reader split is needed in tests.
func OpenDB(t testing.TB) (*sql.DB, *db.Queries) {
	t.Helper()

	f, err := os.CreateTemp("", "notifytest_*.sqlite")
	if err != nil {
		t.Fatalf("testutil: create temp db file: %v", err)
	}
	f.Close()

	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000",
		f.Name(),
	)

	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		os.Remove(f.Name())
		t.Fatalf("testutil: open sqlite: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	if err := db.RunMigrations(conn); err != nil {
		conn.Close()
		os.Remove(f.Name())
		t.Fatalf("testutil: run migrations: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
		os.Remove(f.Name())
		os.Remove(f.Name() + "-shm")
		os.Remove(f.Name() + "-wal")
	})

	return conn, db.New(conn)
}
