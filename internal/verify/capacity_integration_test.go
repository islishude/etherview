//go:build integration

package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"strings"
	"sync"
	"testing"
	"time"
)

func capacitySubmission(index int) SubmissionV2 {
	input, _ := json.Marshal(map[string]any{"language": "Solidity", "sources": map[string]any{"A.sol": map[string]string{"content": fmt.Sprintf("contract A {} // %d", index)}}, "settings": map[string]any{}})
	return SubmissionV2{Kind: JobSolidityStandardJSON, Language: LanguageSolidity, CompilerVersion: "0.8.30", StandardJSON: input, Bytecodes: []BytecodePair{{Runtime: "0x6001"}}}
}

func TestVerificationCapacityConcurrentPrincipalsDedupAndRelease(t *testing.T) {
	db := bytecodeDBPostgres(t)
	repo, _ := NewPostgresRepository(db, RepositoryOptions{MaxQueuedJobs: 6, MaxUserJobs: 5})
	service, _ := NewService(repo, 5<<20)
	user := WithAdmissionPrincipal(t.Context(), "user:first")
	first, _, err := service.SubmitV2(user, capacitySubmission(0))
	if err != nil {
		t.Fatal(err)
	}
	other := WithAdmissionPrincipal(t.Context(), "user:second")
	duplicate, created, err := service.SubmitV2(other, capacitySubmission(0))
	if err != nil || created || duplicate.ID != first.ID {
		t.Fatalf("dedup %v %v", created, err)
	}
	var owner string
	if err = db.QueryRow(t.Context(), `SELECT admission_principal FROM verification_jobs WHERE id=$1::uuid`, first.ID).Scan(&owner); err != nil || owner != "user:first" {
		t.Fatalf("owner=%s err=%v", owner, err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for index := 1; index <= 12; index++ {
		wg.Go(func() { _, _, submitErr := service.SubmitV2(user, capacitySubmission(index)); failures <- submitErr })
	}
	wg.Wait()
	close(failures)
	success, limited := 0, 0
	for submitErr := range failures {
		var typed ServiceError
		if submitErr == nil {
			success++
		} else if errors.As(submitErr, &typed) && typed.Code == ServiceUserCapacity {
			limited++
		} else {
			t.Fatal(submitErr)
		}
	}
	if success != 4 || limited != 8 {
		t.Fatalf("success=%d limited=%d", success, limited)
	}
	if _, _, err = service.SubmitV2(other, capacitySubmission(20)); err != nil {
		t.Fatal(err)
	}
	_, _, err = service.SubmitV2(other, capacitySubmission(21))
	var typed ServiceError
	if !errors.As(err, &typed) || typed.Code != ServiceGlobalCapacity {
		t.Fatalf("global=%v", err)
	}
	// A terminal transition releases capacity; takeover and retry keep it occupied once.
	lease, found, err := repo.Claim(t.Context(), "capacity-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%v %v", found, err)
	}
	if err = repo.Fail(t.Context(), lease, ErrorCompileFailed); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SubmitV2(other, capacitySubmission(21)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = db.QueryRow(t.Context(), `SELECT jobs FROM verification_budget`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("count=%d %v", count, err)
	}
	// Retention preserves status/digest/identity and removes only old unreferenced input.
	if _, err = db.Exec(t.Context(), `UPDATE verification_jobs SET updated_at=clock_timestamp()-interval '8 days' WHERE id=$1::uuid`, lease.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = dbgen.New(db).VerifyCleanupInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	summary, found, err := repo.JobSummary(t.Context(), lease.Job.ID)
	if err != nil || !found || summary.Status != JobFailed {
		t.Fatalf("summary=%+v %v", summary, err)
	}
	var removed bool
	if err = db.QueryRow(t.Context(), `SELECT request_payload IS NULL AND octet_length(request_digest)=32 FROM verification_jobs WHERE id=$1::uuid`, lease.Job.ID).Scan(&removed); err != nil || !removed {
		t.Fatalf("retained input %v", err)
	}
}

func TestVerificationLargeCompactInputAndSummary(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		t.Run(fmt.Sprint(multipart), func(t *testing.T) {
			db := bytecodeDBPostgres(t)
			repo, _ := NewPostgresRepository(db, RepositoryOptions{})
			service, _ := NewService(repo, 5<<20)
			size := 3 << 20
			if multipart {
				size = 3 << 19
			}
			source := "contract A {} /*" + strings.Repeat("a", size) + "*/"
			request := capacitySubmission(1)
			request.StandardJSON, _ = json.Marshal(map[string]any{"language": "Solidity", "sources": map[string]any{"A.sol": map[string]string{"content": source}}, "settings": map[string]any{}})
			if multipart {
				request.Kind = JobSolidityMultipart
				request.StandardJSON = nil
				request.Multipart = &MultipartRequest{Language: LanguageSolidity, Sources: map[string]string{"A.sol": source}}
			}
			job, _, err := service.SubmitV2(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			lease, found, err := repo.Claim(t.Context(), "large-input", time.Minute)
			if err != nil || !found {
				t.Fatalf("claim=%v %v", found, err)
			}
			for _, variant := range lease.Job.RequestV2.StandardJSONVariants {
				if !strings.Contains(string(variant), source) {
					t.Fatal("source lost during reconstruction")
				}
			}
			if len(lease.Job.RequestV2.StandardJSONVariants) == 0 {
				t.Fatal("missing compiler variants")
			}
			var metadataBytes, payloadBytes int
			if err = db.QueryRow(t.Context(), `SELECT octet_length(request::text),octet_length(request_payload) FROM verification_jobs WHERE id=$1::uuid`, job.ID).Scan(&metadataBytes, &payloadBytes); err != nil {
				t.Fatal(err)
			}
			if metadataBytes > 4096 || payloadBytes > size+16384 {
				t.Fatalf("source duplicated: metadata=%d payload=%d", metadataBytes, payloadBytes)
			}
			summary, found, err := service.Job(t.Context(), job.ID)
			if err != nil || !found || summary.RequestV2 != nil {
				t.Fatalf("status read exposed input: %v", err)
			}
			rows, err := db.Query(t.Context(), `EXPLAIN (ANALYZE,BUFFERS) SELECT id,kind,status,summary_outcome,error_code,created_at,updated_at,summary_chain_id,summary_address FROM verification_jobs WHERE id=$1::uuid`, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var line string
				if err = rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				t.Log(line)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerificationGlobalByteBudget(t *testing.T) {
	db := bytecodeDBPostgres(t)
	repo, _ := NewPostgresRepository(db, RepositoryOptions{MaxQueuedBytes: 1024})
	service, _ := NewService(repo, 5<<20)
	_, _, err := service.SubmitV2(context.Background(), capacitySubmission(1))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.SubmitV2(context.Background(), capacitySubmission(2))
	var typed ServiceError
	if !errors.As(err, &typed) || typed.Code != ServiceGlobalCapacity {
		t.Fatalf("byte limit=%v", err)
	}
}

func TestVerificationInputRetentionIsBoundedAndPreservesRecentFailures(t *testing.T) {
	db := bytecodeDBPostgres(t)
	repo, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, 5<<20)
	if err != nil {
		t.Fatal(err)
	}
	var recentID string
	for index := range 102 {
		job, _, err := service.SubmitV2(t.Context(), capacitySubmission(index))
		if err != nil {
			t.Fatal(err)
		}
		recentID = job.ID
		lease, found, err := repo.Claim(t.Context(), "retention", time.Minute)
		if err != nil || !found {
			t.Fatalf("claim=%v err=%v", found, err)
		}
		if err := repo.Fail(t.Context(), lease, ErrorCompileFailed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(t.Context(), `UPDATE verification_jobs SET updated_at=clock_timestamp()-interval '8 days' WHERE id<>$1::uuid`, recentID); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int64{100, 1, 0} {
		removed, err := dbgen.New(db).VerifyCleanupInputs(t.Context())
		if err != nil || removed != expected {
			t.Fatalf("removed=%d expected=%d err=%v", removed, expected, err)
		}
	}
	var retained, metadata, active int64
	if err := db.QueryRow(t.Context(), `SELECT count(request_payload),count(*) FILTER(WHERE octet_length(request_digest)=32 AND request IS NOT NULL),(SELECT jobs FROM verification_budget) FROM verification_jobs`).Scan(&retained, &metadata, &active); err != nil || retained != 1 || metadata != 102 || active != 0 {
		t.Fatalf("retained=%d metadata=%d active=%d err=%v", retained, metadata, active, err)
	}
	job, found, err := repo.Job(t.Context(), recentID)
	if err != nil || !found || job.RequestV2 == nil || job.Status != JobFailed {
		t.Fatalf("recent failure removed: found=%v err=%v", found, err)
	}
}
