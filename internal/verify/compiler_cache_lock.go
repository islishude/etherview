package verify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"github.com/islishude/etherview/internal/db/gen"
	"time"
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
	db           *sql.DB
	pollInterval time.Duration
}

func NewPostgresCompilerCacheInstallLocker(
	db *sql.DB,
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
) (*sql.Conn, error) {
	delay := locker.pollInterval
	if delay <= 0 {
		delay = compilerCacheLockPollInterval
	}
	for {
		conn, err := locker.db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		var acquired bool
		err = conn.QueryRowContext(ctx, dbgen.VerifyLegacyTryCompilerCacheInstallLock, key).Scan(&acquired)
		if err != nil {
			// The server may have granted the session lock before the result was
			// lost. Never return an outcome-uncertain session to the pool.
			discardCompilerCacheLockConnection(conn)
			_ = conn.Close()
			return nil, err
		}
		if acquired {
			return conn, nil
		}
		if err := conn.Close(); err != nil {
			return nil, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseCompilerCacheInstallLock(conn *sql.Conn, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), compilerCacheUnlockTimeout)
	defer cancel()
	var unlocked bool
	err := conn.QueryRowContext(ctx, dbgen.VerifyLegacyUnlockCompilerCacheInstall, key).Scan(&unlocked)
	if err != nil || !unlocked {
		discardCompilerCacheLockConnection(conn)
		_ = conn.Close()
		return errors.New("release compiler cache install lock")
	}
	if err := conn.Close(); err != nil {
		return errors.New("release compiler cache install lock")
	}
	return nil
}

func discardCompilerCacheLockConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
