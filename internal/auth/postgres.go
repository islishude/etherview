package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PostgresRepository keeps only keyed digests. The plaintext token is returned
// once by Manager.Create and is never persisted.
type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("API key repository database is nil")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) Put(ctx context.Context, key APIKey) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.Prefix, key.Digest, key.Name, key.Rate, key.Burst, key.CreatedAt.UTC(), key.RevokedAt)
	if err != nil {
		return fmt.Errorf("insert API key: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ByPrefix(ctx context.Context, prefix string) (APIKey, error) {
	var key APIKey
	var revoked sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT prefix, digest, name, rate_per_second, burst, created_at, revoked_at
		FROM api_keys
		WHERE prefix = $1`, prefix).Scan(
		&key.Prefix, &key.Digest, &key.Name, &key.Rate, &key.Burst, &key.CreatedAt, &revoked,
	)
	if err == sql.ErrNoRows {
		return APIKey{}, errors.New("API key not found")
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("query API key: %w", err)
	}
	key.CreatedAt = key.CreatedAt.UTC()
	if revoked.Valid {
		value := revoked.Time.UTC()
		key.RevokedAt = &value
	}
	return key, nil
}

func (r *PostgresRepository) Revoke(ctx context.Context, prefix string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_keys
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE prefix = $1`, prefix, at.UTC())
	if err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked API key count: %w", err)
	}
	if count == 0 {
		return errors.New("API key not found")
	}
	return nil
}

func (r *PostgresRepository) Rotate(ctx context.Context, prefix string, replacement APIKey) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin API key rotation: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var name string
	var rate, burst int
	var revoked sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT name, rate_per_second, burst, revoked_at
		FROM api_keys
		WHERE prefix = $1
		FOR UPDATE`, prefix).Scan(&name, &rate, &burst, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("API key not found")
	}
	if err != nil {
		return fmt.Errorf("lock API key for rotation: %w", err)
	}
	if revoked.Valid {
		return ErrRevokedAPIKey
	}
	if replacement.Name != name || replacement.Rate != rate || replacement.Burst != burst {
		return errors.New("replacement API key policy differs from active key")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL)`,
		replacement.Prefix, replacement.Digest, name, rate, burst, replacement.CreatedAt.UTC(),
	); err != nil {
		return fmt.Errorf("insert replacement API key: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE api_keys
		SET revoked_at = $2
		WHERE prefix = $1 AND revoked_at IS NULL`, prefix, replacement.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("revoke rotated API key: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read rotated API key count: %w", err)
	}
	if count != 1 {
		return ErrRevokedAPIKey
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit API key rotation: %w", err)
	}
	return nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]APIKey, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT prefix, name, rate_per_second, burst, created_at, revoked_at
		FROM api_keys
		ORDER BY created_at, prefix`)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var keys []APIKey
	for rows.Next() {
		var key APIKey
		var revoked sql.NullTime
		if err := rows.Scan(&key.Prefix, &key.Name, &key.Rate, &key.Burst, &key.CreatedAt, &revoked); err != nil {
			return nil, fmt.Errorf("scan API key: %w", err)
		}
		key.CreatedAt = key.CreatedAt.UTC()
		if revoked.Valid {
			value := revoked.Time.UTC()
			key.RevokedAt = &value
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API keys: %w", err)
	}
	return keys, nil
}
