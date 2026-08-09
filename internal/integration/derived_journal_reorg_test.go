//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

func TestDerivedJournalTracksSingleAndMultiBlockReorgs(t *testing.T) {
	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			db := newMigratedPostgres(t)
			repository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			processors := newDerivedProcessors(t, db)
			published := newDerivedPublicationHarness(t, db, processors)
			reader, err := catalog.NewPostgres(db, catalog.Options{})
			if err != nil {
				t.Fatal(err)
			}

			contract, from, recipient := testAddress(30), testAddress(31), testAddress(32)
			genesis := testBundle(0, testHash(10_000), testHash(0), testHash(11_000), "journal-genesis")
			if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
				t.Fatalf("commit covered genesis: %v", err)
			}
			oldBranch := make([]chainbundle.Bundle, depth)
			newBranch := make([]chainbundle.Bundle, depth)
			oldAmounts, newAmounts := make([]uint64, depth), make([]uint64, depth)
			oldParent, newParent := testHash(10_000), testHash(10_000)
			for index := range depth {
				height := uint64(index + 1)
				oldAmounts[index], newAmounts[index] = 10+height, 100+height
				oldHash, newHash := testHash(10_000+height), testHash(20_000+height)
				oldBranch[index] = derivedTokenBundle(
					t, height, oldHash, oldParent, testHash(11_000+height), "journal-old", contract, from, recipient, oldAmounts[index],
				)
				newBranch[index] = derivedTokenBundle(
					t, height, newHash, newParent, testHash(21_000+height), "journal-new", contract, from, recipient, newAmounts[index],
				)
				if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{oldBranch[index]}); err != nil {
					t.Fatalf("commit covered block %d: %v", height, err)
				}
				oldParent = oldBranch[index].Block.Hash()
				newParent = newBranch[index].Block.Hash()
				published.process(t, ctx, oldBranch[index])
				assertDerivedBlockState(t, ctx, db, oldBranch[index], true)
			}

			applyDerivedReorg(t, ctx, repository, genesis, oldBranch, newBranch, "journal test detach old")
			for _, block := range oldBranch {
				assertDerivedBlockState(t, ctx, db, block, false)
			}
			assertOrphanQueriesUnavailable(t, ctx, reader, contract, oldBranch[len(oldBranch)-1])

			for _, block := range newBranch {
				published.process(t, ctx, block)
				assertDerivedBlockState(t, ctx, db, block, true)
			}
			assertDerivedQueriesUseBranch(t, ctx, db, reader, contract, recipient, newBranch, oldBranch, sumUint64(newAmounts))

			applyDerivedReorg(t, ctx, repository, genesis, newBranch, oldBranch, "journal test reattach old")
			for _, block := range newBranch {
				assertDerivedBlockState(t, ctx, db, block, false)
			}
			for _, block := range oldBranch {
				assertDerivedBlockState(t, ctx, db, block, true)
			}
			// A same-hash reattach is not publication evidence by itself. Replaying
			// the exact attached hashes advances their durable generation and
			// refreshes the same journal/output identities without duplicates.
			for _, block := range oldBranch {
				published.process(t, ctx, block)
				assertDerivedBlockState(t, ctx, db, block, true)
			}
			assertDerivedQueriesUseBranch(t, ctx, db, reader, contract, recipient, oldBranch, newBranch, sumUint64(oldAmounts))
		})
	}
}

func TestStaleDerivedJobsPersistOnlyNonCanonicalJournals(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	contract, from, recipient := testAddress(40), testAddress(41), testAddress(42)
	genesis := testBundle(0, testHash(30_000), testHash(0), testHash(31_000), "stale-genesis")
	stale := derivedTokenBundle(t, 1, testHash(30_001), testHash(30_000), testHash(31_001), "stale-old", contract, from, recipient, 7)
	replacement := derivedTokenBundle(t, 1, testHash(40_001), testHash(30_000), testHash(41_001), "stale-new", contract, from, recipient, 9)
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, stale)
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{stale}, []chainbundle.Bundle{replacement}, "make enrichment job stale")

	processDerivedBlock(t, ctx, newDerivedProcessors(t, db), stale)
	blockHash, _ := stale.BlockHash()
	for _, table := range []string{"token_events", "token_balance_deltas", "normalized_traces", "block_statistics"} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1`, table),
			0, mustBytes(t, blockHash),
		)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1`, 3, mustBytes(t, blockHash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 3, mustBytes(t, blockHash))
}

