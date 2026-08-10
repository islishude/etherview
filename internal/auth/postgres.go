package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
			owner_user_id, scopes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		key.Prefix, key.Digest, key.Name, key.Rate, key.Burst, key.CreatedAt.UTC(),
		key.RevokedAt, key.OwnerUserID, scopeStrings(key.Scopes))
	if err != nil {
		return fmt.Errorf("insert API key: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ByPrefix(ctx context.Context, prefix string) (APIKey, error) {
	var key APIKey
	var revoked sql.NullTime
	var owner pgtype.UUID
	var ownerActive bool
	var scopes []string
	err := r.db.QueryRowContext(ctx, `
		SELECT key.prefix, key.digest, key.name, key.rate_per_second, key.burst,
		       key.created_at, key.revoked_at, key.owner_user_id, key.scopes,
		       COALESCE(owner.status = 'active', TRUE)
		FROM api_keys AS key
		LEFT JOIN users AS owner ON owner.id = key.owner_user_id
		WHERE key.prefix = $1`, prefix).Scan(
		&key.Prefix, &key.Digest, &key.Name, &key.Rate, &key.Burst,
		&key.CreatedAt, &revoked, &owner, &scopes, &ownerActive,
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
	if owner.Valid {
		value := uuid.UUID(owner.Bytes).String()
		key.OwnerUserID = &value
	}
	key.Scopes, err = scopesFromStrings(scopes)
	if err != nil {
		return APIKey{}, err
	}
	key.ownerActive = ownerActive
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
	if replacement.OwnerUserID != nil {
		ownerID, parseErr := uuid.Parse(*replacement.OwnerUserID)
		if parseErr != nil {
			return errors.New("replacement API key owner is invalid")
		}
		var lockedOwner string
		if err := tx.QueryRowContext(ctx, `
			SELECT id::text
			FROM users
			WHERE id = $1 AND status = 'active'
			FOR UPDATE`, ownerID).Scan(&lockedOwner); errors.Is(err, sql.ErrNoRows) {
			return ErrAPIKeyNotActive
		} else if err != nil {
			return fmt.Errorf("lock API key owner for rotation: %w", err)
		}
	}

	var name string
	var rate, burst int
	var revoked sql.NullTime
	var owner pgtype.UUID
	var scopes []string
	err = tx.QueryRowContext(ctx, `
		SELECT name, rate_per_second, burst, revoked_at, owner_user_id, scopes
		FROM api_keys
		WHERE prefix = $1
		FOR UPDATE`, prefix).Scan(&name, &rate, &burst, &revoked, &owner, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("API key not found")
	}
	if err != nil {
		return fmt.Errorf("lock API key for rotation: %w", err)
	}
	if revoked.Valid {
		return ErrRevokedAPIKey
	}
	currentScopes, err := scopesFromStrings(scopes)
	if err != nil {
		return err
	}
	ownerMatches := owner.Valid == (replacement.OwnerUserID != nil)
	if ownerMatches && owner.Valid {
		ownerMatches = uuid.UUID(owner.Bytes).String() == *replacement.OwnerUserID
	}
	if replacement.Name != name || replacement.Rate != rate || replacement.Burst != burst ||
		!ownerMatches || !slices.Equal(replacement.Scopes, currentScopes) {
		return errors.New("replacement API key policy differs from active key")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
			owner_user_id, scopes
		) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, $8)`,
		replacement.Prefix, replacement.Digest, name, rate, burst,
		replacement.CreatedAt.UTC(), replacement.OwnerUserID, scopeStrings(replacement.Scopes),
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
		SELECT prefix, name, rate_per_second, burst, created_at, revoked_at,
		       owner_user_id, scopes
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
		var owner sql.NullString
		var scopes []string
		if err := rows.Scan(
			&key.Prefix, &key.Name, &key.Rate, &key.Burst, &key.CreatedAt,
			&revoked, &owner, &scopes,
		); err != nil {
			return nil, fmt.Errorf("scan API key: %w", err)
		}
		key.CreatedAt = key.CreatedAt.UTC()
		if revoked.Valid {
			value := revoked.Time.UTC()
			key.RevokedAt = &value
		}
		if owner.Valid {
			key.OwnerUserID = &owner.String
		}
		key.Scopes, err = scopesFromStrings(scopes)
		if err != nil {
			return nil, fmt.Errorf("scan API key scopes: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API keys: %w", err)
	}
	return keys, nil
}

func (r *PostgresRepository) PutForUser(
	ctx context.Context,
	userID string,
	key APIKey,
	maximumActive int,
) error {
	identifier, err := apiKeyUserUUID(userID)
	if err != nil || maximumActive < 1 {
		return ErrAPIKeyNotFound
	}
	return dbaccess.WithTransaction(ctx, r.db, func(queries *dbgen.Queries) error {
		if _, err := queries.LockActiveUserForAPIKey(ctx, identifier); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAPIKeyNotFound
			}
			return err
		}
		count, err := queries.CountActiveUserAPIKeys(ctx, identifier)
		if err != nil {
			return err
		}
		if count >= int64(maximumActive) {
			return ErrAPIKeyLimitReached
		}
		return queries.CreateUserAPIKey(ctx, createUserAPIKeyParams(identifier, key))
	})
}

func (r *PostgresRepository) UserKey(
	ctx context.Context,
	userID, prefix string,
) (APIKey, error) {
	identifier, err := apiKeyUserUUID(userID)
	if err != nil || !validPrefix(prefix) {
		return APIKey{}, ErrAPIKeyNotFound
	}
	var row dbgen.ApiKey
	err = dbaccess.WithTransaction(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.LockUserAPIKey(ctx, prefix, identifier)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrAPIKeyNotFound
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("read user API key: %w", err)
	}
	return apiKeyFromRow(row)
}

func (r *PostgresRepository) RotateForUser(
	ctx context.Context,
	userID, prefix string,
	replacement APIKey,
) error {
	identifier, err := apiKeyUserUUID(userID)
	if err != nil || !validPrefix(prefix) {
		return ErrAPIKeyNotFound
	}
	return dbaccess.WithTransaction(ctx, r.db, func(queries *dbgen.Queries) error {
		if _, err := queries.LockActiveUserForAPIKey(ctx, identifier); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAPIKeyNotFound
			}
			return err
		}
		currentRow, err := queries.LockUserAPIKey(ctx, prefix, identifier)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAPIKeyNotFound
		}
		if err != nil {
			return err
		}
		current, err := apiKeyFromRow(currentRow)
		if err != nil {
			return err
		}
		if current.RevokedAt != nil {
			return ErrAPIKeyNotActive
		}
		if current.Name != replacement.Name || current.Rate != replacement.Rate ||
			current.Burst != replacement.Burst ||
			!slices.Equal(current.Scopes, replacement.Scopes) {
			return errors.New("replacement user API key policy differs from active key")
		}
		if err := queries.CreateUserAPIKey(ctx, createUserAPIKeyParams(identifier, replacement)); err != nil {
			return err
		}
		_, err = queries.RevokeUserAPIKey(
			ctx, apiKeyTime(replacement.CreatedAt), prefix, identifier,
		)
		return err
	})
}

func (r *PostgresRepository) RevokeForUser(
	ctx context.Context,
	userID, prefix string,
	revokedAt time.Time,
) (APIKey, error) {
	identifier, err := apiKeyUserUUID(userID)
	if err != nil || !validPrefix(prefix) {
		return APIKey{}, ErrAPIKeyNotFound
	}
	var row dbgen.ApiKey
	err = dbaccess.WithTransaction(ctx, r.db, func(queries *dbgen.Queries) error {
		if _, queryErr := queries.LockActiveUserForAPIKey(ctx, identifier); queryErr != nil {
			if errors.Is(queryErr, pgx.ErrNoRows) {
				return ErrAPIKeyNotFound
			}
			return queryErr
		}
		var queryErr error
		row, queryErr = queries.RevokeUserAPIKey(
			ctx, apiKeyTime(revokedAt), prefix, identifier,
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrAPIKeyNotFound
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("revoke user API key: %w", err)
	}
	return apiKeyFromRow(row)
}

func (r *PostgresRepository) ListForUser(
	ctx context.Context,
	userID string,
	after *UserKeyPageAfter,
	limit int,
) ([]APIKey, int, error) {
	identifier, err := apiKeyUserUUID(userID)
	if err != nil || limit < 1 || limit > 101 {
		return nil, 0, ErrAPIKeyNotFound
	}
	params := dbgen.ListUserAPIKeysPageParams{
		UserID: identifier, PageLimit: int32(limit),
	}
	if after != nil {
		if after.CreatedAt.IsZero() || !validPrefix(after.Prefix) {
			return nil, 0, errors.New("invalid API key page position")
		}
		params.BeforeCreatedAt = apiKeyTime(after.CreatedAt)
		params.BeforePrefix = &after.Prefix
	}
	var rows []dbgen.ListUserAPIKeysPageRow
	var active int64
	err = dbaccess.WithTransaction(ctx, r.db, func(queries *dbgen.Queries) error {
		if _, queryErr := queries.LockActiveUserForAPIKey(ctx, identifier); queryErr != nil {
			if errors.Is(queryErr, pgx.ErrNoRows) {
				return ErrAPIKeyNotFound
			}
			return queryErr
		}
		var queryErr error
		active, queryErr = queries.CountActiveUserAPIKeys(ctx, identifier)
		if queryErr != nil {
			return queryErr
		}
		rows, queryErr = queries.ListUserAPIKeysPage(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list user API keys: %w", err)
	}
	keys := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		key, err := apiKeyFromListRow(row)
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, key)
	}
	return keys, int(active), nil
}

func createUserAPIKeyParams(userID pgtype.UUID, key APIKey) dbgen.CreateUserAPIKeyParams {
	return dbgen.CreateUserAPIKeyParams{
		Prefix: key.Prefix, Digest: key.Digest, Name: key.Name,
		RatePerSecond: int32(key.Rate), Burst: int32(key.Burst),
		CreatedAt: apiKeyTime(key.CreatedAt), UserID: userID,
		Scopes: scopeStrings(key.Scopes),
	}
}

func apiKeyFromRow(row dbgen.ApiKey) (APIKey, error) {
	return apiKeyFromValues(
		row.Prefix, row.Digest, row.Name, row.RatePerSecond, row.Burst,
		row.CreatedAt, row.RevokedAt, row.OwnerUserID, row.Scopes,
	)
}

func apiKeyFromListRow(row dbgen.ListUserAPIKeysPageRow) (APIKey, error) {
	return apiKeyFromValues(
		row.Prefix, nil, row.Name, row.RatePerSecond, row.Burst,
		row.CreatedAt, row.RevokedAt, row.OwnerUserID, row.Scopes,
	)
}

func apiKeyFromValues(
	prefix string,
	digest []byte,
	name string,
	rate, burst int32,
	createdAt, revokedAt pgtype.Timestamptz,
	owner pgtype.UUID,
	scopes []string,
) (APIKey, error) {
	if !createdAt.Valid || !owner.Valid {
		return APIKey{}, errors.New("stored user API key is invalid")
	}
	parsedScopes, err := scopesFromStrings(scopes)
	if err != nil {
		return APIKey{}, err
	}
	ownerValue := uuid.UUID(owner.Bytes).String()
	key := APIKey{
		Prefix: prefix, Digest: digest, Name: name, Rate: int(rate), Burst: int(burst),
		CreatedAt: createdAt.Time.UTC(), OwnerUserID: &ownerValue,
		Scopes: parsedScopes, ownerActive: true,
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		key.RevokedAt = &value
	}
	return key, nil
}

func apiKeyUserUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func apiKeyTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func scopeStrings(scopes []Scope) []string {
	result := make([]string, len(scopes))
	for index, scope := range scopes {
		result[index] = string(scope)
	}
	return result
}

func scopesFromStrings(values []string) ([]Scope, error) {
	scopes := make([]Scope, len(values))
	for index, value := range values {
		scopes[index] = Scope(strings.TrimSpace(value))
	}
	return NormalizeScopes(scopes)
}
