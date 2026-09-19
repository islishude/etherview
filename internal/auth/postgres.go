package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PostgresRepository keeps only keyed digests. The plaintext token is returned
// once by Manager.Create and is never persisted.
type PostgresRepository struct {
	db dbaccess.Database
}

func NewPostgresRepository(db dbaccess.Database) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("API key repository database is nil")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) Put(ctx context.Context, key APIKey) error {
	err := func() error {
		if key.Rate < -2147483648 || key.Rate > 2147483647 {
			return errors.New("invalid stored query value")
		}
		if key.Burst < -2147483648 || key.Burst > 2147483647 {
			return errors.New("invalid stored query value")
		}
		var queryValue2 pgtype.Timestamptz
		if key.RevokedAt != nil {
			queryValue2 = pgtype.Timestamptz{Time: *key.RevokedAt, Valid: true}
		}
		var queryValue3 pgtype.UUID
		if key.OwnerUserID != nil {
			if err := queryValue3.Scan(*key.OwnerUserID); err != nil {
				return err
			}
		}
		return dbgen.New(r.db).AuthWritePutStatement1(ctx, dbgen.AuthWritePutStatement1Params{Prefix: key.Prefix, Digest: key.Digest, Name: key.Name, RatePerSecond: int32(key.Rate), Burst: int32(key.Burst), CreatedAt: pgtype.Timestamptz{Time: key.CreatedAt.UTC(), Valid: true}, RevokedAt: queryValue2, OwnerUserID: queryValue3, Scopes: scopeStrings(key.Scopes)})
	}()
	if err != nil {
		return fmt.Errorf("insert API key: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ByPrefix(ctx context.Context, prefix string) (APIKey, error) {
	row, err := dbgen.New(r.db).AuthLegacyGetAPIKeyByPrefix(ctx, prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, errors.New("API key not found")
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("query API key: %w", err)
	}
	key := APIKey{Prefix: row.Prefix, Digest: row.Digest, Name: row.Name, Rate: int(row.RatePerSecond), Burst: int(row.Burst), ownerActive: row.OwnerActive}
	return decodeAPIKey(key, row.CreatedAt, row.RevokedAt, row.OwnerUserID, row.Scopes)
}

func decodeAPIKey(key APIKey, created, revoked pgtype.Timestamptz, owner pgtype.UUID, scopes []string) (APIKey, error) {
	if !created.Valid || created.InfinityModifier != pgtype.Finite || revoked.Valid && revoked.InfinityModifier != pgtype.Finite {
		return APIKey{}, errors.New("stored API key timestamp is invalid")
	}
	key.CreatedAt = created.Time.UTC()
	if revoked.Valid {
		value := revoked.Time.UTC()
		key.RevokedAt = &value
	}
	if owner.Valid {
		value := uuid.UUID(owner.Bytes).String()
		key.OwnerUserID = &value
	}
	var err error
	key.Scopes, err = scopesFromStrings(scopes)
	if err != nil {
		return APIKey{}, err
	}
	return key, nil
}

func (r *PostgresRepository) Revoke(ctx context.Context, prefix string, at time.Time) error {
	result, err := dbgen.New(r.db).AuthWriteRevokeStatement1(ctx, prefix, pgtype.Timestamptz{Time: at.UTC(), Valid: true})
	if err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	count := result
	if count == 0 {
		return errors.New("API key not found")
	}
	return nil
}

func (r *PostgresRepository) Rotate(ctx context.Context, prefix string, replacement APIKey) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin API key rotation: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if replacement.OwnerUserID != nil {
		ownerID, parseErr := uuid.Parse(*replacement.OwnerUserID)
		if parseErr != nil {
			return errors.New("replacement API key owner is invalid")
		}
		if _, err := dbgen.New(r.db).WithTx(tx).AuthLegacyLockActiveOwner(ctx, pgtype.UUID{Bytes: ownerID, Valid: true}); errors.Is(err, pgx.ErrNoRows) {
			return ErrAPIKeyNotActive
		} else if err != nil {
			return fmt.Errorf("lock API key owner for rotation: %w", err)
		}
	}

	locked, err := dbgen.New(r.db).WithTx(tx).AuthLegacyLockAPIKeyForRotation(ctx, prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("API key not found")
	}
	if err != nil {
		return fmt.Errorf("lock API key for rotation: %w", err)
	}
	if locked.RevokedAt.Valid {
		return ErrRevokedAPIKey
	}
	currentScopes, err := scopesFromStrings(locked.Scopes)
	if err != nil {
		return err
	}
	ownerMatches := locked.OwnerUserID.Valid == (replacement.OwnerUserID != nil)
	if ownerMatches && locked.OwnerUserID.Valid {
		ownerMatches = uuid.UUID(locked.OwnerUserID.Bytes).String() == *replacement.OwnerUserID
	}
	if replacement.Name != locked.Name || replacement.Rate != int(locked.RatePerSecond) || replacement.Burst != int(locked.Burst) ||
		!ownerMatches || !slices.Equal(replacement.Scopes, currentScopes) {
		return errors.New("replacement API key policy differs from active key")
	}
	if err := func() error {
		var queryValue0 pgtype.UUID
		if replacement.OwnerUserID != nil {
			if err := queryValue0.Scan(*replacement.OwnerUserID); err != nil {
				return err
			}
		}
		return dbgen.New(tx).AuthWriteRotateStatement1(ctx, dbgen.AuthWriteRotateStatement1Params{Prefix: replacement.Prefix, Digest: replacement.Digest, Name: locked.Name, RatePerSecond: locked.RatePerSecond, Burst: locked.Burst, CreatedAt: pgtype.Timestamptz{Time: replacement.CreatedAt.UTC(), Valid: true}, OwnerUserID: queryValue0, Scopes: scopeStrings(replacement.Scopes)})
	}(); err != nil {
		return fmt.Errorf("insert replacement API key: %w", err)
	}
	result, err := dbgen.New(tx).AuthWriteRotateStatement2(ctx, prefix, pgtype.Timestamptz{Time: replacement.CreatedAt.UTC(), Valid: true})
	if err != nil {
		return fmt.Errorf("revoke rotated API key: %w", err)
	}
	count := result
	if count != 1 {
		return ErrRevokedAPIKey
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit API key rotation: %w", err)
	}
	return nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]APIKey, error) {
	const pageSize = 512
	var keys []APIKey
	err := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(queries *dbgen.Queries) error {
		cursor := dbgen.AuthLegacyListAPIKeysParams{PageLimit: pageSize, AfterCreatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true}}
		for {
			rows, err := queries.AuthLegacyListAPIKeys(ctx, cursor)
			if err != nil {
				return fmt.Errorf("list API keys: %w", err)
			}
			for _, row := range rows {
				key, err := decodeAPIKey(APIKey{Prefix: row.Prefix, Name: row.Name, Rate: int(row.RatePerSecond), Burst: int(row.Burst)}, row.CreatedAt, row.RevokedAt, row.OwnerUserID, row.Scopes)
				if err != nil {
					return err
				}
				keys = append(keys, key)
			}
			if len(rows) < pageSize {
				return nil
			}
			last := rows[len(rows)-1]
			cursor.HasCursor = true
			cursor.AfterCreatedAt = last.CreatedAt
			cursor.AfterPrefix = last.Prefix
		}
	})
	if err != nil {
		return nil, err
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
