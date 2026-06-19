package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func runMigrations(db *sql.DB) error {
	ctx := context.Background()

	// Ensure migrations table exists
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		var version int
		if _, err := fmt.Sscanf(file, "%d_", &version); err != nil {
			continue
		}

		var applied bool
		err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", version).Scan(&applied)
		if err != nil {
			return err
		}

		if !applied {
			content, err := migrationFiles.ReadFile("migrations/" + file)
			if err != nil {
				return fmt.Errorf("failed to read migration %s: %w", file, err)
			}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx, string(content)); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to execute migration %s: %w", file, err)
			}

			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to record migration %s: %w", file, err)
			}

			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}

	return nil
}
