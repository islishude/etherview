package userauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errConcurrentUserVisibility = errors.New("concurrent user insert is not visible")

type PostgresRepository struct {
	db      *sql.DB
	chainID uint64
	numeric pgtype.Numeric
}

func NewPostgresRepository(db *sql.DB, chainID uint64) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("user authentication repository database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("user authentication repository chain ID is zero")
	}
	return &PostgresRepository{
		db: db, chainID: chainID,
		numeric: pgtype.Numeric{Int: new(big.Int).SetUint64(chainID), Valid: true},
	}, nil
}

func (repository *PostgresRepository) CreateChallenge(
	ctx context.Context,
	challenge Challenge,
) (Challenge, error) {
	if challenge.ChainID != repository.chainID || challenge.ConsumedAt != nil {
		return Challenge{}, ErrChallengeInvalid
	}
	id, err := postgresUUID(challenge.ID)
	if err != nil {
		return Challenge{}, ErrChallengeInvalid
	}
	address, err := addressBytes(challenge.Address)
	if err != nil {
		return Challenge{}, ErrChallengeInvalid
	}
	var row dbgen.AuthChallenge
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.CreateAuthChallenge(ctx, dbgen.CreateAuthChallengeParams{
			ID: id, ChainID: repository.numeric, Address: address,
			Message: challenge.Message, Nonce: challenge.Nonce,
			IssuedAt:  postgresTime(challenge.IssuedAt),
			ExpiresAt: postgresTime(challenge.ExpiresAt),
		})
		return queryErr
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("create authentication challenge: %w", err)
	}
	stored, err := challengeFromRow(row)
	if err != nil || stored.ChainID != repository.chainID {
		return Challenge{}, errStoredStateInvalid
	}
	return stored, nil
}

func (repository *PostgresRepository) Challenge(ctx context.Context, id string) (Challenge, error) {
	identifier, err := postgresUUID(id)
	if err != nil {
		return Challenge{}, ErrChallengeInvalid
	}
	var row dbgen.AuthChallenge
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetAuthChallenge(ctx, identifier)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, ErrChallengeInvalid
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("read authentication challenge: %w", err)
	}
	challenge, err := challengeFromRow(row)
	if err != nil || challenge.ChainID != repository.chainID {
		return Challenge{}, ErrChallengeInvalid
	}
	return challenge, nil
}

func (repository *PostgresRepository) CompleteLogin(
	ctx context.Context,
	challenge Challenge,
	candidateUserID string,
	material SessionMaterial,
) (Session, error) {
	if challenge.ChainID != repository.chainID || challenge.ConsumedAt != nil ||
		!material.ExpiresAt.After(material.CreatedAt) {
		return Session{}, ErrChallengeInvalid
	}
	challengeID, err := postgresUUID(challenge.ID)
	if err != nil {
		return Session{}, ErrChallengeInvalid
	}
	userID, err := postgresUUID(candidateUserID)
	if err != nil {
		return Session{}, fmt.Errorf("%w: candidate user ID", ErrInvalidInput)
	}
	sessionID, err := postgresUUID(material.ID)
	if err != nil {
		return Session{}, fmt.Errorf("%w: session ID", ErrInvalidInput)
	}
	address, err := addressBytes(challenge.Address)
	if err != nil {
		return Session{}, ErrChallengeInvalid
	}

	for attempt := 0; attempt < 2; attempt++ {
		session, disabled, transactionErr := repository.completeLoginOnce(
			ctx, challenge, challengeID, userID, sessionID, address, material,
		)
		if errors.Is(transactionErr, errConcurrentUserVisibility) && attempt == 0 {
			continue
		}
		if transactionErr != nil {
			switch {
			case errors.Is(transactionErr, ErrChallengeConsumed):
				return Session{}, ErrChallengeConsumed
			case errors.Is(transactionErr, ErrChallengeExpired):
				return Session{}, ErrChallengeExpired
			case errors.Is(transactionErr, ErrChallengeInvalid):
				return Session{}, ErrChallengeInvalid
			}
			return Session{}, fmt.Errorf("complete user login: %w", transactionErr)
		}
		if disabled {
			return Session{}, ErrUserDisabled
		}
		return session, nil
	}
	return Session{}, errConcurrentUserVisibility
}

