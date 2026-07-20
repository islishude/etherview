package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Migration struct {
	Version  string
	SQL      string
	Checksum string
}

func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		contents, err := fs.ReadFile(migrationFS, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(contents)
		migrations = append(migrations, Migration{
			Version:  strings.TrimSuffix(entry.Name(), ".sql"),
			SQL:      string(contents),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(migrations, func(left, right int) bool { return migrations[left].Version < migrations[right].Version })
	return migrations, nil
}

// RunMigrations applies embedded migrations under a PostgreSQL transaction
// advisory lock. An already-applied migration whose bytes changed is rejected.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("run migrations: nil database")
	}
	migrations, err := Migrations()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('etherview:migrations'))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS etherview_schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	for _, migration := range migrations {
		var existingChecksum string
		err := tx.QueryRowContext(ctx,
			`SELECT checksum FROM etherview_schema_migrations WHERE version = $1`,
			migration.Version,
		).Scan(&existingChecksum)
		switch {
		case err == nil:
			if existingChecksum != migration.Checksum {
				return fmt.Errorf("migration %s checksum changed after application", migration.Version)
			}
			continue
		case err != sql.ErrNoRows:
			return fmt.Errorf("read migration %s state: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO etherview_schema_migrations (version, checksum) VALUES ($1, $2)`,
			migration.Version, migration.Checksum,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", migration.Version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
