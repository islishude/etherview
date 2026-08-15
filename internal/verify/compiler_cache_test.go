package verify

import (
	"context"
	"crypto/sha256"
	"testing"
)

type immediateCompilerCacheInstallLocker struct{}

func (immediateCompilerCacheInstallLocker) WithCompilerCacheInstallLock(
	_ context.Context,
	_ [sha256.Size]byte,
	action func() error,
) error {
	return action()
}

var testCompilerCacheInstallLocker CompilerCacheInstallLocker = immediateCompilerCacheInstallLocker{}

func TestPostgresCompilerCacheInstallLockerRejectsNilDatabase(t *testing.T) {
	if _, err := NewPostgresCompilerCacheInstallLocker(nil); err == nil ||
		err.Error() != "compiler cache install locker requires PostgreSQL" {
		t.Fatalf("nil database error = %v", err)
	}
}