func (repository *PostgresRepository) completeLoginOnce(
	ctx context.Context,
	expected Challenge,
	challengeID, candidateUserID, sessionID pgtype.UUID,
	address []byte,
	material SessionMaterial,
) (Session, bool, error) {
	var (
		session  Session
		disabled bool
	)
	err := dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		consumedRow, err := queries.ConsumeAuthChallenge(
			ctx, postgresTime(material.CreatedAt), challengeID,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			currentRow, currentErr := queries.GetAuthChallenge(ctx, challengeID)
			if currentErr != nil {
				return ErrChallengeInvalid
			}
			current, currentErr := challengeFromRow(currentRow)
			if currentErr != nil {
				return ErrChallengeInvalid
			}
			if current.ConsumedAt != nil {
				return ErrChallengeConsumed
			}
			if !material.CreatedAt.Before(current.ExpiresAt) {
				return ErrChallengeExpired
			}
			return ErrChallengeInvalid
		}
		if err != nil {
			return err
		}
		consumed, err := challengeFromRow(consumedRow)
		if err != nil || consumed.ConsumedAt == nil ||
			!sameChallenge(consumed, expected, false) {
			return ErrChallengeInvalid
		}

		loginRow, err := queries.GetOrCreateUserForLogin(ctx, dbgen.GetOrCreateUserForLoginParams{
			ID: candidateUserID, ChainID: repository.numeric,
			Address: address, CreatedAt: postgresTime(material.CreatedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errConcurrentUserVisibility
		}
		if err != nil {
			return err
		}
		user, err := userFromValues(
			loginRow.ID, loginRow.ChainID, loginRow.Address, loginRow.DisplayName,
			loginRow.Role, loginRow.Status, loginRow.CreatedAt,
			loginRow.UpdatedAt, loginRow.LastLoginAt,
		)
		if err != nil || user.ChainID != repository.chainID ||
			user.Address != expected.Address {
			return errStoredStateInvalid
		}
		if user.Status == StatusDisabled {
			disabled = true
			return nil
		}

		recorded, err := queries.RecordUserLogin(
			ctx, postgresTime(material.CreatedAt), loginRow.ID,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent administrator may have disabled the user after the
			// initial statement. READ COMMITTED gives this statement a fresh
			// snapshot, so confirm the new status and commit the challenge
			// consumption without creating a session.
			current, queryErr := queries.GetUserByID(
				ctx, loginRow.ID, repository.numeric,
			)
			if queryErr != nil {
				return queryErr
			}
			currentUser, queryErr := userFromRow(current)
			if queryErr != nil || currentUser.Status != StatusDisabled {
				return errStoredStateInvalid
			}
			disabled = true
			return nil
		}
		if err != nil {
			return err
		}
		user, err = userFromRow(recorded)
		if err != nil {
			return err
		}
		storedSession, err := queries.CreateUserSession(ctx, dbgen.CreateUserSessionParams{
			ID: sessionID, UserID: recorded.ID,
			TokenDigest: material.TokenDigest[:], CsrfDigest: material.CSRFDigest[:],
			CreatedAt: postgresTime(material.CreatedAt),
			ExpiresAt: postgresTime(material.ExpiresAt),
		})
		if err != nil {
			return err
		}
		session, err = sessionFromRow(storedSession, user)
		if err != nil {
			return err
		}
		if !constantTimeEqual(storedSession.TokenDigest, material.TokenDigest[:]) ||
			!constantTimeEqual(storedSession.CsrfDigest, material.CSRFDigest[:]) {
			return errStoredStateInvalid
		}
		return nil
	})
	return session, disabled, err
}

