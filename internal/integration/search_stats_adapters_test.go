//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/adapters"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestSearchCursorGenerationFreezesLateLabelsAndEnrichment(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	blocks := []struct {
		number uint64
		hash   uint64
		parent uint64
	}{{0, 910, 0}, {1, 911, 910}, {2, 912, 911}}
	for _, block := range blocks {
		commitCanonical(t, ctx, repository, testBundle(block.number, testHash(block.hash), testHash(block.parent), testHash(9_100+block.number), "search"))
	}
	alpha, beta, late := testAddress(910), testAddress(911), testAddress(912)
	for _, item := range []struct{ address, label string }{{alpha.String(), "Treasury Alpha"}, {beta.String(), "Treasury Beta"}} {
		execFixture(t, ctx, db, `
			INSERT INTO operator_labels (chain_id, object_kind, object_key, label)
			VALUES (1, 'address', $1, $2)`, item.address, item.label)
	}
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (2)`)
	if _, err := db.ExecContext(ctx, `UPDATE operator_labels SET chain_id = 2
		WHERE chain_id = 1 AND object_key = $1`, alpha.String()); err == nil || !strings.Contains(err.Error(), "chain_id is immutable") {
		t.Fatalf("cross-chain source update error=%v", err)
	}
	var originalChain string
	if err := db.QueryRowContext(ctx, `SELECT chain_id::text FROM operator_labels
		WHERE object_key = $1`, alpha.String()).Scan(&originalChain); err != nil || originalChain != "1" {
		t.Fatalf("label chain=%q error=%v", originalChain, err)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, cursor, err := reader.Search(ctx, "treasury", "", 1)
	if err != nil || len(first) != 1 || cursor == "" || !strings.EqualFold(first[0].Key, alpha.String()) {
		t.Fatalf("first=%+v cursor=%q error=%v", first, cursor, err)
	}
	// Both mutations happen after page one without changing the canonical tip.
	execFixture(t, ctx, db, `UPDATE operator_labels SET label = 'Changed Alpha', updated_at = now()
		WHERE chain_id = 1 AND object_key = $1`, alpha.String())
	execFixture(t, ctx, db, `INSERT INTO operator_labels (chain_id, object_kind, object_key, label)
		VALUES (1, 'address', $1, 'treasury')`, late.String())
	second, next, err := reader.Search(ctx, "treasury", cursor, 1)
	if err != nil || len(second) != 1 || next != "" || !strings.EqualFold(second[0].Key, beta.String()) {
		t.Fatalf("second=%+v next=%q error=%v", second, next, err)
	}
	execFixture(t, ctx, db, `INSERT INTO operator_labels (chain_id, object_kind, object_key, label)
		SELECT 1, 'address', '0x' || lpad(to_hex(value), 40, '0'), 'noise-' || value::text
		FROM generate_series(10000, 11004) AS value`)
	if err := db.QueryRowContext(ctx, `SELECT prune_search_catalog(1, 1000)`).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Search(ctx, "treasury", cursor, 1); !errors.Is(err, query.ErrInvalidCursor) {
		t.Fatalf("expired cursor error=%v", err)
	}
}

func TestSearchCursorKeepsNormalizedAddressOrderingAcrossPages(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(913), testHash(0), testHash(9_113), "search-boundary"))
	firstAddress, secondAddress := checksumAddressOrderInversion(t)
	for _, address := range []string{firstAddress, secondAddress} {
		execFixture(t, ctx, db, `INSERT INTO operator_labels
			(chain_id, object_kind, object_key, label)
			VALUES (1, 'address', $1, 'boundary inversion')`, strings.ToLower(address))
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, cursor, err := reader.Search(ctx, "boundary inversion", "", 1)
	if err != nil || len(first) != 1 || cursor == "" || first[0].Key != firstAddress {
		t.Fatalf("first=%+v cursor=%q error=%v", first, cursor, err)
	}
	second, next, err := reader.Search(ctx, "boundary inversion", cursor, 1)
	if err != nil || len(second) != 1 || next != "" || second[0].Key != secondAddress {
		t.Fatalf("second=%+v next=%q error=%v", second, next, err)
	}
}

func checksumAddressOrderInversion(t *testing.T) (string, string) {
	t.Helper()
	type candidate struct {
		normalized string
		checksum   string
	}
	candidates := make([]candidate, 0, 512)
	for value := uint64(1); value <= 512; value++ {
		normalized := testAddress(value).String()
		checksum, err := query.ChecksumAddress(normalized)
		if err != nil {
			t.Fatal(err)
		}
		for _, previous := range candidates {
			if previous.normalized < normalized && previous.checksum > checksum {
				return previous.checksum, checksum
			}
		}
		candidates = append(candidates, candidate{normalized: normalized, checksum: checksum})
	}
	t.Fatal("failed to find EIP-55 checksum ordering inversion")
	return "", ""
}

func TestStatsV2ConfiguredStartRemainsParentlessWithRetainedCanonicalHistory(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	parent := testBundle(6, testHash(914), testHash(0), testHash(9_114), "stats-retained-parent")
	start := testBundle(7, testHash(915), testHash(914), testHash(9_115), "stats-configured-start")
	commitCanonical(t, ctx, repository, parent)
	if err := repository.ConfigureIndex(ctx, "1", 7); err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, start)
	processor, err := enrich.NewPostgresStatsProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(ctx, derivedJob(t, start, enrich.StatsStage))
	if err != nil || result.State != enrich.ResultComplete {
		t.Fatalf("stats result=%+v error=%v", result, err)
	}
	var interval, transactionsPerSecond sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT block_interval_seconds::text,
		transactions_per_second::text FROM block_statistics
		WHERE chain_id = 1 AND block_number = 7 AND block_hash = $1`, mustBytes(t, testHash(915))).Scan(
		&interval, &transactionsPerSecond,
	); err != nil {
		t.Fatal(err)
	}
	if interval.Valid || transactionsPerSecond.Valid {
		t.Fatalf("configured start interval=%v tps=%v", interval, transactionsPerSecond)
	}
}

