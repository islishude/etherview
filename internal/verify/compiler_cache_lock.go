package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

const (
	compilerCacheLockPollInterval = 25 * time.Millisecond
	compilerCacheUnlockTimeout    = 5 * time.Second
)

// CompilerCacheInstallLocker serializes the final publication of one
// content-addressed compiler artifact. Implementations must not call action
// unless they own the exact digest's lock for the complete call.
type CompilerCacheInstallLocker interface {
	WithCompilerCacheInstallLock(
		context.Context,
		[sha256.Size]byte,
		func() error,
	) error
}

// PostgresCompilerCacheInstallLocker coordinates replicas that share one
// compiler cache and writer PostgreSQL lock domain. It uses session locks but
// never opens a transaction or holds a connection across artifact download.
type PostgresCompilerCacheInstallLocker struct {
	db           *pgxpool.Pool
	pollInterval time.Duration
}

func NewPostgresCompilerCacheInstallLocker(
	db *pgxpool.Pool,
) (*PostgresCompilerCacheInstallLocker, error) {
	if db == nil {
		return nil, errors.New("compiler cache install locker requires PostgreSQL")
	}
	return &PostgresCompilerCacheInstallLocker{
		db: db, pollInterval: compilerCacheLockPollInterval,
	}, nil
}

func (locker *PostgresCompilerCacheInstallLocker) WithCompilerCacheInstallLock(
	ctx context.Context,
	digest [sha256.Size]byte,
	action func() error,
) (result error) {
	if locker == nil || locker.db == nil || action == nil ||
		digest == [sha256.Size]byte{} {
		return errors.New("compiler cache install lock is invalid")
	}
	key := compilerCacheDigestKey(digest)
	conn, err := locker.acquire(ctx, key)
	if err != nil {
		return errors.New("acquire compiler cache install lock")
	}
	defer func() {
		if err := releaseCompilerCacheInstallLock(conn, key); err != nil {
			result = errors.Join(result, err)
		}
	}()
	return action()
}

func compilerCacheDigestKey(digest [sha256.Size]byte) string {
	return hex.EncodeToString(digest[:])
}

func (locker *PostgresCompilerCacheInstallLocker) acquire(
	ctx context.Context,
	key string,
) (*pgxpool.Conn, error) {
	delay := locker.pollInterval
	if delay <= 0 {
		delay = compilerCacheLockPollInterval
	}
	for {
		conn, err := locker.db.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		var acquired bool
		err = func() error {

			queryRow, err := dbgen.New(conn).VerifyLegacyTryCompilerCacheInstallLock(ctx, key)
			if err != nil {
				return err
			}
			acquired = queryRow
			return nil
		}()
		if err != nil {
			// The server may have granted the session lock before the result was
			// lost. Never return an outcome-uncertain session to the pool.
			discardCompilerCacheLockConnection(conn)
			return nil, err
		}
		if acquired {
			return conn, nil
		}
		conn.Release()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseCompilerCacheInstallLock(conn *pgxpool.Conn, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), compilerCacheUnlockTimeout)
	defer cancel()
	var unlocked bool
	err := func() error {

		queryRow, err := dbgen.New(conn).VerifyLegacyUnlockCompilerCacheInstall(ctx, key)
		if err != nil {
			return err
		}
		unlocked = queryRow
		return nil
	}()
	if err != nil || !unlocked {
		discardCompilerCacheLockConnection(conn)
		return errors.New("release compiler cache install lock")
	}
	conn.Release()
	return nil
}

func discardCompilerCacheLockConnection(conn *pgxpool.Conn) {
	dbaccess.Discard(context.Background(), conn)
}