func (repository *PostgresRepository) ActiveSession(
	ctx context.Context,
	tokenDigest [opaqueValueBytes]byte,
	observedAt time.Time,
	touchBefore time.Time,
) (Session, error) {
	var row dbgen.GetActiveUserSessionRow
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetActiveUserSession(
			ctx, tokenDigest[:], postgresTime(observedAt),
		)
		if queryErr != nil {
			return queryErr
		}
		return queries.TouchActiveUserSession(
			ctx, postgresTime(observedAt), row.SessionID, postgresTime(touchBefore),
		)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("authenticate user session: %w", err)
	}
	user, err := userFromValues(
		row.UserID, row.ChainID, row.Address, row.DisplayName,
		row.Role, row.Status, row.UserCreatedAt, row.UserUpdatedAt, row.LastLoginAt,
	)
	if err != nil || user.ChainID != repository.chainID ||
		len(row.CsrfDigest) != opaqueValueBytes {
		return Session{}, ErrSessionInvalid
	}
	if user.Status == StatusDisabled {
		return Session{}, ErrUserDisabled
	}
	if user.Status != StatusActive {
		return Session{}, ErrSessionInvalid
	}
	session, err := sessionFromValues(
		row.SessionID, user, row.SessionCreatedAt, row.SessionExpiresAt,
		row.LastUsedAt, row.CsrfDigest,
	)
	if err != nil {
		return Session{}, ErrSessionInvalid
	}
	return session, nil
}

func (repository *PostgresRepository) RevokeSession(
	ctx context.Context,
	tokenDigest [opaqueValueBytes]byte,
	revokedAt time.Time,
) (bool, error) {
	var count int64
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		count, queryErr = queries.RevokeUserSessionByDigest(
			ctx, postgresTime(revokedAt), tokenDigest[:],
		)
		return queryErr
	})
	if err != nil {
		return false, fmt.Errorf("revoke user session: %w", err)
	}
	return count != 0, nil
}

func (repository *PostgresRepository) UserByID(ctx context.Context, id string) (User, error) {
	identifier, err := postgresUUID(id)
	if err != nil {
		return User{}, ErrUserNotFound
	}
	var row dbgen.User
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetUserByID(ctx, identifier, repository.numeric)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user by ID: %w", err)
	}
	return repository.checkedUser(row)
}

func (repository *PostgresRepository) UserByAddress(
	ctx context.Context,
	address string,
) (User, error) {
	canonical, err := normalizeAddress(address)
	if err != nil {
		return User{}, ErrUserNotFound
	}
	raw, _ := addressBytes(canonical)
	var row dbgen.User
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetUserByAddress(ctx, repository.numeric, raw)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user by address: %w", err)
	}
	return repository.checkedUser(row)
}

func (repository *PostgresRepository) UpdateDisplayName(
	ctx context.Context,
	userID string,
	displayName *string,
	updatedAt time.Time,
) (User, error) {
	identifier, err := postgresUUID(userID)
	if err != nil {
		return User{}, ErrUserNotFound
	}
	displayName, err = normalizeDisplayName(displayName)
	if err != nil {
		return User{}, err
	}
	var row dbgen.User
	err = dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		if _, queryErr := queries.GetUserByID(ctx, identifier, repository.numeric); queryErr != nil {
			return queryErr
		}
		var queryErr error
		row, queryErr = queries.UpdateCurrentUserDisplayName(
			ctx, displayName, postgresTime(updatedAt), identifier,
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("update current user: %w", err)
	}
	return repository.checkedUser(row)
}

