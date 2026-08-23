//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/ethrpc"
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
	conflicting := request
	conflicting.SourceURI = "https://metadata.example.invalid/conflicting-42.json"
	if _, err := repository.EnqueueNFT(ctx, conflicting); !errors.Is(err, metadata.ErrExactNFTMetadataConflict) {
		t.Fatalf("conflicting exact source error = %v, want ErrExactNFTMetadataConflict", err)
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
	orphanBlock := testBundle(1, testHash(903), testHash(900), testHash(9_003), "metadata-child")
	commitCanonical(t, ctx, core, orphanBlock)
	orphanRequest.BlockNumber = 1
	orphanRequest.BlockHash = testHash(903)
	orphanRequest.SourceURI = "https://metadata.example.invalid/42-v2.json"
	orphan, err := repository.EnqueueNFT(ctx, orphanRequest)
	if err != nil || !orphan.Created || orphan.JobID == first.JobID {
		t.Fatalf("enqueue changed source = %+v, err=%v", orphan, err)
	}
	orphanLease, found, err := repository.Claim(ctx, "metadata-integration-3", time.Minute)
	if err != nil || !found || orphanLease.JobID != orphan.JobID {
		t.Fatalf("claim changed source = %+v, found=%t, err=%v", orphanLease, found, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 1`); err != nil {
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
	genesis := testBundle(0, blockHash, testHash(0), testHash(9_100), "media-genesis")
	blockHash = genesis.Block.Hash()
	commitCanonical(t, ctx, core, genesis)
	address := testAddress(911)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			resolved_uri, media_type, content_hash, content_size, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'media:42', 'https://metadata.example.invalid/42.json',
			'available', '{"image":"https://media.example.invalid/42.png"}'::jsonb,
			'https://metadata.example.invalid/42.json', 'application/json', $3, 56,
			clock_timestamp(), clock_timestamp(), $1, 42, 0, $2, $2)`,
		mustBytes(t, address), mustBytes(t, blockHash), mustBytes(t, testHash(912))); err != nil {
		t.Fatalf("insert canonical NFT metadata: %v", err)
	}
	source, err := metadata.NewPostgresImageSource(db, "1")
	if err != nil {
		t.Fatalf("create PostgreSQL media source: %v", err)
	}
	selection, err := source.SelectNFTImage(ctx, address, "42")
	if err != nil || selection.URI != "https://media.example.invalid/42.png" || selection.BlockHash != blockHash {
		t.Fatalf("canonical image selection = %+v, err=%v", selection, err)
	}
	current, err := source.NFTImageCurrent(ctx, address, "42", selection)
	if err != nil || !current {
		t.Fatalf("canonical image current=%t err=%v", current, err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			resolved_uri, media_type, content_hash, content_size, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'media:43', 'https://metadata.example.invalid/43.json',
			'available', '{"name":"No image"}'::jsonb,
			'https://metadata.example.invalid/43.json', 'application/json', $3, 19,
			clock_timestamp(), clock_timestamp(), $1, 43, 0, $2, $2)`,
		mustBytes(t, address), mustBytes(t, blockHash), mustBytes(t, testHash(913))); err != nil {
		t.Fatalf("insert missing-image metadata: %v", err)
	}
	if _, err := source.SelectNFTImage(ctx, address, "43"); !errors.Is(err, metadata.ErrMediaImageNotFound) {
		t.Fatalf("missing image error = %v, want ErrMediaImageNotFound", err)
	}

	newBlockHash := testHash(914)
	newBlock := testBundle(1, newBlockHash, blockHash, testHash(9_140), "media-new")
	newBlockHash = newBlock.Block.Hash()
	commitCanonical(t, ctx, core, newBlock)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			resolved_uri, media_type, content_hash, content_size, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'media:42', 'https://metadata.example.invalid/42-v2.json',
			'available', '{"image":"https://media.example.invalid/42-v2.png"}'::jsonb,
			'https://metadata.example.invalid/42-v2.json', 'application/json', $3, 59,
			clock_timestamp(), clock_timestamp(), $1, 42, 1, $2, $2)`,
		mustBytes(t, address), mustBytes(t, newBlockHash), mustBytes(t, testHash(915))); err != nil {
		t.Fatalf("insert newer NFT metadata: %v", err)
	}
	newSelection, err := source.SelectNFTImage(ctx, address, "42")
	if err != nil || newSelection.BlockHash != newBlockHash || newSelection.URI != "https://media.example.invalid/42-v2.png" {
		t.Fatalf("new canonical image selection = %+v, err=%v", newSelection, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 1`); err != nil {
		t.Fatalf("orphan newer metadata observation: %v", err)
	}
	fallback, err := source.SelectNFTImage(ctx, address, "42")
	if err != nil || fallback.BlockHash != blockHash || fallback.URI != selection.URI {
		t.Fatalf("canonical fallback selection = %+v, err=%v", fallback, err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatalf("orphan metadata observation: %v", err)
	}
	if current, err := source.NFTImageCurrent(ctx, address, "42", selection); err != nil || current {
		t.Fatalf("orphan image current=%t err=%v", current, err)
	}
	if _, err := source.SelectNFTImage(ctx, address, "42"); !errors.Is(err, metadata.ErrMediaSourceNoncanonical) {
		t.Fatalf("orphan image error = %v, want ErrMediaSourceNoncanonical", err)
	}
}

func TestPostgresNFTMetadataDisplayReaderSelectsOnlyNewestCanonicalObservation(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(930), testHash(0), testHash(9_300), "metadata-display-genesis")
	commitCanonical(t, ctx, core, genesis)
	genesisHash := genesis.Block.Hash()
	address := testAddress(931)
	document := `{"name":"Canonical NFT","description":"plain","image":"ipfs://bafybeigdyrzt1234567890/42.png","attributes":[{"trait_type":"Level","value":9007199254740993}]}`
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			resolved_uri, media_type, content_hash, content_size, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'display:42:0', 'https://metadata.example.invalid/42.json',
			'available', $4::jsonb, 'https://metadata.example.invalid/42.json',
			'application/json', $3, octet_length(convert_to($4::text, 'UTF8')),
			clock_timestamp(), clock_timestamp(), $1, 42, 0, $2, $2)`,
		mustBytes(t, address), mustBytes(t, genesisHash), mustBytes(t, testHash(932)), document); err != nil {
		t.Fatalf("insert canonical display metadata: %v", err)
	}
	for index, state := range []metadata.State{
		metadata.StatePending, metadata.StateUnavailable, metadata.StateUnsafe, metadata.StateError,
	} {
		tokenID := 43 + index
		if state == metadata.StatePending {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO external_metadata (
					chain_id, resource_kind, resource_key, source_uri, state,
					token_address, token_id, observed_block_number, observed_block_hash, identity_hash
				) VALUES (1, 'nft', $4, 'https://metadata.example.invalid/terminal.json', $5,
					$1, $3::numeric, 0, $2, $2)`,
				mustBytes(t, address), mustBytes(t, genesisHash), strconv.Itoa(tokenID), "display:"+strconv.Itoa(tokenID), state); err != nil {
				t.Fatalf("insert %s display metadata: %v", state, err)
			}
			continue
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO external_metadata (
				chain_id, resource_kind, resource_key, source_uri, state,
				last_error_code, last_error, fetched_at, terminal_at,
				token_address, token_id, observed_block_number, observed_block_hash, identity_hash
			) VALUES (1, 'nft', $4, 'https://metadata.example.invalid/terminal.json', $5,
				'terminal', 'bounded', clock_timestamp(), clock_timestamp(),
				$1, $3::numeric, 0, $2, $2)`,
			mustBytes(t, address), mustBytes(t, genesisHash), strconv.Itoa(tokenID), "display:"+strconv.Itoa(tokenID), state); err != nil {
			t.Fatalf("insert %s display metadata: %v", state, err)
		}
	}

	reader, err := metadata.NewPostgresMetadataReader(db, "1", "https://ipfs.example/base")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.State != metadata.StateAvailable || selected.Name != "Canonical NFT" ||
		len(selected.Attributes) != 1 || selected.Attributes[0].Value != "9007199254740993" ||
		selected.Image.URL != "https://ipfs.example/base/ipfs/bafybeigdyrzt1234567890/42.png" ||
		selected.Observation.BlockHash != genesisHash || selected.ContentObservation == nil ||
		selected.ContentObservation.BlockHash != genesisHash || selected.ContentStale {
		t.Fatalf("canonical display selection=%+v err=%v", selected, err)
	}
	for index, state := range []metadata.State{
		metadata.StatePending, metadata.StateUnavailable, metadata.StateUnsafe, metadata.StateError,
	} {
		selected, err := reader.NFTMetadata(ctx, address, strconv.Itoa(43+index))
		if err != nil || selected.State != state || selected.Image.State != metadata.NFTMetadataImageUnavailable || len(selected.Attributes) != 0 {
			t.Fatalf("%s display selection=%+v err=%v", state, selected, err)
		}
	}

	newBlock := testBundle(1, testHash(933), genesisHash, testHash(9_330), "metadata-display-new")
	commitCanonical(t, ctx, core, newBlock)
	newHash := newBlock.Block.Hash()
	newDocument := `{"name":"New canonical NFT","image":"https://media.example/new.png"}`
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state, document,
			resolved_uri, media_type, content_hash, content_size, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'display:42:1', 'https://metadata.example.invalid/42-v2.json',
			'available', $4::jsonb, 'https://metadata.example.invalid/42-v2.json',
			'application/json', $3, octet_length(convert_to($4::text, 'UTF8')),
			clock_timestamp(), clock_timestamp(), $1, 42, 1, $2, $2)`,
		mustBytes(t, address), mustBytes(t, newHash), mustBytes(t, testHash(934)), newDocument); err != nil {
		t.Fatalf("insert newer display metadata: %v", err)
	}
	selected, err = reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.Name != "New canonical NFT" || selected.Observation.BlockHash != newHash ||
		selected.ContentObservation == nil || selected.ContentObservation.BlockHash != newHash || selected.ContentStale {
		t.Fatalf("new display selection=%+v err=%v", selected, err)
	}
	updateTopic := crypto.Keccak256Hash([]byte("MetadataUpdate(uint256)"))
	updateBlock := metadataUpdateBundle(t, 2, newHash, "metadata-display-refresh", address, []*types.Log{{
		Address: address, Topics: []common.Hash{updateTopic}, Data: metadataUint256(42),
	}})
	commitCanonical(t, ctx, core, updateBlock)
	updateHash := updateBlock.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, address, updateHash, 2, "erc721")
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	updateDiscoverer, err := metadata.NewUpdateDiscoverer(repository, metadata.UpdateDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := updateDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process display update=%t err=%v", processed, err)
	}
	selected, err = reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.State != metadata.StatePending || !selected.ContentStale ||
		selected.Observation.BlockHash != updateHash || selected.ContentObservation == nil ||
		selected.ContentObservation.BlockHash != newHash || selected.Name != "New canonical NFT" {
		t.Fatalf("pending stale display selection=%+v err=%v", selected, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO external_metadata (
			chain_id, resource_kind, resource_key, source_uri, state,
			last_error_code, last_error, fetched_at, terminal_at,
			token_address, token_id, observed_block_number, observed_block_hash, identity_hash
		) VALUES (1, 'nft', 'display:42:2', 'https://metadata.example.invalid/42-v3.json', 'error',
			'fetch_failed', 'bounded', clock_timestamp(), clock_timestamp(),
			$1, 42, 2, $2, $2)`, address.Bytes(), updateHash.Bytes()); err != nil {
		t.Fatalf("insert failed refreshed metadata: %v", err)
	}
	selected, err = reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.State != metadata.StateError || !selected.ContentStale ||
		selected.Observation.BlockHash != updateHash || selected.ContentObservation == nil ||
		selected.ContentObservation.BlockHash != newHash || selected.Name != "New canonical NFT" {
		t.Fatalf("failed stale display selection=%+v err=%v", selected, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 2`); err != nil {
		t.Fatal(err)
	}
	selected, err = reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.State != metadata.StateAvailable || selected.ContentStale ||
		selected.Observation.BlockHash != newHash || selected.ContentObservation == nil ||
		selected.ContentObservation.BlockHash != newHash || selected.Name != "New canonical NFT" {
		t.Fatalf("post-reorg display selection=%+v err=%v", selected, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 1`); err != nil {
		t.Fatal(err)
	}
	selected, err = reader.NFTMetadata(ctx, address, "42")
	if err != nil || selected.Name != "Canonical NFT" || selected.Observation.BlockHash != genesisHash {
		t.Fatalf("fallback display selection=%+v err=%v", selected, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.NFTMetadata(ctx, address, "42"); !errors.Is(err, metadata.ErrNFTMetadataNoncanonical) {
		t.Fatalf("orphan-only display error=%v", err)
	}
	if _, err := reader.NFTMetadata(ctx, address, "99"); !errors.Is(err, metadata.ErrNFTMetadataNotFound) {
		t.Fatalf("missing display error=%v", err)
	}
}

func TestPostgresNFTMetadataUpdateObservationsAreExactImmutableAndReorgSafe(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	token := testAddress(940)
	singleTopic := crypto.Keccak256Hash([]byte("MetadataUpdate(uint256)"))
	blockZero := metadataUpdateBundle(t, 0, common.Hash{}, "metadata-update-zero", token, []*types.Log{{
		Address: token, Topics: []common.Hash{singleTopic}, Data: metadataUint256(42),
	}})
	commitCanonical(t, ctx, core, blockZero)
	zeroHash := blockZero.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token, zeroHash, 0, "erc721")
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, found, err := repository.NextNFTUpdate(ctx)
	if err != nil || !found || candidate.Token != token || candidate.BlockHash != zeroHash || candidate.Standard != metadata.NFTStandardERC721 {
		t.Fatalf("update candidate=%+v found=%t err=%v", candidate, found, err)
	}
	discoverer, err := metadata.NewUpdateDiscoverer(repository, metadata.UpdateDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := discoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process update=%t err=%v", processed, err)
	}
	var state, kind, fromID, toID string
	if err := db.QueryRowContext(ctx, `
		SELECT state, event_kind, from_token_id::text, to_token_id::text
		FROM nft_metadata_update_observations
		WHERE chain_id = 1 AND block_number = 0 AND block_hash = $1 AND log_index = 0`,
		zeroHash.Bytes(),
	).Scan(&state, &kind, &fromID, &toID); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || kind != "erc4906_single" || fromID != "42" || toID != "42" {
		t.Fatalf("stored update=%s:%s:%s:%s", state, kind, fromID, toID)
	}
	observation := metadata.NFTUpdateObservation{
		Candidate: candidate, Kind: metadata.NFTUpdateERC4906Single,
		State: metadata.NFTUpdateAccepted, FromTokenID: "42", ToTokenID: "42",
	}
	const writers = 8
	var wait sync.WaitGroup
	errorsSeen := make(chan error, writers)
	for range writers {
		wait.Go(func() {
			canonical, recordErr := repository.RecordNFTUpdate(ctx, observation)
			if recordErr != nil {
				errorsSeen <- recordErr
			} else if !canonical {
				errorsSeen <- errors.New("identical update observation became stale")
			}
		})
	}
	wait.Wait()
	close(errorsSeen)
	for recordErr := range errorsSeen {
		t.Fatal(recordErr)
	}
	conflicting := observation
	conflicting.State = metadata.NFTUpdateMalformed
	conflicting.FromTokenID, conflicting.ToTokenID = "", ""
	conflicting.ErrorCode = "data_length_invalid"
	if _, err := repository.RecordNFTUpdate(ctx, conflicting); !errors.Is(err, metadata.ErrExactNFTUpdateConflict) {
		t.Fatalf("conflicting update error=%v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE nft_metadata_update_observations SET error_code = 'mutated'
		WHERE chain_id = 1 AND block_number = 0 AND block_hash = $1 AND log_index = 0`,
		zeroHash.Bytes(),
	); err == nil {
		t.Fatal("direct mutation of exact NFT metadata update succeeded")
	}

	blockOne := metadataUpdateBundle(t, 1, zeroHash, "metadata-update-one", token, []*types.Log{{
		Address: token, Topics: []common.Hash{singleTopic}, Data: metadataUint256(43),
	}})
	commitCanonical(t, ctx, core, blockOne)
	oneHash := blockOne.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token, oneHash, 1, "erc721")
	staleCandidate, found, err := repository.NextNFTUpdate(ctx)
	if err != nil || !found || staleCandidate.BlockHash != oneHash {
		t.Fatalf("stale candidate=%+v found=%t err=%v", staleCandidate, found, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 1`); err != nil {
		t.Fatal(err)
	}
	canonical, err := repository.RecordNFTUpdate(ctx, metadata.NFTUpdateObservation{
		Candidate: staleCandidate, Kind: metadata.NFTUpdateERC4906Single,
		State: metadata.NFTUpdateAccepted, FromTokenID: "43", ToTokenID: "43",
	})
	if err != nil || canonical {
		t.Fatalf("stale record canonical=%t err=%v", canonical, err)
	}
	var staleRows int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM nft_metadata_update_observations
		WHERE chain_id = 1 AND block_hash = $1`, oneHash.Bytes(),
	).Scan(&staleRows); err != nil || staleRows != 0 {
		t.Fatalf("stale update rows=%d err=%v", staleRows, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 0`); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM nft_metadata_update_observations
		WHERE chain_id = 1 AND block_hash = $1`, zeroHash.Bytes(),
	).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("retained orphan updates=%d err=%v", retained, err)
	}
	canonical, err = repository.RecordNFTUpdate(ctx, observation)
	if err != nil || canonical {
		t.Fatalf("retained orphan idempotency canonical=%t err=%v", canonical, err)
	}
}

func TestPostgresNFTMetadataUpdateUsesLatestExactTokenStandard(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	token := testAddress(945)
	blockZero := testBundle(0, testHash(946), common.Hash{}, testHash(9_460), "metadata-standard-base")
	commitCanonical(t, ctx, core, blockZero)
	zeroHash := blockZero.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token, zeroHash, 0, "erc721")

	singleTopic := crypto.Keccak256Hash([]byte("MetadataUpdate(uint256)"))
	blockOne := metadataUpdateBundle(t, 1, zeroHash, "metadata-standard-mismatch", token, []*types.Log{{
		Address: token, Topics: []common.Hash{singleTopic}, Data: metadataUint256(42),
	}})
	commitCanonical(t, ctx, core, blockOne)
	oneHash := blockOne.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token, oneHash, 1, "erc1155")
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, found, err := repository.NextNFTUpdate(ctx)
	if err != nil || !found || candidate.Standard != metadata.NFTStandardERC1155 || candidate.BlockHash != oneHash {
		t.Fatalf("latest-standard candidate=%+v found=%t err=%v", candidate, found, err)
	}
	discoverer, err := metadata.NewUpdateDiscoverer(repository, metadata.UpdateDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := discoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process standard mismatch=%t err=%v", processed, err)
	}
	var state, errorCode string
	if err := db.QueryRowContext(ctx, `
		SELECT state, error_code
		FROM nft_metadata_update_observations
		WHERE chain_id = 1 AND block_hash = $1 AND log_index = 0`,
		oneHash.Bytes(),
	).Scan(&state, &errorCode); err != nil {
		t.Fatal(err)
	}
	if state != "malformed" || errorCode != "standard_mismatch" {
		t.Fatalf("latest-standard observation=%s:%s", state, errorCode)
	}
}

func TestPostgresNFTMetadataUpdateSignalsDriveBoundedExactSourceRefresh(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	token721 := testAddress(950)
	token1155 := testAddress(951)
	blockZero := testBundle(0, testHash(952), common.Hash{}, testHash(9_520), "metadata-refresh-base")
	commitCanonical(t, ctx, core, blockZero)
	zeroHash := blockZero.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token721, zeroHash, 0, "erc721")
	const sameURI = "https://metadata.example/42.json"
	for _, tokenID := range []string{"5", "20", "42", "500"} {
		if err := repository.RecordNFTSource(ctx, metadata.NFTSourceObservation{
			Candidate: metadata.NFTSourceCandidate{
				ChainID: "1", Token: token721, TokenID: tokenID, BlockNumber: 0,
				BlockHash: zeroHash, Standard: metadata.NFTStandardERC721,
			},
			State: metadata.NFTSourceFound, SourceURI: sameURI,
		}); err != nil {
			t.Fatalf("record initial source %s: %v", tokenID, err)
		}
	}

	singleTopic := crypto.Keccak256Hash([]byte("MetadataUpdate(uint256)"))
	blockOne := metadataUpdateBundle(t, 1, zeroHash, "metadata-refresh-single", token721, []*types.Log{{
		Address: token721, Topics: []common.Hash{singleTopic}, Data: metadataUint256(42),
	}})
	commitCanonical(t, ctx, core, blockOne)
	oneHash := blockOne.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token721, oneHash, 1, "erc721")
	updateDiscoverer, err := metadata.NewUpdateDiscoverer(repository, metadata.UpdateDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := updateDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process direct update=%t err=%v", processed, err)
	}
	rpcService := &metadataRefreshRPC{erc721URI: sameURI, erc1155URI: "https://metadata.example/{id}.json"}
	pool, closeRPC := metadataRefreshRPCPool(t, rpcService)
	defer closeRPC()
	sourceDiscoverer, err := metadata.NewSourceDiscoverer(repository, pool, metadata.SourceDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := sourceDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process direct source=%t err=%v", processed, err)
	}
	if len(rpcService.calls) != 1 || rpcService.calls[0].tokenID != "42" ||
		rpcService.calls[0].selector.BlockHash == nil || *rpcService.calls[0].selector.BlockHash != oneHash ||
		!rpcService.calls[0].selector.RequireCanonical {
		t.Fatalf("direct RPC calls=%+v", rpcService.calls)
	}
	var sameSourceVersions, pendingRows int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM nft_metadata_source_observations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 42 AND source_uri = $2`,
		token721.Bytes(), sameURI,
	).Scan(&sameSourceVersions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM external_metadata
		WHERE chain_id = 1 AND resource_kind = 'nft' AND token_address = $1
		  AND token_id = 42 AND observed_block_hash = $2 AND state = 'pending'`,
		token721.Bytes(), oneHash.Bytes(),
	).Scan(&pendingRows); err != nil {
		t.Fatal(err)
	}
	if sameSourceVersions != 2 || pendingRows != 1 {
		t.Fatalf("same URI source versions=%d pending rows=%d", sameSourceVersions, pendingRows)
	}

	batchTopic := crypto.Keccak256Hash([]byte("BatchMetadataUpdate(uint256,uint256)"))
	blockTwo := metadataUpdateBundle(t, 2, oneHash, "metadata-refresh-batch", token721, []*types.Log{{
		Address: token721, Topics: []common.Hash{batchTopic}, Data: append(metadataUint256(1), metadataUint256(30)...),
	}})
	commitCanonical(t, ctx, core, blockTwo)
	twoHash := blockTwo.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token721, twoHash, 2, "erc721")
	if processed, err := updateDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process batch update=%t err=%v", processed, err)
	}
	displayReader, err := metadata.NewPostgresMetadataReader(db, "1", "https://ipfs.example/base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := displayReader.NFTMetadata(ctx, token721, "19"); !errors.Is(err, metadata.ErrNFTMetadataNotFound) {
		t.Fatalf("undiscovered batch token metadata error=%v", err)
	}
	var batchIDs []string
	for {
		candidate, found, err := repository.NextNFTSource(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !found || candidate.BlockHash != twoHash {
			break
		}
		batchIDs = append(batchIDs, candidate.TokenID)
		if err := repository.RecordNFTSource(ctx, metadata.NFTSourceObservation{
			Candidate: candidate, State: metadata.NFTSourceFound,
			SourceURI: "https://metadata.example/" + candidate.TokenID + ".json",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(batchIDs, ",") != "5,20" {
		t.Fatalf("bounded batch IDs=%v", batchIDs)
	}

	uriTopic := crypto.Keccak256Hash([]byte("URI(string,uint256)"))
	blockThree := metadataUpdateBundle(t, 3, twoHash, "metadata-refresh-uri", token1155, []*types.Log{{
		Address: token1155,
		Topics:  []common.Hash{uriTopic, common.BigToHash(big.NewInt(7))},
		Data:    metadataABIString("https://event.example/untrusted/{id}.json"),
	}})
	commitCanonical(t, ctx, core, blockThree)
	threeHash := blockThree.Block.Hash()
	insertMetadataUpdateTokenContract(t, ctx, db, token1155, threeHash, 3, "erc1155")
	if processed, err := updateDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process URI update=%t err=%v", processed, err)
	}
	if processed, err := sourceDiscoverer.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("process URI source=%t err=%v", processed, err)
	}
	wantExpanded := "https://metadata.example/" + strings.Repeat("0", 63) + "7.json"
	var storedURI string
	if err := db.QueryRowContext(ctx, `
		SELECT source_uri FROM nft_metadata_source_observations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 7 AND block_hash = $2`,
		token1155.Bytes(), threeHash.Bytes(),
	).Scan(&storedURI); err != nil {
		t.Fatal(err)
	}
	lastCall := rpcService.calls[len(rpcService.calls)-1]
	if storedURI != wantExpanded || lastCall.tokenID != "7" || lastCall.selector.BlockHash == nil || *lastCall.selector.BlockHash != threeHash {
		t.Fatalf("stored URI=%q last RPC=%+v", storedURI, lastCall)
	}
}

type metadataRefreshRPCCall struct {
	tokenID  string
	selector rpc.BlockNumberOrHash
}

type metadataRefreshRPC struct {
	erc721URI  string
	erc1155URI string
	calls      []metadataRefreshRPCCall
}

func (service *metadataRefreshRPC) Call(
	_ context.Context,
	call map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	encoded, ok := call["data"].(string)
	if !ok {
		return nil, errors.New("metadata refresh call data is not a string")
	}
	input := common.FromHex(encoded)
	if len(input) != 36 {
		return nil, errors.New("metadata refresh calldata length is invalid")
	}
	tokenID := new(big.Int).SetBytes(input[4:]).String()
	service.calls = append(service.calls, metadataRefreshRPCCall{tokenID: tokenID, selector: selector})
	uri := service.erc721URI
	if hexutil.Encode(input[:4]) == "0x0e89341c" {
		uri = service.erc1155URI
	}
	return hexutil.Bytes(metadataABIString(uri)), nil
}

func metadataRefreshRPCPool(t *testing.T, service *metadataRefreshRPC) (*ethrpc.Pool, func()) {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "metadata-state", Client: client,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		client.Close()
		server.Stop()
		t.Fatal(err)
	}
	return pool, func() {
		client.Close()
		server.Stop()
	}
}

func metadataUpdateBundle(
	t *testing.T,
	number uint64,
	parentHash common.Hash,
	variant string,
	token common.Address,
	logs []*types.Log,
) chainbundle.Bundle {
	t.Helper()
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parentHash, ExtraData: []byte(variant),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &token, Data: []byte(variant), Logs: logs,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func insertMetadataUpdateTokenContract(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	token common.Address,
	blockHash common.Hash,
	blockNumber uint64,
	standard string,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, $3, 'high', 'complete', $4, $5)`,
		token.Bytes(), testHash(941+blockNumber).Bytes(), standard,
		strconv.FormatUint(blockNumber, 10), blockHash.Bytes(),
	); err != nil {
		t.Fatalf("insert metadata update token contract: %v", err)
	}
}

func metadataUint256(value int64) []byte {
	return new(big.Int).SetInt64(value).FillBytes(make([]byte, 32))
}

func metadataABIString(value string) []byte {
	length := len(value)
	padded := (length + 31) / 32 * 32
	result := make([]byte, 64+padded)
	result[31] = 32
	binary.BigEndian.PutUint64(result[56:64], uint64(length))
	copy(result[64:], value)
	return result
}

func TestPostgresNFTMetadataSourceDiscoveryIsExactAndImmutable(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	blockHash := testHash(920)
	bundle := testBundle(0, blockHash, testHash(0), testHash(9_200), "metadata-source")
	blockHash = bundle.Block.Hash()
	commitCanonical(t, ctx, core, bundle)
	token := testAddress(921)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, 'erc721', 'high', 'pending', 0, $3)`,
		mustBytes(t, token), mustBytes(t, testHash(922)), mustBytes(t, blockHash)); err != nil {
		t.Fatalf("insert NFT contract: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, token_id, amount, canonical, confidence, raw
		) VALUES (1, 0, $1, 0, 0, $2, $3, 'erc721', 'transfer',
			$4, $5, 42, 1, TRUE, 'high', '{}')`,
		mustBytes(t, blockHash), mustBytes(t, bundle.Block.Transactions()[0].Hash()),
		mustBytes(t, token), mustBytes(t, testAddress(923)), mustBytes(t, testAddress(924))); err != nil {
		t.Fatalf("insert NFT event candidate: %v", err)
	}
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, found, err := repository.NextNFTSource(ctx)
	if err != nil || !found || candidate.Token != token || candidate.TokenID != "42" ||
		candidate.BlockHash != blockHash || candidate.Standard != metadata.NFTStandardERC721 {
		t.Fatalf("source candidate = %+v found=%t err=%v", candidate, found, err)
	}
	observation := metadata.NFTSourceObservation{
		Candidate: candidate, State: metadata.NFTSourceUnavailable, ErrorCode: "token_uri_unavailable",
	}
	if err := repository.RecordNFTSource(ctx, observation); err != nil {
		t.Fatalf("record source observation: %v", err)
	}
	if err := repository.RecordNFTSource(ctx, observation); err != nil {
		t.Fatalf("repeat identical source observation: %v", err)
	}
	if _, found, err := repository.NextNFTSource(ctx); err != nil || found {
		t.Fatalf("source candidate after terminal observation found=%t err=%v", found, err)
	}
	conflicting := observation
	conflicting.State = metadata.NFTSourceFound
	conflicting.ErrorCode = ""
	conflicting.SourceURI = "https://metadata.example.invalid/42.json"
	if err := repository.RecordNFTSource(ctx, conflicting); !errors.Is(err, metadata.ErrExactNFTSourceConflict) {
		t.Fatalf("conflicting source observation error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE nft_metadata_source_observations SET error_code = 'different'
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 42 AND block_hash = $2`,
		mustBytes(t, token), mustBytes(t, blockHash)); err == nil {
		t.Fatal("direct mutation of exact NFT source observation succeeded")
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
		  AND token_address = $2 AND token_id = $3::numeric AND observed_block_hash = $4`,
		request.ChainID, mustBytes(t, request.Token), request.TokenID, mustBytes(t, request.BlockHash),
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
