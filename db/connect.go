package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Open returns a writer and reader *sql.DB pair for the given SQLite file path.
//
// The writer pool is limited to a single connection to serialise all writes and
// prevent SQLITE_BUSY errors under WAL concurrency. The reader pool allows up
// to maxReaderConns concurrent read connections.
//
// Both connections have WAL journal mode, foreign key enforcement, and a
// 5-second busy timeout applied via DSN parameters.
//
// The caller owns both returned DB handles and must Close() them on shutdown.
func Open(path string, maxReaderConns int) (writer *sql.DB, reader *sql.DB, err error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000",
		path,
	)

	writer, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open writer db: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	if err = writer.Ping(); err != nil {
		writer.Close()
		return nil, nil, fmt.Errorf("ping writer db: %w", err)
	}

	roDSN := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&mode=ro",
		path,
	)

	reader, err = sql.Open("sqlite3", roDSN)
	if err != nil {
		writer.Close()
		return nil, nil, fmt.Errorf("open reader db: %w", err)
	}
	if maxReaderConns <= 0 {
		maxReaderConns = 4
	}
	reader.SetMaxOpenConns(maxReaderConns)
	reader.SetMaxIdleConns(maxReaderConns)

	if err = reader.Ping(); err != nil {
		writer.Close()
		reader.Close()
		return nil, nil, fmt.Errorf("ping reader db: %w", err)
	}

	return writer, reader, nil
}
