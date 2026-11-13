//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
)

type balanceRPC struct {
	bodies map[common.Hash]hexutil.Bytes
	calls  int
}

func (s *balanceRPC) GetBlockAccessListRaw(_ context.Context, hash common.Hash) (hexutil.Bytes, error) {
	s.calls++
	return s.bodies[hash], nil
}

func TestNativeBalancePublicationCoverageReplayAndReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, _ := store.NewPostgresRepository(db)
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis, err := newIntegrationBundle(integrationBundleOptions{ExtraData: []byte("balance-genesis")})
	if err != nil {
		t.Fatal(err)
	}
	service := &balanceRPC{bodies: map[common.Hash]hexutil.Bytes{}}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "state", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	address := common.HexToAddress("0x1234")
	unknown := common.HexToAddress("0x5678")
	makeBlock := func(parent chainbundle.Bundle, extra string, change bool) chainbundle.Bundle {
		list := []any{}
		if change {
			list = append(list, []any{address.Bytes(), []any{}, []any{}, []any{[]any{uint64(0), big.NewInt(42)}, []any{uint64(1), big.NewInt(0)}}, []any{}, []any{}})
		}
		if change {
			list = append(list, []any{unknown.Bytes(), []any{}, []any{}, []any{}, []any{}, []any{}})
		}
		encoded, err := rlp.EncodeToBytes(list)
		if err != nil {
			t.Fatal(err)
		}
		hash := crypto.Keccak256Hash(encoded)
		b, err := newIntegrationBundle(integrationBundleOptions{Number: parent.Block.NumberU64() + 1, ParentHash: parent.Block.Hash(), ExtraData: []byte(extra), Amsterdam: true, BALHash: &hash})
		if err != nil {
			t.Fatal(err)
		}
		service.bodies[b.Block.Hash()] = encoded
		return b
	}
	one := makeBlock(genesis, "one", true)
	two := makeBlock(one, "two", false)
	three := makeBlock(two, "three", false)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, one, two, three}); err != nil {
		t.Fatal(err)
	}
	cache := state.PostgresNativeBalances{DB: db, ChainID: "1"}
	ref := func(b chainbundle.Bundle) state.CanonicalRef {
		return state.CanonicalRef{Number: b.Block.NumberU64(), Hash: b.Block.Hash()}
	}
	read := func(b chainbundle.Bundle, a common.Address, want string) {
		t.Helper()
		values, err := cache.Read(ctx, ref(b), []common.Address{a})
		if err != nil {
			t.Fatal(err)
		}
		value, found := values[a]
		if value != want || found != (want != "") {
			t.Fatalf("block %d balance=%q found=%t want=%q", b.Block.NumberU64(), value, found, want)
		}
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	processor, _ := enrich.NewPostgresNativeBalanceProcessor(db, pool)
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ID: "balance-test", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(b chainbundle.Bundle, key string) enrich.Job {
		t.Helper()
		word, _ := enrich.ParseWord(b.Block.Hash().Hex())
		result, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.NativeBalanceStage, ChainID: "1", BlockHash: word, BlockNumber: b.Block.NumberU64(), Replay: enrich.ReplaySource{Kind: "balance-test", Key: key}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Job
	}
	process := func() {
		t.Helper()
		if found, err := worker.ProcessOne(ctx); err != nil || !found {
			t.Fatalf("process found=%t err=%v", found, err)
		}
	}
	publish := func(b chainbundle.Bundle, key string) {
		t.Helper()
		execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
		enqueue(b, key)
		process()
	}
	guardedJob := enqueue(genesis, "genesis")
	if _, err := db.Exec(ctx, `UPDATE durable_jobs SET status='succeeded' WHERE id=$1`, guardedJob.ID); err == nil {
		t.Fatal("unfenced native balance publication accepted")
	}

	publish(genesis, "genesis")
	if service.calls != 0 {
		t.Fatal("pre-BAL block fetched BAL")
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM browsing_blocks WHERE stage='native_balance' AND complete`, 0)
	read(one, address, "")
	publish(one, "one")
	read(one, address, "0")
	read(three, address, "")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_balance_changes`, 2)
	publish(three, "three")
	read(three, address, "") // a missing middle block is not continuity
	publish(two, "two")
	read(three, address, "0")
	read(three, unknown, "") // read-only/absent account is not a zero baseline
	if err := cache.Save(ctx, ref(three), map[common.Address]string{unknown: "0"}); err != nil {
		t.Fatal(err)
	}
	read(three, unknown, "0")
	if err := cache.Save(ctx, ref(one), map[common.Address]string{address: "99"}); !errors.Is(err, state.ErrNativeBalanceIntegrity) {
		t.Fatalf("conflict=%v", err)
	}
	for _, statement := range []string{
		`UPDATE native_balance_observations SET balance=1 WHERE balance=0`,
		`UPDATE native_balance_changes SET balance=1 WHERE balance=0`,
		`UPDATE native_balance_blocks SET raw='"0xc0"'::jsonb`,
	} {
		if _, err := db.Exec(ctx, statement); err == nil {
			t.Fatalf("mutable evidence: %s", statement)
		}
	}
	// Coverage follows every publication prerequisite, including pending Core
	// attachment and retained journal identity, not just the stage result row.
	if _, err := db.Exec(ctx, `UPDATE transactional_outbox SET published_at=NULL WHERE topic='core.block.canonical' AND message_key=$1`, two.Block.Hash().Hex()); err != nil {
		t.Fatal(err)
	}
	read(three, address, "")
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
	read(three, address, "0")
	var journal json.RawMessage
	var jobID, generation int64
	if err := db.QueryRow(ctx, `DELETE FROM block_journals WHERE block_hash=$1 AND stage='native_balance@1' RETURNING payload,durable_job_id,job_generation`, two.Block.Hash().Bytes()).Scan(&journal, &jobID, &generation); err != nil {
		t.Fatal(err)
	}
	read(three, address, "")
	if _, err := db.Exec(ctx, `INSERT INTO block_journals(chain_id,block_hash,stage,sequence,payload,canonical,durable_job_id,job_generation) VALUES(1,$1,'native_balance@1',1,$2,true,$3,$4)`, two.Block.Hash().Bytes(), journal, jobID, generation); err != nil {
		t.Fatal(err)
	}
	read(three, address, "0")

	calls := service.calls
	job := enqueue(two, "replay")
	read(three, address, "")
	process()
	read(three, address, "0")
	if service.calls != calls {
		t.Fatal("replay ignored retained authenticated BAL")
	}
	if _, err := processor.Process(ctx, job); !errors.Is(err, enrich.ErrAtomicPublicationRequired) {
		t.Fatalf("unleased=%v", err)
	}
	executor, err := maintenance.NewExecutor("1", &maintenanceBundleSource{bundle: two},
		&indexer.Canonicalizer{ChainID: "1", Repository: repository}, repository, queue)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Reindex(ctx, maintenance.Request{
		ID: 1, ChainID: "1", Operation: maintenance.OperationReindex, Stage: "native_balance",
		FromBlock: 2, ToBlock: 2, Reason: "rebuild BAL balances",
	}); err != nil {
		t.Fatal(err)
	}
	read(three, address, "")
	process()
	read(three, address, "0")
	if service.calls != calls {
		t.Fatal("maintenance replay ignored retained authenticated BAL")
	}
	replacement := makeBlock(genesis, "replacement", false)
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{one, two, three}, []chainbundle.Bundle{replacement}, "balance replacement")
	read(three, address, "")
	read(replacement, address, "")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_balance_changes WHERE canonical`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM native_balance_changes`, 2)
	publish(replacement, "replacement")
	read(replacement, address, "")
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{replacement}, []chainbundle.Bundle{one, two, three}, "balance reattach")
	read(one, address, "")
	publish(one, "reattach-one")
	publish(two, "reattach-two")
	publish(three, "reattach-three")
	read(three, address, "0")
	var raw json.RawMessage
	if err := db.QueryRow(ctx, `SELECT raw FROM native_balance_blocks WHERE block_hash=$1`, one.Block.Hash().Bytes()).Scan(&raw); err != nil || len(raw) == 0 {
		t.Fatalf("retained raw=%s error=%v", raw, err)
	}
}