func TestDerivedJournalFailureRollsBackEveryProductionStage(t *testing.T) {
	for _, stage := range []enrich.StageID{enrich.TokenStage, enrich.StatsStage, enrich.TraceStage} {
		t.Run(stage.String(), func(t *testing.T) {
			db := newMigratedPostgres(t)
			repository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			genesis := testBundle(0, testHash(50_000), testHash(0), testHash(51_000), "rollback-genesis")
			block := derivedTokenBundle(
				t, 1, testHash(50_001), testHash(50_000), testHash(51_001), "rollback-block",
				testAddress(50), testAddress(51), testAddress(52), 13,
			)
			if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, block}); err != nil {
				t.Fatalf("commit covered rollback branch: %v", err)
			}
			execFixture(t, ctx, db, `
				CREATE FUNCTION reject_derived_journal() RETURNS trigger
				LANGUAGE plpgsql AS $$
				BEGIN
				    RAISE EXCEPTION 'integration journal rejection';
				END
				$$`)
			execFixture(t, ctx, db, `
				CREATE TRIGGER reject_derived_journal_write
				BEFORE INSERT OR UPDATE ON block_journals
				FOR EACH ROW EXECUTE FUNCTION reject_derived_journal()`)

			processors := newDerivedProcessors(t, db)
			job := derivedJob(t, block, stage)
			var processErr error
			switch stage {
			case enrich.TokenStage:
				_, processErr = processors.token.Process(ctx, job)
			case enrich.StatsStage:
				_, processErr = processors.stats.Process(ctx, job)
			case enrich.TraceStage:
				_, processErr = processors.trace.Process(ctx, job)
			}
			if processErr == nil || !strings.Contains(processErr.Error(), "persist block stage journal") {
				t.Fatalf("process error = %v, want journal write failure", processErr)
			}
			blockHash, _ := block.BlockHash()
			assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, blockHash))
			assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, blockHash))
			for _, table := range []string{"token_events", "token_balance_deltas", "normalized_traces", "block_statistics"} {
				assertRowCount(t, ctx, db,
					fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1`, table),
					0, mustBytes(t, blockHash),
				)
			}
		})
	}
}

type derivedProcessors struct {
	token *enrich.PostgresTokenProcessor
	stats *enrich.PostgresStatsProcessor
	trace *enrich.TraceRPCProcessor
}

type derivedPublicationHarness struct {
	db        *sql.DB
	queue     *enrich.PostgresJobQueue
	worker    *enrich.Worker
	runs      map[string]int
	stageList []enrich.StageID
}

func newDerivedProcessors(t *testing.T, db *sql.DB) derivedProcessors {
	t.Helper()
	token, err := enrich.NewPostgresTokenProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := enrich.NewPostgresStatsProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "derived-trace", Client: newIntegrationRPCClient(t, "debug", &derivedTraceService{db: db}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	trace, err := enrich.NewTraceRPCProcessor(db, pool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return derivedProcessors{token: token, stats: stats, trace: trace}
}

func newDerivedPublicationHarness(
	t *testing.T,
	db *sql.DB,
	processors derivedProcessors,
) *derivedPublicationHarness {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{
		processors.token, processors.stats, processors.trace,
	}, enrich.WorkerOptions{ID: "derived-publication", LeaseDuration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return &derivedPublicationHarness{
		db: db, queue: queue, worker: worker, runs: make(map[string]int),
		stageList: []enrich.StageID{enrich.TokenStage, enrich.StatsStage, enrich.TraceStage},
	}
}

func (harness *derivedPublicationHarness) process(
	t *testing.T,
	ctx context.Context,
	block chainbundle.Bundle,
) {
	t.Helper()
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		reference.Hash.String(),
	); err != nil {
		t.Fatalf("acknowledge derived block outbox: %v", err)
	}
	harness.runs[reference.Hash.String()]++
	replay := enrich.ReplaySource{
		Kind: "integration-derived-publication",
		Key:  fmt.Sprintf("%s:%d", reference.Hash, harness.runs[reference.Hash.String()]),
	}
	for _, stage := range harness.stageList {
		result, err := harness.queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
			Replay: replay,
		})
		if err != nil || !result.Created && !result.Replayed {
			t.Fatalf("enqueue published %s for %s: result=%+v error=%v", stage, reference.Hash, result, err)
		}
	}
	for range harness.stageList {
		processed, err := harness.worker.ProcessOne(ctx)
		if err != nil || !processed {
			t.Fatalf("process published derived stage for %s: processed=%t error=%v", reference.Hash, processed, err)
		}
	}
}

type derivedTraceService struct{ db *sql.DB }

func (service *derivedTraceService) TraceTransaction(
	ctx context.Context,
	hash common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	if service.db == nil {
		return nil, errors.New("derived trace caller is not configured")
	}
	var from, to, value, gas, input string
	if err := service.db.QueryRowContext(ctx, `
		SELECT raw->>'from', raw->>'to', raw->>'value', raw->>'gas', raw->>'input'
		FROM transactions
		WHERE chain_id = 1 AND hash = $1`, hash.Bytes()).Scan(&from, &to, &value, &gas, &input); err != nil {
		return nil, fmt.Errorf("read derived trace transaction input: %w", err)
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from, "to": to,
		"value": value, "gas": gas, "gasUsed": "0x100", "input": input, "output": "0x",
	})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func processDerivedBlock(t *testing.T, ctx context.Context, processors derivedProcessors, block chainbundle.Bundle) {
	t.Helper()
	for _, entry := range []struct {
		stage   enrich.StageID
		process func(context.Context, enrich.Job) (enrich.StageResult, error)
	}{
		{stage: enrich.TokenStage, process: processors.token.Process},
		{stage: enrich.StatsStage, process: processors.stats.Process},
		{stage: enrich.TraceStage, process: processors.trace.Process},
	} {
		result, err := entry.process(ctx, derivedJob(t, block, entry.stage))
		if err != nil {
			t.Fatalf("process %s: %v", entry.stage, err)
		}
		if result.State != enrich.ResultComplete {
			t.Fatalf("process %s result = %+v", entry.stage, result)
		}
	}
}

func derivedJob(t *testing.T, block chainbundle.Bundle, stage enrich.StageID) enrich.Job {
	t.Helper()
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	return enrich.Job{
		ID:    fmt.Sprintf("integration-%s-%d-%s", stage.Name, reference.Number, reference.Hash),
		Stage: stage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	}
}

func derivedTokenBundle(
	t *testing.T,
	number uint64,
	blockHash, parentHash, transactionHash common.Hash,
	variant string,
	contract, from, to common.Address,
	amount uint64,
) chainbundle.Bundle {
	t.Helper()
	amountWord := make([]byte, 32)
	binary.BigEndian.PutUint64(amountWord[24:], amount)
	destination := testAddress(2)
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number:     number,
		ParentHash: parentHash,
		ExtraData:  []byte(variant),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &destination,
			Data: append([]byte(variant), transactionHash.Bytes()...),
			Logs: []*types.Log{{
				Address: contract,
				Topics: []common.Hash{
					common.HexToHash(transferTopic), derivedAddressTopic(t, from), derivedAddressTopic(t, to),
				},
				Data: amountWord,
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": variant},
	})
	if err != nil {
		t.Fatalf("build derived token bundle: %v", err)
	}
	registerFixtureIdentities(blockHash, bundle.Block.Hash(), transactionHash, bundle.Block.Transactions()[0].Hash())
	return bundle
}

func derivedAddressTopic(t *testing.T, address common.Address) common.Hash {
	t.Helper()
	addressBytes := mustBytes(t, address)
	word := make([]byte, 32)
	copy(word[12:], addressBytes)
	return common.BytesToHash(word)
}

func applyDerivedReorg(
	t *testing.T,
	ctx context.Context,
	repository *store.PostgresRepository,
	ancestor chainbundle.Bundle,
	detached, attached []chainbundle.Bundle,
	reason string,
) {
	t.Helper()
	detachedReferences := make([]store.BlockRef, len(detached))
	for index := range detached {
		detachedReferences[len(detached)-1-index] = mustBlockRef(t, detached[index])
	}
	checkpoint := mustBlockRef(t, attached[len(attached)-1])
	if err := repository.ApplyReorg(ctx, "1", store.Reorg{
		Ancestor: mustBlockRef(t, ancestor), Detached: detachedReferences, Attached: attached,
		Checkpoint: store.NewCoreCheckpoint(checkpoint), Reason: reason,
	}); err != nil {
		t.Fatalf("apply derived reorg: %v", err)
	}
}

func assertDerivedBlockState(t *testing.T, ctx context.Context, db *sql.DB, block chainbundle.Bundle, canonical bool) {
	t.Helper()
	reference := mustBlockRef(t, block)
	blockHash := mustBytes(t, reference.Hash)
	expectedRows := map[string]int{
		"token_events": 1, "token_balance_deltas": 2, "normalized_traces": 1, "block_statistics": 1,
	}
	for table, expected := range expectedRows {
		var total, matching int
		query := fmt.Sprintf(`
			SELECT count(*), count(*) FILTER (WHERE canonical = $2)
			FROM %s WHERE chain_id = 1 AND block_hash = $1`, table)
		if err := db.QueryRowContext(ctx, query, blockHash, canonical).Scan(&total, &matching); err != nil {
			t.Fatalf("query %s canonical state: %v", table, err)
		}
		if total != expected || matching != expected {
			t.Fatalf("%s rows total=%d matching canonical=%t:%d, want %d", table, total, canonical, matching, expected)
		}
	}
	rows, err := db.QueryContext(ctx, `
		SELECT stage, sequence::text, payload, canonical
		FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1
		ORDER BY stage, sequence`, blockHash)
	if err != nil {
		t.Fatalf("query derived journals: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	seen := make(map[string]bool)
	for rows.Next() {
		var stage, sequence string
		var raw []byte
		var storedCanonical bool
		if err := rows.Scan(&stage, &sequence, &raw, &storedCanonical); err != nil {
			t.Fatal(err)
		}
		if sequence != "1" || storedCanonical != canonical {
			t.Fatalf("journal %s sequence=%s canonical=%t", stage, sequence, storedCanonical)
		}
		var payload struct {
			Schema   string `json:"schema"`
			Version  int    `json:"version"`
			Stage    string `json:"stage"`
			Rollback struct {
				Operation string   `json:"operation"`
				Canonical bool     `json:"canonical"`
				Relations []string `json:"relations"`
			} `json:"rollback"`
			Replay struct {
				Operation string   `json:"operation"`
				Canonical bool     `json:"canonical"`
				Relations []string `json:"relations"`
			} `json:"replay"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode journal %s: %v", stage, err)
		}
		if payload.Schema != "etherview.derived-canonicality" || payload.Version != 1 || payload.Stage != stage ||
			payload.Rollback.Operation != "set_canonical" || payload.Rollback.Canonical ||
			payload.Replay.Operation != "set_canonical" || !payload.Replay.Canonical {
			t.Fatalf("journal %s payload = %+v", stage, payload)
		}
		seen[stage] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"token@1", "stats@3", "trace@2"} {
		if !seen[stage] {
			t.Fatalf("missing journal %s; seen=%v", stage, seen)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("journal identities = %v", seen)
	}
}

