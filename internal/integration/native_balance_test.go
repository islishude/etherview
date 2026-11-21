//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chaininput"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
	"github.com/jackc/pgx/v5/pgtype"
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
	collector := syncer.BALCollector{DB: db, ChainID: "1", Fetcher: &ethrpc.BALFetcher{Pool: pool}, Timeout: time.Second}
	processor, _ := enrich.NewPostgresNativeBalanceProcessor(db)
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
		found, err := collector.CollectOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			execFixture(t, ctx, db, `UPDATE durable_jobs SET available_at=now() WHERE stage='native_balance' AND status='queued'`)
			process()
		}
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
	// Hold a query on a genuinely missing address while its chosen tip is replaced.
	waiting := make(chan error, 1)
	go func() {
		demand := state.BalanceDemand{Store: cache, Timeout: 5 * time.Second}
		_, err := demand.Read(ctx, ref(three), []common.Address{common.HexToAddress("0xfeed")})
		waiting <- err
	}()
	for deadline := time.Now().Add(3 * time.Second); ; {
		var queued bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM balance_acquisitions WHERE block_hash=$1)`, three.Block.Hash().Bytes()).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("balance acquisition was not submitted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{one, two, three}, []chainbundle.Bundle{replacement}, "balance replacement")
	if err := <-waiting; !errors.Is(err, publicquery.ErrNotReady) {
		t.Fatalf("query returned stale branch: %v", err)
	}
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

type exactBalanceRPC struct {
	hash  common.Hash
	calls atomic.Int64
	codes atomic.Int64
}

func (s *exactBalanceRPC) GetBalance(_ context.Context, _ common.Address, ref rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	if ref.BlockHash == nil || *ref.BlockHash != s.hash || !ref.RequireCanonical {
		return 0, errors.New("unexpected block selector")
	}
	s.calls.Add(1)
	return 42, nil
}

func (s *exactBalanceRPC) GetCode(_ context.Context, address common.Address, ref rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if ref.BlockHash == nil || *ref.BlockHash != s.hash || !ref.RequireCanonical {
		return nil, errors.New("wrong code selector")
	}
	s.codes.Add(1)
	if address == common.HexToAddress("0xb16") {
		return make(hexutil.Bytes, (1<<20)+1), nil
	}
	return hexutil.Bytes{}, nil
}

func TestAccountInputAdmissionWithRetainedEvidence(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	block, err := newIntegrationBundle(integrationBundleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
		t.Fatal(err)
	}
	hash := block.Block.Hash().Bytes()
	execFixture(t, ctx, db, `INSERT INTO account_inputs(chain_id,block_number,block_hash,operation,address,argument,evidence,expires_at)
		SELECT 1,0,$1,'code@1',decode(lpad(to_hex(i),40,'0'),'hex'),''::bytea,'{}'::jsonb,now()+interval '1 hour' FROM generate_series(1,8193) i`, hash)
	q := dbgen.New(db)
	var chain, height pgtype.Numeric
	_ = chain.Scan("1")
	_ = height.Scan("0")
	if err := q.AccountInputExpire(ctx, chain); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM account_inputs`, 8192)
	key := dbgen.AccountInputSubmitParams{ChainID: chain, BlockNumber: height, BlockHash: hash, Operation: "code@1", Address: common.Address{}.Bytes(), Argument: []byte{}}
	if n, err := q.AccountInputSubmit(ctx, key); err != nil || n != 1 {
		t.Fatalf("admit missing account with a full evidence cache: rows=%d err=%v", n, err)
	}
	execFixture(t, ctx, db, `INSERT INTO account_inputs(chain_id,block_number,block_hash,operation,address,argument,expires_at)
		SELECT 1,0,$1,'summary@1',decode(lpad(to_hex(i),40,'0'),'hex'),''::bytea,now()+interval '2 minutes' FROM generate_series(1,8191) i`, hash)
	if n, err := q.AccountInputSubmit(ctx, key); err != nil || n != 1 {
		t.Fatalf("coalesce existing request at capacity: rows=%d err=%v", n, err)
	}
	demand := state.BalanceDemand{Store: state.PostgresNativeBalances{DB: db, ChainID: "1"}, Timeout: time.Minute}
	reader := state.Reader{Canonical: state.PostgresCanonicalSource{DB: db, ChainID: "1"}, BalanceDemand: &demand}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// An unadmitted request returns immediately even with a long remaining budget.
	result := make(chan error, 1)
	go func() {
		_, _, _, err := reader.AccountKind(requestCtx, common.HexToAddress("0xf00d").Hex())
		result <- err
	}()
	select {
	case err := <-result:
		var unavailable state.CapabilityError
		if !errors.As(err, &unavailable) || unavailable.Code != "endpoint_unavailable" {
			t.Fatalf("full acquisition queue error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waited for an account input that was never admitted")
	}
}

func TestSyncBalanceDemandReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis, err := newIntegrationBundle(integrationBundleOptions{ExtraData: []byte("acquisition")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	ref := state.CanonicalRef{Hash: genesis.Block.Hash()}
	cache := state.PostgresNativeBalances{DB: db, ChainID: "1"}
	demand := state.BalanceDemand{Store: cache, Timeout: 300 * time.Millisecond}
	account := common.HexToAddress("0x1234")
	addresses := []common.Address{account, account}
	// Both waiters time out independently, leaving one durable request.
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := demand.Read(ctx, ref, addresses); errs <- err }()
	}
	for range 2 {
		if err := <-errs; err == nil {
			t.Fatal("missing acquisition reported success")
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM balance_acquisitions`, 1)
	node := &exactBalanceRPC{hash: ref.Hash}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", node); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	defer client.Close()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "state", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	collector := state.BalanceCollector{Store: cache, Pool: pool, Timeout: time.Second}
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- collector.Run(workerCtx) }()
	demand.Timeout = 5 * time.Second
	values, err := demand.Read(ctx, ref, addresses)
	api := &state.Reader{Canonical: state.PostgresCanonicalSource{DB: db, ChainID: "1"}, BalanceDemand: &demand}
	kind, _, hash, kindErr := api.AccountKind(ctx, account.Hex())
	if kindErr != nil || kind != "eoa" || common.HexToHash(hash) != ref.Hash {
		stop()
		t.Fatalf("account kind=%s hash=%s err=%v", kind, hash, kindErr)
	}
	stop()
	if workerErr := <-done; workerErr != nil {
		t.Fatal(workerErr)
	}
	if err != nil || !slices.Equal(values, []string{"42", "42"}) {
		t.Fatalf("values=%v err=%v", values, err)
	}
	values, err = demand.Read(ctx, ref, addresses)
	if err != nil || !slices.Equal(values, []string{"42", "42"}) || node.calls.Load() != 1 {
		t.Fatalf("replay=%v calls=%d err=%v", values, node.calls.Load(), err)
	}
	if _, _, _, err = api.AccountKind(ctx, account.Hex()); err != nil || node.codes.Load() != 1 {
		t.Fatalf("offline account read err=%v calls=%d", err, node.codes.Load())
	}

	var chain, height pgtype.Numeric
	if err := chain.Scan("1"); err != nil {
		t.Fatal(err)
	}
	if err := height.Scan("0"); err != nil {
		t.Fatal(err)
	}
	open := func() *chaininput.Session {
		session, err := chaininput.Open(ctx, db, chain, height, ref.Hash, "integration@1")
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	selector := rpc.BlockNumberOrHashWithHash(ref.Hash, true)
	var code hexutil.Bytes
	for range 2 {
		if err := open().CallContext(ctx, &code, "eth_getCode", account, selector); !errors.Is(err, chaininput.ErrPending) {
			t.Fatalf("first probe = %v", err)
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM stage_state_inputs`, 1)
	q := dbgen.New(db)
	abandoned, err := q.StageInputClaim(ctx, 10, chain)
	if err != nil {
		t.Fatal(err)
	}
	collectorCtx, finish := context.WithCancel(ctx)
	unavailable := rpc.NewServer()
	if err := unavailable.RegisterName("eth", &exactBalanceRPC{}); err != nil {
		t.Fatal(err)
	}
	badClient := rpc.DialInProc(unavailable)
	t.Cleanup(badClient.Close)
	stagePool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		{Name: "outage", Client: badClient, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}},
		{Name: "state", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}},
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stage := &syncer.StateInputCollector{DB: db, ChainID: "1", Pool: stagePool, Timeout: time.Second, PollInterval: 10 * time.Millisecond}
	go func() { done <- stage.Run(collectorCtx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := open().CallContext(ctx, &code, "eth_getCode", account, selector)
		if !errors.Is(err, chaininput.ErrPending) || time.Now().After(deadline) {
			finish()
			if workerErr := <-done; workerErr != nil {
				t.Fatal(workerErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := open().CallContext(ctx, &code, "eth_getCode", account, selector); err != nil || node.codes.Load() != 2 {
		t.Fatalf("stage replay = %v calls=%d", err, node.codes.Load())
	}
	changed, err := q.StageInputFinish(ctx, dbgen.StageInputFinishParams{ID: abandoned.ID, LeaseGeneration: abandoned.LeaseGeneration, Endpoint: "state", Outcome: "failed"})
	if err != nil || changed != 0 {
		t.Fatalf("expired lease publication rows=%d error=%v", changed, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM stage_state_inputs WHERE attempts=3 AND outcome='ready'`, 1)
	// A value beyond the acquisition wire budget cannot become a successful
	// empty code observation. The rest of this group stays on its pinned endpoint.
	oversize := common.HexToAddress("0xb16")
	if err := open().CallContext(ctx, &code, "eth_getCode", oversize, selector); !errors.Is(err, chaininput.ErrPending) {
		t.Fatal(err)
	}
	largeCtx, stopLarge := context.WithTimeout(ctx, 4*time.Second)
	go func() { done <- stage.Run(largeCtx) }()
	for largeCtx.Err() == nil {
		err = open().CallContext(ctx, &code, "eth_getCode", oversize, selector)
		if !errors.Is(err, chaininput.ErrPending) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopLarge()
	if workerErr := <-done; workerErr != nil {
		t.Fatal(workerErr)
	}
	if !errors.Is(err, chaininput.ErrUnavailable) {
		t.Fatalf("oversize input = %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM balance_acquisitions`, 0)
}
