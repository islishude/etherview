//go:build integration

package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourcifyImportConcurrentClaimsAndWake(t *testing.T) {
	db := bytecodeDBPostgres(t)
	address := "0x" + strings.Repeat("11", 20)
	bytecodeDBSeedGenesis(t, db, address)
	block := sha256.Sum256([]byte("genesis"))
	target := VerificationTarget{ChainID: 1, Address: address, AtBlockHash: "0x" + hex.EncodeToString(block[:]), CodeHash: "0x" + hex.EncodeToString(keccak256Bytes([]byte{0x60, 2})), RuntimeBytecode: "0x6002", GenesisPredeploy: true}
	var notifications atomic.Int32
	repository, err := NewPostgresRepository(db, RepositoryOptions{SignalJobs: func() {
		var count int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM verification_jobs`).Scan(&count); err != nil || count == 0 {
			t.Errorf("notification before durable commit: count=%d err=%v", count, err)
		}
		notifications.Add(1)
	}})
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(repository, 5<<20)
	job, created, err := service.SubmitV2(t.Context(), SubmissionV2{Kind: JobSourcifyImport, SourceChainID: 11155111, Target: &target})
	if err != nil || !created || notifications.Load() != 1 {
		t.Fatalf("submit: created=%v err=%v wake=%d", created, err, notifications.Load())
	}
	invalid := target
	invalid.ChainID = 999
	if _, _, err := service.SubmitV2(t.Context(), SubmissionV2{Kind: JobSourcifyImport, SourceChainID: 11155111, Target: &invalid}); err == nil || notifications.Load() != 1 {
		t.Fatal("failed insert emitted notification")
	}
	var wg sync.WaitGroup
	leases := make(chan VerificationLease, 8)
	for i := range 8 {
		wg.Go(func() {
			// Independent repository instances share only PostgreSQL.
			peer, _ := NewPostgresRepository(db, RepositoryOptions{})
			lease, found, err := peer.ClaimRunnable(t.Context(), fmt.Sprintf("instance-%d", i), time.Minute, CompilerAvailability{})
			if err != nil {
				t.Error(err)
				return
			}
			if found {
				leases <- lease
			}
		})
	}
	wg.Wait()
	close(leases)
	if len(leases) != 1 {
		t.Fatalf("claims=%d", len(leases))
	}
	first := <-leases
	if first.Job.ID != job.ID {
		t.Fatal("wrong job")
	}
	if err := repository.Renew(t.Context(), first, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.Claim(t.Context(), "other", time.Minute); err != nil || found {
		t.Fatalf("renewed lease reclaimed: %v %v", found, err)
	}
	bytecodeDBExec(t, db, `UPDATE verification_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, job.ID)
	next, found, err := repository.Claim(t.Context(), "replacement", time.Minute)
	if err != nil || !found || next.Token == first.Token {
		t.Fatalf("takeover: %v %v", found, err)
	}
	if err := repository.Fail(t.Context(), first, ErrorSourcifyUnavailable); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old owner published: %v", err)
	}
	if err := repository.Fail(t.Context(), next, ErrorSourcifyNotFound); err != nil {
		t.Fatal(err)
	}
}