func assertOrphanQueriesUnavailable(
	t *testing.T,
	ctx context.Context,
	reader *catalog.Postgres,
	contract common.Address,
	orphan chainbundle.Bundle,
) {
	t.Helper()
	if _, err := reader.TokenEvents(ctx, catalog.TokenEventRequest{ChainID: "1", TokenAddress: contract.String()}); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatalf("token query after reorg error = %v, want unavailable instead of orphan rows", err)
	}
	reference := mustBlockRef(t, orphan)
	if _, err := reader.BlockStats(ctx, catalog.BlockStatsRequest{ChainID: "1", FromBlock: "1", ToBlock: fmt.Sprint(reference.Number)}); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatalf("stats query after reorg error = %v, want unavailable instead of orphan rows", err)
	}
	transactionHash := orphan.Block.Transactions()[0].Hash()
	if _, err := reader.TransactionTrace(ctx, "1", transactionHash.String()); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("trace query after reorg error = %v, want not found for orphan transaction", err)
	}
}

func assertDerivedQueriesUseBranch(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	reader *catalog.Postgres,
	contract, recipient common.Address,
	active, orphan []chainbundle.Bundle,
	wantDelta uint64,
) {
	t.Helper()
	activeHashes := make(map[string]bool, len(active))
	for _, block := range active {
		activeHashes[mustBlockRef(t, block).Hash.String()] = true
	}
	page, err := reader.TokenEvents(ctx, catalog.TokenEventRequest{ChainID: "1", TokenAddress: contract.String(), Limit: 100})
	if err != nil {
		t.Fatalf("query canonical token events: %v", err)
	}
	if len(page.Items) != len(active) {
		t.Fatalf("canonical token events = %d, want %d", len(page.Items), len(active))
	}
	for _, event := range page.Items {
		if !activeHashes[event.BlockHash] {
			t.Fatalf("token query returned orphan block %s; active=%v", event.BlockHash, activeHashes)
		}
	}
	stats, err := reader.BlockStats(ctx, catalog.BlockStatsRequest{ChainID: "1", FromBlock: "1", ToBlock: fmt.Sprint(len(active))})
	if err != nil {
		t.Fatalf("query canonical stats: %v", err)
	}
	if len(stats) != len(active) {
		t.Fatalf("canonical stats = %d, want %d", len(stats), len(active))
	}
	for _, stat := range stats {
		if !activeHashes[stat.BlockHash] {
			t.Fatalf("stats query returned orphan block %s; active=%v", stat.BlockHash, activeHashes)
		}
	}
	activeTransaction := active[len(active)-1].Block.Transactions()[0].Hash()
	trace, err := reader.TransactionTrace(ctx, "1", activeTransaction.String())
	if err != nil || len(trace.Frames) != 1 || !activeHashes[trace.BlockHash] {
		t.Fatalf("canonical trace = %+v, error=%v", trace, err)
	}
	orphanTransaction := orphan[len(orphan)-1].Block.Transactions()[0].Hash()
	if _, err := reader.TransactionTrace(ctx, "1", orphanTransaction.String()); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("orphan trace error = %v, want not found", err)
	}
	var delta string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(delta), 0)::text
		FROM token_balance_deltas AS delta
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = delta.chain_id
		 AND canonical.number = delta.block_number
		 AND canonical.block_hash = delta.block_hash
		WHERE delta.chain_id = 1
		  AND delta.token_address = $1
		  AND delta.owner_address = $2
		  AND delta.canonical = TRUE`, mustBytes(t, contract), mustBytes(t, recipient)).Scan(&delta); err != nil {
		t.Fatalf("query canonical token aggregate: %v", err)
	}
	if delta != fmt.Sprint(wantDelta) {
		t.Fatalf("canonical recipient delta = %s, want %d", delta, wantDelta)
	}
}

func sumUint64(values []uint64) uint64 {
	var result uint64
	for _, value := range values {
		result += value
	}
	return result
}
