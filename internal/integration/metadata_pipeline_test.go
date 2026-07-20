//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/store"
)

func TestPostgresMetadataPipelineIsDurableAuditedAndCanonicalBound(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	genesis := testBundle(0, testHash(900), testHash(0), testHash(9_000), "metadata-genesis")
	commitCanonical(t, ctx, core, genesis)
	token := testAddress(901)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, 'erc721', 'high', 'pending', 0, $3)`,
		mustBytes(t, token), mustBytes(t, testHash(902)), mustBytes(t, testHash(900)),
	); err != nil {
		t.Fatalf("insert canonical NFT contract: %v", err)
	}
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatalf("create metadata repository: %v", err)
	}
	request := metadata.NFTRequest{
		ChainID: "1", Token: token, TokenID: "42", BlockNumber: 0,
		BlockHash: testHash(900), SourceURI: "https://metadata.example.invalid/42.json",
		MaxAttempts: 3,
	}
	first, err := repository.EnqueueNFT(ctx, request)
	if err != nil || !first.Created || first.JobID <= 0 {
		t.Fatalf("first enqueue = %+v, err=%v", first, err)
	}
	duplicate, err := repository.EnqueueNFT(ctx, request)
	if err != nil || duplicate.Created || duplicate.JobID != first.JobID {
		t.Fatalf("duplicate enqueue = %+v, err=%v, first=%+v", duplicate, err, first)
	}

	lease, found, err := repository.Claim(ctx, "metadata-integration-1", time.Minute)
	if err != nil || !found || lease.JobID != first.JobID || lease.Attempt != 1 || lease.MaxAttempts != 3 {
		t.Fatalf("first claim = %+v, found=%t, err=%v", lease, found, err)
	}
	current, err := repository.Current(ctx, lease)
	if err != nil || !current.Resource || !current.Canonical {
		t.Fatalf("current source = %+v, err=%v", current, err)
	}
	if err := repository.Retry(ctx, lease, "temporary_fetch_error", "temporary upstream failure", 0); err != nil {
		t.Fatalf("retry metadata: %v", err)
	}
	assertMetadataState(t, ctx, db, request, metadataState{State: "pending", Attempts: 1, ErrorCode: "temporary_fetch_error"})
	assertMetadataJob(t, ctx, db, first.JobID, "queued", 1)
	assertMetadataAttemptCount(t, ctx, db, first.JobID, 1)

	lease, found, err = repository.Claim(ctx, "metadata-integration-2", time.Minute)
	if err != nil || !found || lease.JobID != first.JobID || lease.Attempt != 2 {
		t.Fatalf("second claim = %+v, found=%t, err=%v", lease, found, err)
	}
	document := json.RawMessage(`{"name":"Integration NFT","image":"ipfs://bafybeigdyrzt1234567890/42.png"}`)
	digest := sha256.Sum256(document)
	if err := repository.Finish(ctx, lease, metadata.Outcome{
		State: metadata.StateAvailable, ResolvedURI: request.SourceURI,
		MediaType: "application/json", Document: document,
		ContentHash: digest, ContentSize: int64(len(document)),
	}); err != nil {
		t.Fatalf("finish available metadata: %v", err)
	}
	assertMetadataState(t, ctx, db, request, metadataState{State: "available", Attempts: 2, ContentSize: sql.NullInt64{Int64: int64(len(document)), Valid: true}})
	assertMetadataJob(t, ctx, db, first.JobID, "succeeded", 2)
	assertMetadataAttemptCount(t, ctx, db, first.JobID, 2)
	if err := repository.Renew(ctx, lease, time.Minute); !errors.Is(err, metadata.ErrLeaseLost) {
		t.Fatalf("renew completed lease error = %v, want ErrLeaseLost", err)
	}

	exhaustedRequest := request
	exhaustedRequest.TokenID = "43"
	exhaustedRequest.SourceURI = "https://metadata.example.invalid/43.json"
	exhaustedRequest.MaxAttempts = 1
	exhausted, err := repository.EnqueueNFT(ctx, exhaustedRequest)
	if err != nil || !exhausted.Created {
		t.Fatalf("enqueue exhaustion fixture = %+v, err=%v", exhausted, err)
	}
	exhaustedLease, found, err := repository.Claim(ctx, "metadata-integration-crash", time.Minute)
	if err != nil || !found || exhaustedLease.JobID != exhausted.JobID || exhaustedLease.Attempt != 1 {
		t.Fatalf("claim exhaustion fixture = %+v, found=%t, err=%v", exhaustedLease, found, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE durable_jobs SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1`, exhausted.JobID); err != nil {
		t.Fatalf("expire crashed metadata lease: %v", err)
	}
	if next, found, err := repository.Claim(ctx, "metadata-integration-reaper", time.Minute); err != nil || found {
		t.Fatalf("claim after exhaustion = %+v, found=%t, err=%v", next, found, err)
	}
	assertMetadataState(t, ctx, db, exhaustedRequest, metadataState{State: "error", Attempts: 1, ErrorCode: "attempts_exhausted"})
	assertMetadataJob(t, ctx, db, exhausted.JobID, "failed", 1)
	assertMetadataAttemptCount(t, ctx, db, exhausted.JobID, 1)

	orphanRequest := request
	orphanRequest.SourceURI = "https://metadata.example.invalid/42-v2.json"
	orphan, err := repository.EnqueueNFT(ctx, orphanRequest)
	if err != nil || !orphan.Created || orphan.JobID == first.JobID {
		t.Fatalf("enqueue changed source = %+v, err=%v", orphan, err)
	}
	orphanLease, found, err := repository.Claim(ctx, "metadata-integration-3", time.Minute)
	if err != nil || !found || orphanLease.JobID != orphan.JobID {
		t.Fatalf("claim changed source = %+v, found=%t, err=%v", orphanLease, found, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatalf("detach metadata source block: %v", err)
	}
	if err := repository.Finish(ctx, orphanLease, metadata.Outcome{
		State: metadata.StateAvailable, ResolvedURI: orphanRequest.SourceURI,
		MediaType: "application/json", Document: document,
		ContentHash: digest, ContentSize: int64(len(document)),
	}); err != nil {
		t.Fatalf("finish orphaned metadata source: %v", err)
	}
	assertMetadataState(t, ctx, db, orphanRequest, metadataState{State: "unavailable", Attempts: 1, ErrorCode: "source_block_noncanonical"})
	assertMetadataJob(t, ctx, db, orphan.JobID, "succeeded", 1)
}

