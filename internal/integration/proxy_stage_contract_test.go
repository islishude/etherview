//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type proxyContractState struct {
	code                 []byte
	implementation       *ethrpc.Address
	beacon               *ethrpc.Address
	beaconImplementation *ethrpc.Address
}

type integrationProxyCaller struct {
	name      string
	mu        *sync.Mutex
	states    map[string]map[string]proxyContractState
	errors    map[string]error
	callBlock map[string][]string
}

func (caller *integrationProxyCaller) Call(ctx context.Context, method string, params []any, result any) error {
	if len(params) < 2 {
		return errors.New("proxy integration RPC received too few parameters")
	}
	blockReference, ok := params[len(params)-1].(map[string]any)
	if !ok || blockReference["requireCanonical"] != true {
		return errors.New("proxy integration RPC was not EIP-1898 canonical")
	}
	blockHash, ok := blockReference["blockHash"].(string)
	if !ok {
		return errors.New("proxy integration RPC block hash is absent")
	}
	if err := caller.errors[blockHash]; err != nil {
		return err
	}
	var address ethrpc.Address
	switch method {
	case "eth_getCode", "eth_getStorageAt":
		parsed, err := ethrpc.ParseAddress(params[0].(string))
		if err != nil {
			return err
		}
		address = parsed
	case "eth_call":
		request, ok := params[0].(map[string]any)
		if !ok {
			return errors.New("proxy integration beacon call is invalid")
		}
		parsed, err := ethrpc.ParseAddress(request["to"].(string))
		if err != nil {
			return err
		}
		address = parsed
	default:
		return fmt.Errorf("unexpected proxy integration RPC method %q", method)
	}
	caller.mu.Lock()
	caller.callBlock[blockHash] = append(caller.callBlock[blockHash], caller.name+":"+method)
	caller.mu.Unlock()
	state := caller.states[blockHash][address.String()]
	switch method {
	case "eth_getCode":
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("proxy integration code destination is invalid")
		}
		*destination = ethrpc.DataFromBytes(state.code)
	case "eth_getStorageAt":
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("proxy integration storage destination is invalid")
		}
		word := make([]byte, 32)
		slot := params[1].(string)
		if slot == enrich.EIP1967ImplementationSlot.String() && state.implementation != nil {
			encoded, err := state.implementation.Bytes()
			if err != nil {
				return err
			}
			copy(word[12:], encoded)
		}
		if slot == enrich.EIP1967BeaconSlot.String() && state.beacon != nil {
			encoded, err := state.beacon.Bytes()
			if err != nil {
				return err
			}
			copy(word[12:], encoded)
		}
		*destination = ethrpc.DataFromBytes(word)
	case "eth_call":
		if state.beaconImplementation == nil {
			return errors.New("proxy integration beacon implementation is missing")
		}
		word := make([]byte, 32)
		encoded, err := state.beaconImplementation.Bytes()
		if err != nil {
			return err
		}
		copy(word[12:], encoded)
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("proxy integration beacon destination is invalid")
		}
		*destination = ethrpc.DataFromBytes(word)
	}
	return nil
}