func TestSearchUsesLatestCanonicalLogicalObservationAndPrunePreservesReorgFallback(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []struct{ number, hash, parent uint64 }{{0, 920, 0}, {1, 921, 920}, {2, 922, 921}} {
		commitCanonical(t, ctx, repository, testBundle(block.number, testHash(block.hash), testHash(block.parent), testHash(9_200+block.number), "logical"))
	}
	registry := mustBytes(t, testAddress(920))
	oldAddress, newAddress := mustBytes(t, testAddress(921)), mustBytes(t, testAddress(922))
	execFixture(t, ctx, db, `INSERT INTO name_records
		(chain_id, registry, name, address, block_number, block_hash, canonical)
		VALUES (1, $1, 'alice.eth', $2, 1, $3, true)`, registry, oldAddress, mustBytes(t, testHash(921)))
	execFixture(t, ctx, db, `INSERT INTO name_records
		(chain_id, registry, name, address, block_number, block_hash, canonical)
		VALUES (1, $1, 'alice.eth', $2, 2, $3, true)`, registry, newAddress, mustBytes(t, testHash(922)))
	execFixture(t, ctx, db, `INSERT INTO name_records
		(chain_id, registry, name, address, block_number, block_hash, canonical)
		VALUES (1, $1, 'alice.eth', $2, 2, $3, true)`,
		mustBytes(t, testAddress(929)), mustBytes(t, testAddress(928)), mustBytes(t, testHash(922)))
	tokenAddress := mustBytes(t, testAddress(923))
	execFixture(t, ctx, db, `INSERT INTO token_contracts
		(chain_id, address, code_hash, standard, confidence, name, symbol, metadata_state,
		 observed_block_number, observed_block_hash)
		VALUES (1, $1, $2, 'erc20', 'inferred', 'Old Coin', 'OLD', 'complete', 1, $3)`,
		tokenAddress, mustBytes(t, testHash(9_231)), mustBytes(t, testHash(921)))
	execFixture(t, ctx, db, `INSERT INTO token_contracts
		(chain_id, address, code_hash, standard, confidence, name, symbol, metadata_state,
		 observed_block_number, observed_block_hash)
		VALUES (1, $1, $2, 'erc20', 'inferred', 'New Coin', 'NEW', 'complete', 2, $3)`,
		tokenAddress, mustBytes(t, testHash(9_232)), mustBytes(t, testHash(922)))
	contractAddress := mustBytes(t, testAddress(924))
	execFixture(t, ctx, db, `INSERT INTO contract_code_observations
		(chain_id, address, block_number, block_hash, code_hash, code, canonical)
		VALUES (1, $1, 1, $2, $3, '\x01', true)`,
		contractAddress, mustBytes(t, testHash(921)), mustBytes(t, testHash(9_241)))
	execFixture(t, ctx, db, `INSERT INTO contract_code_observations
		(chain_id, address, block_number, block_hash, code_hash, code, canonical)
		VALUES (1, $1, 2, $2, $3, '\x02', true)`,
		contractAddress, mustBytes(t, testHash(922)), mustBytes(t, testHash(9_242)))
	oldValidTo := uint64(1)
	insertVerifiedContractFixture(
		t, ctx, db, contractAddress, mustBytes(t, testHash(9_241)), 1, &oldValidTo,
		"0.8.30", "OldVerified", `[]`, `{}`, `{}`,
	)
	insertVerifiedContractFixture(
		t, ctx, db, contractAddress, mustBytes(t, testHash(9_242)), 2, nil,
		"0.8.30", "NewVerified", `[]`, `{}`, `{}`,
	)
	resolvedName := testAddress(922).String()
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, NameResolver: nameResolverFunc(
		func(context.Context, string) (string, error) { return resolvedName, nil },
	)})
	if err != nil {
		t.Fatal(err)
	}
	names, _, err := reader.Search(ctx, "alice.eth", "", 20)
	if err != nil || len(names) != 1 || !strings.EqualFold(names[0].Key, testAddress(922).String()) {
		t.Fatalf("names=%+v error=%v", names, err)
	}
	oldTokens, _, err := reader.Search(ctx, "old coin", "", 20)
	if err != nil || len(oldTokens) != 0 {
		t.Fatalf("old tokens=%+v error=%v", oldTokens, err)
	}
	newTokens, _, err := reader.Search(ctx, "new coin", "", 20)
	if err != nil || len(newTokens) != 1 || newTokens[0].Kind != gen.SearchResultKindToken {
		t.Fatalf("new tokens=%+v error=%v", newTokens, err)
	}
	oldContracts, _, err := reader.Search(ctx, "oldverified", "", 20)
	if err != nil || len(oldContracts) != 0 {
		t.Fatalf("old contracts=%+v error=%v", oldContracts, err)
	}
	newContracts, _, err := reader.Search(ctx, "newverified", "", 20)
	if err != nil || len(newContracts) != 1 || newContracts[0].Kind != gen.SearchResultKindContract {
		t.Fatalf("new contracts=%+v error=%v", newContracts, err)
	}
	// Finality at genesis allows pruning only a finalized baseline; both
	// reorgable observations above it must remain visible in their generations.
	execFixture(t, ctx, db, `INSERT INTO chain_finality
		(chain_id, finalized_number, finalized_hash) VALUES (1, 0, $1)
		ON CONFLICT (chain_id) DO UPDATE SET finalized_number = 0, finalized_hash = EXCLUDED.finalized_hash`, mustBytes(t, testHash(920)))
	var minimum int64
	if err := db.QueryRowContext(ctx, `SELECT prune_search_catalog(1, 1000)`).Scan(&minimum); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 2`)
	resolvedName = testAddress(921).String()
	names, _, err = reader.Search(ctx, "alice.eth", "", 20)
	if err != nil || len(names) != 1 || !strings.EqualFold(names[0].Key, testAddress(921).String()) {
		t.Fatalf("reorg names=%+v min_generation=%d error=%v", names, minimum, err)
	}
	oldTokens, _, err = reader.Search(ctx, "old coin", "", 20)
	if err != nil || len(oldTokens) != 1 {
		t.Fatalf("reorg old tokens=%+v error=%v", oldTokens, err)
	}
	oldContracts, _, err = reader.Search(ctx, "oldverified", "", 20)
	if err != nil || len(oldContracts) != 1 {
		t.Fatalf("reorg old contracts=%+v error=%v", oldContracts, err)
	}
}

func TestPostgresAdaptersPersistFreshSuccessAndStableFailure(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repository, testBundle(0, testHash(930), testHash(0), testHash(9_300), "adapter"))
	assertSearchConstraintSet(t, ctx, db)
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	priceBody, _ := json.Marshal(map[string]any{
		"native_usd": "3500.25", "native_btc": "0.05", "observed_at": now.Add(-time.Minute),
	})
	priceFetcher := &integrationJSONFetcher{body: priceBody}
	priceService, err := adapters.NewPostgresPriceService(db, 1, priceFetcher, adapters.PriceOptions{
		BaseURL: "https://price.example/v1", Freshness: 5 * time.Minute,
		FailureTTL: 30 * time.Second, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		price, err := priceService.NativePrice(ctx)
		if err != nil || price.USD != "3500.25" {
			t.Fatalf("price=%+v error=%v", price, err)
		}
	}
	if priceFetcher.calls.Load() != 1 {
		t.Fatalf("price fetches=%d", priceFetcher.calls.Load())
	}

	secret := "https://operator:secret@example.invalid"
	failing := &integrationJSONFetcher{err: &metadata.FetchError{Kind: metadata.FailureUnsafeURL, Err: errors.New(secret)}}
	nameService, err := adapters.NewPostgresNameService(db, 1, failing, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err := nameService.Resolve(ctx, "missing.eth")
		if !errors.Is(err, adapters.ErrUnavailable) || strings.Contains(err.Error(), secret) {
			t.Fatalf("name error=%q", err)
		}
	}
	if failing.calls.Load() != 1 {
		t.Fatalf("name fetches=%d", failing.calls.Load())
	}
	secondProvider := &integrationJSONFetcher{err: &metadata.FetchError{Kind: metadata.FailureUnsafeURL, Err: errors.New(secret)}}
	isolatedService, err := adapters.NewPostgresNameService(db, 1, secondProvider, adapters.NameOptions{
		BaseURL: "https://name-two.example/v1?token=operator-secret", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedService.Resolve(ctx, "missing.eth"); !errors.Is(err, adapters.ErrUnavailable) {
		t.Fatalf("second-provider name error=%v", err)
	}
	if secondProvider.calls.Load() != 1 {
		t.Fatalf("second provider reused the first provider cache: fetches=%d", secondProvider.calls.Load())
	}
	var providerCount int
	var providerKeysSafe bool
	if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT provider_key),
		bool_and(provider_key ~ '^sha256:[0-9a-f]{64}$'
			AND provider_key NOT LIKE '%operator-secret%'
			AND provider_key NOT LIKE '%name.example%')
		FROM external_adapter_observations
		WHERE chain_id = 1 AND capability = 'name' AND observation_key = 'missing.eth'`).Scan(
		&providerCount, &providerKeysSafe,
	); err != nil || providerCount != 2 || !providerKeysSafe {
		t.Fatalf("provider identities count=%d safe=%t error=%v", providerCount, providerKeysSafe, err)
	}
	nameBody, _ := json.Marshal(map[string]any{
		"name": "alice.eth", "address": testAddress(931).String(), "registry": testAddress(932).String(),
		"block_number": "0", "block_hash": testHash(930).String(), "observed_at": now.Add(-time.Minute),
	})
	successNames := &integrationJSONFetcher{body: nameBody}
	nameService, err = adapters.NewPostgresNameService(db, 1, successNames, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	address, err := nameService.Resolve(ctx, "alice.eth")
	if err != nil || !strings.EqualFold(address, testAddress(931).String()) {
		t.Fatalf("address=%q error=%v", address, err)
	}
	if successNames.calls.Load() != 1 {
		t.Fatalf("successful name fetches=%d", successNames.calls.Load())
	}
	later := now.Add(2 * time.Hour)
	conflictBody, _ := json.Marshal(map[string]any{
		"name": "alice.eth", "address": testAddress(933).String(), "registry": testAddress(932).String(),
		"block_number": "0", "block_hash": testHash(930).String(), "observed_at": later.Add(-time.Minute),
	})
	conflictingNames := &integrationJSONFetcher{body: conflictBody}
	nameService, err = adapters.NewPostgresNameService(db, 1, conflictingNames, adapters.NameOptions{
		BaseURL: "https://name.example/v1", Freshness: time.Hour,
		FailureTTL: time.Minute, Now: func() time.Time { return later },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nameService.Resolve(ctx, "alice.eth"); !errors.Is(err, adapters.ErrUnavailable) {
		t.Fatalf("conflicting name error=%v", err)
	} else {
		var capabilityErr adapters.CapabilityError
		if !errors.As(err, &capabilityErr) || capabilityErr.Code != "identity_conflict" {
			t.Fatalf("conflicting capability error=%#v", err)
		}
	}
	if conflictingNames.calls.Load() != 1 {
		t.Fatalf("conflicting name fetches=%d", conflictingNames.calls.Load())
	}
	var storedAddress []byte
	if err := db.QueryRowContext(ctx, `SELECT address FROM name_records
		WHERE chain_id = 1 AND name = 'alice.eth' AND block_hash = $1`, mustBytes(t, testHash(930))).Scan(&storedAddress); err != nil {
		t.Fatal(err)
	}
	if got := "0x" + hex.EncodeToString(storedAddress); !strings.EqualFold(got, testAddress(931).String()) {
		t.Fatalf("stored address=%s", got)
	}
}

func TestExactCoreSearchUsesFrozenOperatorLabels(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	sharedHash := testHash(934)
	commitCanonical(t, ctx, repository, testBundle(0, sharedHash, testHash(0), sharedHash, "exact-label"))
	for _, label := range []struct {
		kind, key, value string
	}{
		{"block", "0", "Height zero label"},
		{"block", sharedHash.String(), "Block hash label"},
		{"transaction", sharedHash.String(), "Transaction hash label"},
	} {
		execFixture(t, ctx, db, `INSERT INTO operator_labels
			(chain_id, object_kind, object_key, label) VALUES (1, $1, $2, $3)`,
			label.kind, strings.ToLower(label.key), label.value)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	byHeight, _, err := reader.Search(ctx, "0", "", 20)
	if err != nil || len(byHeight) != 1 || byHeight[0].Label != "Height zero label" {
		t.Fatalf("height results=%+v error=%v", byHeight, err)
	}
	first, cursor, err := reader.Search(ctx, sharedHash.String(), "", 1)
	if err != nil || len(first) != 1 || cursor == "" || first[0].Kind != gen.SearchResultKindBlock ||
		first[0].Label != "Block hash label" {
		t.Fatalf("first exact results=%+v cursor=%q error=%v", first, cursor, err)
	}
	execFixture(t, ctx, db, `UPDATE operator_labels
		SET label = 'Changed transaction label', updated_at = now()
		WHERE chain_id = 1 AND object_kind = 'transaction' AND object_key = $1`, sharedHash.String())
	second, next, err := reader.Search(ctx, sharedHash.String(), cursor, 1)
	if err != nil || len(second) != 1 || next != "" || second[0].Kind != gen.SearchResultKindTransaction ||
		second[0].Label != "Transaction hash label" {
		t.Fatalf("second exact results=%+v next=%q error=%v", second, next, err)
	}
}

type integrationJSONFetcher struct {
	body  []byte
	err   error
	calls atomic.Int64
}

func (f *integrationJSONFetcher) Fetch(context.Context, string, metadata.Kind) (metadata.Result, error) {
	f.calls.Add(1)
	if f.err != nil {
		return metadata.Result{}, f.err
	}
	return metadata.Result{Body: append([]byte(nil), f.body...), FetchedAt: time.Now().UTC()}, nil
}

func assertSearchConstraintSet(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conname LIKE 'block_statistics_v2_%'
		  AND conrelid = 'block_statistics'::regclass
		  AND convalidated`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("validated stats constraints=%d error=%v", count, err)
	}
}