func TestPostgresNFTMediaSourceRequiresCurrentCanonicalAvailableDocument(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create core repository: %v", err)
	}
	blockHash := testHash(910)
	commitCanonical(t, ctx, core, testBundle(0, blockHash, testHash(0), testHash(9_100), "media-genesis"))
	address := testAddress(911)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			token_address, token_id, observed_block_number, observed_block_hash
		) VALUES (1, 'nft', 'media:42', 'https://metadata.example.invalid/42.json',
			'available', '{"image":"https://media.example.invalid/42.png"}'::jsonb,
			$1, 42, 0, $2)`, mustBytes(t, address), mustBytes(t, blockHash)); err != nil {
		t.Fatalf("insert canonical NFT metadata: %v", err)
	}
	source, err := metadata.NewPostgresImageSource(db, "1")
	if err != nil {
		t.Fatalf("create PostgreSQL media source: %v", err)
	}
	uri, err := source.NFTImageURI(ctx, address, "42")
	if err != nil || uri != "https://media.example.invalid/42.png" {
		t.Fatalf("canonical image URI = %q, err=%v", uri, err)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE external_metadata SET document = '{"name":"No image"}'::jsonb
		WHERE chain_id = 1 AND resource_kind = 'nft' AND token_address = $1 AND token_id = 42`,
		mustBytes(t, address)); err != nil {
		t.Fatalf("remove canonical image field: %v", err)
	}
	if _, err := source.NFTImageURI(ctx, address, "42"); !errors.Is(err, metadata.ErrMediaImageNotFound) {
		t.Fatalf("missing image error = %v, want ErrMediaImageNotFound", err)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE external_metadata SET document = '{"image":"https://media.example.invalid/42.png"}'::jsonb
		WHERE chain_id = 1 AND resource_kind = 'nft' AND token_address = $1 AND token_id = 42`,
		mustBytes(t, address)); err != nil {
		t.Fatalf("restore canonical image field: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatalf("orphan metadata observation: %v", err)
	}
	if _, err := source.NFTImageURI(ctx, address, "42"); !errors.Is(err, metadata.ErrMediaSourceNotFound) {
		t.Fatalf("orphan image error = %v, want ErrMediaSourceNotFound", err)
	}
}

type metadataState struct {
	State       string
	Attempts    int
	ErrorCode   string
	ContentSize sql.NullInt64
}

func assertMetadataState(t *testing.T, ctx context.Context, db *sql.DB, request metadata.NFTRequest, want metadataState) {
	t.Helper()
	var got metadataState
	var errorCode sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT state, attempt_count, last_error_code, content_size
		FROM external_metadata
		WHERE chain_id = $1::numeric AND resource_kind = 'nft'
		  AND token_address = $2 AND token_id = $3::numeric`,
		request.ChainID, mustBytes(t, request.Token), request.TokenID,
	).Scan(&got.State, &got.Attempts, &errorCode, &got.ContentSize); err != nil {
		t.Fatalf("read metadata state: %v", err)
	}
	if errorCode.Valid {
		got.ErrorCode = errorCode.String
	}
	if got != want {
		t.Fatalf("metadata state = %+v, want %+v", got, want)
	}
}

func assertMetadataJob(t *testing.T, ctx context.Context, db *sql.DB, jobID int64, status string, attempts int) {
	t.Helper()
	var gotStatus string
	var gotAttempts int
	var leasedBy sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT status, attempts, leased_by FROM durable_jobs WHERE id = $1`, jobID,
	).Scan(&gotStatus, &gotAttempts, &leasedBy); err != nil {
		t.Fatalf("read metadata job: %v", err)
	}
	if gotStatus != status || gotAttempts != attempts || leasedBy.Valid {
		t.Fatalf("metadata job = status %q attempts %d leased=%v, want status %q attempts %d unleased", gotStatus, gotAttempts, leasedBy, status, attempts)
	}
}

func assertMetadataAttemptCount(t *testing.T, ctx context.Context, db *sql.DB, jobID int64, count int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM external_metadata_attempts WHERE durable_job_id = $1`, jobID).Scan(&got); err != nil {
		t.Fatalf("count metadata attempts: %v", err)
	}
	if got != count {
		t.Fatalf("metadata attempt count = %d, want %d", got, count)
	}
}