func TestProxyStageCreationUpgradeBeaconDependencyAndReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	genesis := testBundle(0, testHash(70_000), testHash(0), testHash(71_000), "proxy-genesis")
	commitCanonical(t, ctx, repository, genesis)

	proxy := testAddress(700)
	implementationOne, implementationTwo, implementationThree := testAddress(701), testAddress(702), testAddress(703)
	createdBeaconProxy, beacon, beaconImplementation := testAddress(710), testAddress(711), testAddress(712)
	lateTransactionTarget, lateLogTarget := testAddress(720), testAddress(721)
	blockOne := proxyCreationBundle(t, 1, testHash(70_001), testHash(70_000), testHash(71_001), proxy)
	oldTwo := proxyUpgradeBundle(t, 2, testHash(70_002), testHash(70_001), testHash(71_002), proxy)
	newTwo := proxyUpgradeBundle(t, 2, testHash(80_002), testHash(70_001), testHash(81_002), proxy)
	blockThree := testBundle(3, testHash(80_003), testHash(80_002), testHash(81_003), "trace-create")
	blockThree.Block.Transactions[0].Transaction.To = &lateTransactionTarget
	blockThree.Receipts[0].To = &lateTransactionTarget
	blockThree.Receipts[0].Logs[0].Address = lateLogTarget

	states := map[string]map[string]proxyContractState{
		genesis.Block.Hash.String(): {
			testAddress(2).String(): {code: []byte{0x60, 0xa0}},
			testAddress(3).String(): {code: []byte{0x60, 0xa1}},
		},
		blockOne.Block.Hash.String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationOne},
			implementationOne.String(): {code: []byte{0x60, 0x11}},
		},
		oldTwo.Block.Hash.String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationTwo},
			implementationTwo.String(): {code: []byte{0x60, 0x22}},
		},
		newTwo.Block.Hash.String(): {
			proxy.String():               {code: []byte{0x60, 0x01}, implementation: &implementationThree},
			implementationThree.String(): {code: []byte{0x60, 0x33}},
		},
		blockThree.Block.Hash.String(): {
			createdBeaconProxy.String():   {code: []byte{0x60, 0x03}, beacon: &beacon},
			beacon.String():               {beaconImplementation: &beaconImplementation},
			beaconImplementation.String(): {code: []byte{0x60, 0x44}},
		},
	}
	var callMu sync.Mutex
	calls := make(map[string][]string)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint("state-a", states, nil, &callMu, calls),
		proxyStateEndpoint("state-b", states, nil, &callMu, calls),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, genesis, "genesis-predeploy"))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 2, mustBytes(t, genesis.Block.Hash))

	commitCanonical(t, ctx, repository, blockOne)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, blockOne, "create"))
	assertCanonicalProxyImplementation(t, ctx, db, blockOne, proxy, implementationOne, "eip1967", nil)

	commitCanonical(t, ctx, repository, oldTwo)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, oldTwo, "upgrade-old"))
	assertCanonicalProxyImplementation(t, ctx, db, oldTwo, proxy, implementationTwo, "eip1967", nil)

	applyDerivedReorg(t, ctx, repository, blockOne, []ethrpc.Bundle{oldTwo}, []ethrpc.Bundle{newTwo}, "proxy implementation fork")
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, newTwo, "upgrade-new"))
	assertCanonicalProxyImplementation(t, ctx, db, newTwo, proxy, implementationThree, "eip1967", nil)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 2, mustBytes(t, oldTwo.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, newTwo.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical`, 1, mustBytes(t, newTwo.Block.Hash))
	assertOneStateEndpointPerBlock(t, calls)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, newTwo, "upgrade-new-replay"))

	commitCanonical(t, ctx, repository, blockThree)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	blockThreeWord, _ := enrich.ParseWord(blockThree.Block.Hash.String())
	abiJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ABIStage, ChainID: "1", BlockHash: blockThreeWord, BlockNumber: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyJobResult, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: blockThreeWord, BlockNumber: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	abiProcessor, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{abiProcessor, processor}, enrich.WorkerOptions{
		ID: "proxy-dependency", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "succeeded")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "queued")
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "succeeded")
	assertStageDetail(t, ctx, db, blockThreeWord, "abi", "proxy_dependency", "complete")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND octet_length(code) = 0 AND code_hash <> decode(repeat('00', 32), 'hex')`, 2, mustBytes(t, blockThree.Block.Hash))

	tracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "trace-create", Client: proxyTraceCaller{block: blockThree, created: createdBeaconProxy},
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	traceProcessor, err := enrich.NewTraceRPCProcessor(db, tracePool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	traceJob := proxyJob(t, blockThree, "trace-create")
	traceJob.Stage = enrich.TraceStage
	if result, err := traceProcessor.Process(ctx, traceJob); err != nil || result.Details["proxy_requeued"] != "true" || result.Details["abi_requeued"] != "true" {
		t.Fatalf("trace downstream replay result=%+v err=%v", result, err)
	}
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "queued")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "queued")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage IN ('proxy','abi')`, 0, mustBytes(t, blockThree.Block.Hash))

	processOne(t, ctx, worker)
	assertCanonicalProxyImplementation(t, ctx, db, blockThree, createdBeaconProxy, beaconImplementation, "beacon", &beacon)
	processOne(t, ctx, worker)
	if processed, err := worker.ProcessOne(ctx); err != nil || processed {
		t.Fatalf("downstream replay did not quiesce: processed=%t err=%v", processed, err)
	}
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "succeeded")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "succeeded")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1'`, 1, mustBytes(t, blockThree.Block.Hash))
}

