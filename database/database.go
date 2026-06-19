package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func OpenDatabase(dbPath string) (*sql.DB, error) {
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite only supports one writer at a time, so set max open connections to 1
	db.SetMaxOpenConns(1)

	if err = db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if err = createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}

func createSchema(db *sql.DB) error {
	ctx := context.Background()
	queries := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			developer_key TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS versions (
			id TEXT PRIMARY KEY,
			product_id TEXT NOT NULL,
			version_string TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT (STRFTIME('%Y-%m-%d %H:%M:%f', 'NOW')),
			FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS file_manifests (
			id TEXT PRIMARY KEY,
			version_id TEXT NOT NULL,
			file_path TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			sha256_hash TEXT NOT NULL,
			FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS patches (
			id TEXT PRIMARY KEY,
			product_id TEXT NOT NULL,
			from_version_id TEXT NOT NULL,
			to_version_id TEXT NOT NULL,
			file_path TEXT NOT NULL,
			patch_size INTEGER NOT NULL,
			patch_sha256 TEXT NOT NULL,
			FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
			FOREIGN KEY (from_version_id) REFERENCES versions(id) ON DELETE CASCADE,
			FOREIGN KEY (to_version_id) REFERENCES versions(id) ON DELETE CASCADE
		);`,
	}

	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	return nil
}
