//go:build integration

package verify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresCompilerCacheInstallLockerCoordinatesSessions(t *testing.T) {
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	firstDB := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-first", 2)
	secondDB := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-second", 1)
	thirdDB := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-third", 2)
	first := newCompilerCacheLockTestLocker(t, firstDB)
	second := newCompilerCacheLockTestLocker(t, secondDB)
	third := newCompilerCacheLockTestLocker(t, thirdDB)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	digest := sha256.Sum256([]byte("shared compiler digest"))
	otherDigest := sha256.Sum256([]byte("independent compiler digest"))
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- first.WithCompilerCacheInstallLock(ctx, digest, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	secondEntered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- second.WithCompilerCacheInstallLock(ctx, digest, func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("same digest entered concurrent install section")
	case <-time.After(100 * time.Millisecond):
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, time.Second)
	if err := secondDB.PingContext(pingCtx); err != nil {
		pingCancel()
		t.Fatalf("contended waiter pinned its only pool connection: %v", err)
	}
	pingCancel()

	otherEntered := make(chan struct{})
	otherResult := make(chan error, 1)
	go func() {
		otherResult <- third.WithCompilerCacheInstallLock(ctx, otherDigest, func() error {
			close(otherEntered)
			return nil
		})
	}()
	select {
	case <-otherEntered:
	case <-ctx.Done():
		t.Fatalf("different digest did not acquire independently: %v", ctx.Err())
	}
	if err := <-otherResult; err != nil {
		t.Fatal(err)
	}

	cancelCtx, cancelWait := context.WithTimeout(ctx, 100*time.Millisecond)
	cancelResult := third.WithCompilerCacheInstallLock(cancelCtx, digest, func() error {
		t.Fatal("canceled same-digest waiter entered install section")
		return nil
	})
	cancelWait()
	if cancelResult == nil || cancelResult.Error() != "acquire compiler cache install lock" {
		t.Fatalf("canceled lock result = %v", cancelResult)
	}

	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-ctx.Done():
		t.Fatalf("same digest did not enter after release: %v", ctx.Err())
	}
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCompilerCacheInstallLockReleasesOnSessionDiscard(t *testing.T) {
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	firstDB := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-discard", 1)
	secondDB := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-recover", 1)
	first := newCompilerCacheLockTestLocker(t, firstDB)
	second := newCompilerCacheLockTestLocker(t, secondDB)
	digest := sha256.Sum256([]byte("discarded compiler lock"))
	key := compilerCacheDigestKey(digest)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := first.acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	discardCompilerCacheLockConnection(conn)
	_ = conn.Close()

	entered := false
	if err := second.WithCompilerCacheInstallLock(ctx, digest, func() error {
		entered = true
		return nil
	}); err != nil || !entered {
		t.Fatalf("reacquire after discarded session entered=%t error=%v", entered, err)
	}

	unlockedConn, err := firstDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseCompilerCacheInstallLock(unlockedConn, key); err == nil ||
		err.Error() != "release compiler cache install lock" {
		t.Fatalf("release unowned lock error = %v", err)
	}
	if err := firstDB.PingContext(ctx); err != nil {
		t.Fatalf("pool unusable after discarding unowned lock connection: %v", err)
	}
}

func TestPostgresCompilerCacheInstallLockAcquisitionFailure(t *testing.T) {
	databaseURL := os.Getenv("ETHERVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ETHERVIEW_TEST_DATABASE_URL is not configured")
	}
	database := openCompilerCacheLockTestDatabase(t, databaseURL, "compiler-lock-closed", 1)
	locker := newCompilerCacheLockTestLocker(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	err := locker.WithCompilerCacheInstallLock(
		t.Context(),
		sha256.Sum256([]byte("closed database")),
		func() error {
			called = true
			return nil
		},
	)
	if err == nil || err.Error() != "acquire compiler cache install lock" || called {
		t.Fatalf("closed database acquisition error=%v called=%t", err, called)
	}
}

func openCompilerCacheLockTestDatabase(
	t *testing.T,
	databaseURL string,
	applicationName string,
	maximumConnections int,
) *sql.DB {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["application_name"] = applicationName
	database := stdlib.OpenDB(*config)
	database.SetMaxOpenConns(maximumConnections)
	database.SetMaxIdleConns(maximumConnections)
	t.Cleanup(func() { _ = database.Close() })
	if err := database.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return database
}

func newCompilerCacheLockTestLocker(
	t *testing.T,
	database *sql.DB,
) *PostgresCompilerCacheInstallLocker {
	t.Helper()
	locker, err := NewPostgresCompilerCacheInstallLocker(database)
	if err != nil {
		t.Fatal(err)
	}
	locker.pollInterval = 5 * time.Millisecond
	return locker
}