func TestProxyUnavailableMakesDependentABIUnavailableWithoutUnboundResult(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, _ := store.NewPostgresRepository(db)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	block := testBundle(0, testHash(90_000), testHash(0), testHash(91_000), "proxy-unavailable")
	commitCanonical(t, ctx, repository, block)
	word, _ := enrich.ParseWord(block.Block.Hash.String())
	rpcError := &ethrpc.RPCError{Code: -32602, Message: "block hash object is unsupported"}
	states := map[string]map[string]proxyContractState{}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint("state-no-1898", states, map[string]error{block.Block.Hash.String(): rpcError}, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	proxyProcessor, _ := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	abiProcessor, _ := enrich.NewPostgresABIProcessorWithProxyDependency(db)
	queue, _ := enrich.NewPostgresJobQueue(db)
	abi, _ := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.ABIStage, ChainID: "1", BlockHash: word, BlockNumber: 0, MaxAttempts: 1})
	proxy, _ := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: 0, MaxAttempts: 1})
	worker, _ := enrich.NewWorker(queue, []enrich.Processor{abiProcessor, proxyProcessor}, enrich.WorkerOptions{
		ID: "proxy-unavailable", LeaseDuration: 2 * time.Second,
	})
	processOne(t, ctx, worker)
	assertStageResult(t, ctx, db, proxy.Job, enrich.ResultUnavailable, "eth_getCode cannot serve the exact block-hash state", map[string]string{})
	processOne(t, ctx, worker)
	assertStageResult(t, ctx, db, abi.Job, enrich.ResultUnavailable, "proxy stage is unavailable for this block", map[string]string{})
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi' AND details ? 'unbound'`, 0, mustBytes(t, block.Block.Hash))
}