func (repository *PostgresRepository) Users(
	ctx context.Context,
	after *UserPageAfter,
	limit int,
) ([]User, error) {
	// HTTP callers may request 100 visible rows and ask for one additional
	// sentinel row to decide whether to emit next_cursor.
	if limit < 1 || limit > 101 {
		return nil, fmt.Errorf("%w: user page limit must be between 1 and 101", ErrInvalidInput)
	}
	params := dbgen.ListUsersPageParams{
		ChainID: repository.numeric, PageLimit: int32(limit),
	}
	if after != nil {
		id, err := postgresUUID(after.ID)
		if err != nil || after.CreatedAt.IsZero() {
			return nil, fmt.Errorf("%w: invalid user page position", ErrInvalidInput)
		}
		params.BeforeCreatedAt = postgresTime(after.CreatedAt)
		params.BeforeID = id
	}
	var rows []dbgen.User
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListUsersPage(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]User, 0, len(rows))
	for _, row := range rows {
		user, err := repository.checkedUser(row)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (repository *PostgresRepository) UpdateUser(
	ctx context.Context,
	userID string,
	update AdminUserUpdate,
	updatedAt time.Time,
) (AdminUserUpdateResult, error) {
	identifier, err := postgresUUID(userID)
	if err != nil {
		return AdminUserUpdateResult{}, ErrUserNotFound
	}
	if update.Role == nil && update.Status == nil {
		return AdminUserUpdateResult{}, fmt.Errorf("%w: role or status is required", ErrInvalidInput)
	}
	var role, status *string
	if update.Role != nil {
		if !update.Role.valid() {
			return AdminUserUpdateResult{}, fmt.Errorf("%w: invalid user role", ErrInvalidInput)
		}
		value := string(*update.Role)
		role = &value
	}
	if update.Status != nil {
		if !update.Status.valid() {
			return AdminUserUpdateResult{}, fmt.Errorf("%w: invalid user status", ErrInvalidInput)
		}
		value := string(*update.Status)
		status = &value
	}
	var result AdminUserUpdateResult
	err = dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		row, queryErr := queries.UpdateAdminUser(ctx, dbgen.UpdateAdminUserParams{
			Role: role, Status: status, UpdatedAt: postgresTime(updatedAt),
			ID: identifier, ChainID: repository.numeric,
		})
		if queryErr != nil {
			return queryErr
		}
		user, queryErr := userFromRow(row)
		if queryErr != nil {
			return queryErr
		}
		result.User = user
		if update.Status != nil && *update.Status == StatusDisabled {
			count, queryErr := queries.RevokeAllUserSessions(
				ctx, postgresTime(updatedAt), identifier,
			)
			if queryErr != nil {
				return queryErr
			}
			result.RevokedSessions = uint64(count)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserUpdateResult{}, ErrUserNotFound
	}
	if err != nil {
		return AdminUserUpdateResult{}, fmt.Errorf("update user administration state: %w", err)
	}
	if result.User.ChainID != repository.chainID {
		return AdminUserUpdateResult{}, errStoredStateInvalid
	}
	return result, nil
}

func (repository *PostgresRepository) RevokeAllSessions(
	ctx context.Context,
	userID string,
	revokedAt time.Time,
) (uint64, error) {
	identifier, err := postgresUUID(userID)
	if err != nil {
		return 0, ErrUserNotFound
	}
	var count int64
	err = dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		if _, queryErr := queries.GetUserByID(ctx, identifier, repository.numeric); queryErr != nil {
			return queryErr
		}
		var queryErr error
		count, queryErr = queries.RevokeAllUserSessions(
			ctx, postgresTime(revokedAt), identifier,
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("revoke all user sessions: %w", err)
	}
	return uint64(count), nil
}

func (repository *PostgresRepository) Cleanup(
	ctx context.Context,
	expiredBefore time.Time,
	limit int,
) (CleanupResult, error) {
	if limit < 1 || limit > 1000 {
		return CleanupResult{}, fmt.Errorf("%w: cleanup limit must be between 1 and 1000", ErrInvalidInput)
	}
	var result CleanupResult
	err := dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		var err error
		result.Challenges, err = queries.DeleteExpiredAuthChallenges(
			ctx,
			repository.numeric,
			postgresTime(expiredBefore),
			int32(limit),
		)
		if err != nil {
			return err
		}
		result.Sessions, err = queries.DeleteExpiredUserSessions(
			ctx,
			repository.numeric,
			postgresTime(expiredBefore),
			int32(limit),
		)
		return err
	})
	if err != nil {
		return CleanupResult{}, fmt.Errorf("clean user authentication state: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) checkedUser(row dbgen.User) (User, error) {
	user, err := userFromRow(row)
	if err != nil || user.ChainID != repository.chainID {
		return User{}, errStoredStateInvalid
	}
	return user, nil
}

func normalizeDisplayName(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" || utf8.RuneCountInString(normalized) > 64 ||
		len(normalized) > 256 {
		return nil, fmt.Errorf("%w: display name must contain 1 to 64 characters", ErrInvalidInput)
	}
	for _, character := range normalized {
		if unicode.IsControl(character) {
			return nil, fmt.Errorf("%w: display name contains a control character", ErrInvalidInput)
		}
	}
	return &normalized, nil
}

func challengeFromRow(row dbgen.AuthChallenge) (Challenge, error) {
	id, err := uuidString(row.ID)
	if err != nil {
		return Challenge{}, err
	}
	chainID, err := uint64Numeric(row.ChainID)
	if err != nil || len(row.Address) != common.AddressLength {
		return Challenge{}, errStoredStateInvalid
	}
	issuedAt, err := requiredTime(row.IssuedAt)
	if err != nil {
		return Challenge{}, err
	}
	expiresAt, err := requiredTime(row.ExpiresAt)
	if err != nil {
		return Challenge{}, err
	}
	challenge := Challenge{
		ID: id, ChainID: chainID, Address: common.BytesToAddress(row.Address).Hex(),
		Message: row.Message, Nonce: row.Nonce,
		IssuedAt: issuedAt, ExpiresAt: expiresAt,
	}
	if row.ConsumedAt.Valid {
		consumedAt, err := requiredTime(row.ConsumedAt)
		if err != nil {
			return Challenge{}, err
		}
		challenge.ConsumedAt = &consumedAt
	}
	return challenge, nil
}

func userFromRow(row dbgen.User) (User, error) {
	return userFromValues(
		row.ID, row.ChainID, row.Address, row.DisplayName, row.Role, row.Status,
		row.CreatedAt, row.UpdatedAt, row.LastLoginAt,
	)
}

func userFromValues(
	id pgtype.UUID,
	chainID pgtype.Numeric,
	address []byte,
	displayName *string,
	roleValue, statusValue string,
	createdAt, updatedAt, lastLoginAt pgtype.Timestamptz,
) (User, error) {
	identifier, err := uuidString(id)
	if err != nil {
		return User{}, err
	}
	chain, err := uint64Numeric(chainID)
	if err != nil || len(address) != common.AddressLength {
		return User{}, errStoredStateInvalid
	}
	role, status := Role(roleValue), Status(statusValue)
	if !role.valid() || !status.valid() {
		return User{}, errStoredStateInvalid
	}
	created, err := requiredTime(createdAt)
	if err != nil {
		return User{}, err
	}
	updated, err := requiredTime(updatedAt)
	if err != nil {
		return User{}, err
	}
	user := User{
		ID: identifier, ChainID: chain, Address: common.BytesToAddress(address).Hex(),
		DisplayName: displayName, Role: role, Status: status,
		CreatedAt: created, UpdatedAt: updated,
	}
	if lastLoginAt.Valid {
		value, err := requiredTime(lastLoginAt)
		if err != nil {
			return User{}, err
		}
		user.LastLoginAt = &value
	}
	return user, nil
}

func sessionFromRow(row dbgen.UserSession, user User) (Session, error) {
	return sessionFromValues(
		row.ID, user, row.CreatedAt, row.ExpiresAt, row.LastUsedAt, row.CsrfDigest,
	)
}

func sessionFromValues(
	id pgtype.UUID,
	user User,
	createdAt, expiresAt, lastUsedAt pgtype.Timestamptz,
	csrf []byte,
) (Session, error) {
	identifier, err := uuidString(id)
	if err != nil || len(csrf) != opaqueValueBytes {
		return Session{}, errStoredStateInvalid
	}
	created, err := requiredTime(createdAt)
	if err != nil {
		return Session{}, err
	}
	expires, err := requiredTime(expiresAt)
	if err != nil {
		return Session{}, err
	}
	lastUsed, err := requiredTime(lastUsedAt)
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID: identifier, User: user,
		CreatedAt: created, ExpiresAt: expires, LastUsedAt: lastUsed,
	}
	copy(session.csrfDigest[:], csrf)
	return session, nil
}

func addressBytes(address string) ([]byte, error) {
	canonical, err := normalizeAddress(address)
	if err != nil || canonical != address {
		return nil, ErrInvalidInput
	}
	value := common.HexToAddress(canonical)
	return append([]byte(nil), value[:]...), nil
}

func postgresUUID(value string) (pgtype.UUID, error) {
	normalized, err := normalizeUUID(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	parsed, _ := uuid.Parse(normalized)
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}, nil
}

func uuidString(value pgtype.UUID) (string, error) {
	if !value.Valid {
		return "", errStoredStateInvalid
	}
	id := uuid.UUID(value.Bytes)
	if id.Version() != 4 {
		return "", errStoredStateInvalid
	}
	return id.String(), nil
}

func postgresTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func requiredTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return time.Time{}, errStoredStateInvalid
	}
	return value.Time.UTC(), nil
}

func uint64Numeric(value pgtype.Numeric) (uint64, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite ||
		value.Int == nil || value.Exp != 0 || !value.Int.IsUint64() {
		return 0, errStoredStateInvalid
	}
	return value.Int.Uint64(), nil
}