func TestProxyPoisonCandidateDoesNotBlockValidProxyInSamePostgresBlock(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, _ := store.NewPostgresRepository(db)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	poison, valid := testAddress(930), testAddress(931)
	poisonImplementation, poisonBeacon, validImplementation := testAddress(932), testAddress(933), testAddress(934)
	block := testBundle(0, testHash(93_000), testHash(0), testHash(93_100), "proxy-poison")
	block.Block.Transactions[0].Transaction.To = &poison
	block.Receipts[0].To = &poison
	block.Receipts[0].Logs[0].Address = valid
	commitCanonical(t, ctx, repository, block)
	states := map[string]map[string]proxyContractState{
		block.Block.Hash.String(): {
			poison.String(): {
				code: []byte{0x60, 0x91}, implementation: &poisonImplementation, beacon: &poisonBeacon,
			},
			valid.String():               {code: []byte{0x60, 0x92}, implementation: &validImplementation},
			validImplementation.String(): {code: []byte{0x60, 0x93}},
		},
	}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint("state-mixed", states, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	processor, _ := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	result, err := processor.Process(ctx, proxyJob(t, block, "poison-mixed"))
	if err != nil || result.State != enrich.ResultComplete || result.Details["rejected_candidates"] != "1" || result.Details["proxies"] != "1" {
		t.Fatalf("mixed proxy result=%+v err=%v", result, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 3, mustBytes(t, block.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND proxy_address = $2 AND canonical`, 0, mustBytes(t, block.Block.Hash), mustBytes(t, poison))
	assertCanonicalProxyImplementation(t, ctx, db, block, valid, validImplementation, "eip1967", nil)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy' AND state = 'complete'`, 1, mustBytes(t, block.Block.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical`, 1, mustBytes(t, block.Block.Hash))
}

func proxyStateEndpoint(
	name string,
	states map[string]map[string]proxyContractState,
	errorsByBlock map[string]error,
	mu *sync.Mutex,
	calls map[string][]string,
) ethrpc.Endpoint {
	if errorsByBlock == nil {
		errorsByBlock = make(map[string]error)
	}
	return ethrpc.Endpoint{
		Name:     name,
		Client:   &integrationProxyCaller{name: name, states: states, errors: errorsByBlock, mu: mu, callBlock: calls},
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}
}

func proxyCreationBundle(t *testing.T, number uint64, hash, parent, transaction ethrpc.Hash, proxy ethrpc.Address) ethrpc.Bundle {
	t.Helper()
	bundle := testBundle(number, hash, parent, transaction, "proxy-create")
	bundle.Block.Transactions[0].Transaction.To = nil
	bundle.Receipts[0].To = nil
	bundle.Receipts[0].ContractAddress = &proxy
	bundle.Receipts[0].Logs = nil
	return bundle
}

func proxyUpgradeBundle(t *testing.T, number uint64, hash, parent, transaction ethrpc.Hash, proxy ethrpc.Address) ethrpc.Bundle {
	t.Helper()
	bundle := testBundle(number, hash, parent, transaction, "proxy-upgrade")
	bundle.Block.Transactions[0].Transaction.To = &proxy
	bundle.Receipts[0].To = &proxy
	bundle.Receipts[0].Logs[0].Address = proxy
	topic := enrich.SignatureHash("Upgraded(address)")
	parsed, err := ethrpc.ParseHash(topic.String())
	if err != nil {
		t.Fatal(err)
	}
	bundle.Receipts[0].Logs[0].Topics = []ethrpc.Hash{parsed}
	bundle.Receipts[0].Logs[0].Data = ethrpc.DataFromBytes(make([]byte, 32))
	return bundle
}

func proxyJob(t *testing.T, block ethrpc.Bundle, suffix string) enrich.Job {
	t.Helper()
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	return enrich.Job{ID: "proxy-" + suffix, Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number}
}

func assertProxyProcessComplete(t *testing.T, ctx context.Context, processor *enrich.PostgresProxyProcessor, job enrich.Job) {
	t.Helper()
	result, err := processor.Process(ctx, job)
	if err != nil || result.State != enrich.ResultComplete {
		t.Fatalf("process proxy %s result=%+v err=%v", job.ID, result, err)
	}
}

func assertCanonicalProxyImplementation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block ethrpc.Bundle,
	proxy, implementation ethrpc.Address,
	kind string,
	beacon *ethrpc.Address,
) {
	t.Helper()
	var gotImplementation, gotBeacon []byte
	var gotKind string
	if err := db.QueryRowContext(ctx, `
		SELECT implementation_address, beacon_address, proxy_kind
		FROM proxy_observations
		WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2 AND canonical`,
		mustBytes(t, proxy), mustBytes(t, block.Block.Hash),
	).Scan(&gotImplementation, &gotBeacon, &gotKind); err != nil {
		t.Fatalf("query proxy observation: %v", err)
	}
	if string(gotImplementation) != string(mustBytes(t, implementation)) || gotKind != kind {
		t.Fatalf("proxy kind=%s implementation=%x", gotKind, gotImplementation)
	}
	if beacon == nil && len(gotBeacon) != 0 || beacon != nil && string(gotBeacon) != string(mustBytes(t, *beacon)) {
		t.Fatalf("proxy beacon=%x want=%v", gotBeacon, beacon)
	}
}

func assertOneStateEndpointPerBlock(t *testing.T, calls map[string][]string) {
	t.Helper()
	for block, entries := range calls {
		seen := make(map[string]bool)
		for _, entry := range entries {
			for index := range entry {
				if entry[index] == ':' {
					seen[entry[:index]] = true
					break
				}
			}
		}
		if len(seen) != 1 {
			t.Fatalf("block %s used state endpoints %v: %v", block, seen, entries)
		}
	}
}

func processOne(t *testing.T, ctx context.Context, worker *enrich.Worker) {
	t.Helper()
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process durable enrichment job=%t err=%v", processed, err)
	}
}

func assertJobStatus(t *testing.T, ctx context.Context, db *sql.DB, id, want string) {
	t.Helper()
	var got string
	var lastError sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT status, last_error FROM durable_jobs WHERE id = $1`, id).Scan(&got, &lastError); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("job %s status=%s want=%s last_error=%q", id, got, want, lastError.String)
	}
}

func assertStageDetail(t *testing.T, ctx context.Context, db *sql.DB, block enrich.Word, stage, key, want string) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(ctx, `
		SELECT details->>$3
		FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = $2`, block[:], stage, key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("stage %s detail %s=%q want=%q", stage, key, got, want)
	}
}

type proxyTraceCaller struct {
	block   ethrpc.Bundle
	created ethrpc.Address
}

func (caller proxyTraceCaller) Call(_ context.Context, method string, _ []any, result any) error {
	if method != "debug_traceTransaction" {
		return fmt.Errorf("unexpected trace method %q", method)
	}
	transaction := caller.block.Block.Transactions[0].Transaction
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": transaction.From.String(), "to": transaction.To.String(),
		"value": transaction.Value.String(), "gas": "0x5208", "gasUsed": "0x100",
		"input": transaction.Input.String(), "output": "0x",
		"calls": []any{map[string]any{
			"type": "CREATE2", "from": transaction.To.String(), "to": caller.created.String(),
			"value": "0x0", "gas": "0x1000", "gasUsed": "0x80", "input": "0x6000", "output": "0x6003",
		}},
	})
	if err != nil {
		return err
	}
	destination, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("proxy trace result destination is invalid")
	}
	*destination = encoded
	return nil
}
