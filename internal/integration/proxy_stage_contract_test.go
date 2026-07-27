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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type proxyContractState struct {
	code                 []byte
	implementation       *common.Address
	beacon               *common.Address
	beaconImplementation *common.Address
}

type integrationProxyService struct {
	name      string
	mu        *sync.Mutex
	states    map[string]map[string]proxyContractState
	errors    map[string]error
	callBlock map[string][]string
}

func (service *integrationProxyService) GetCode(
	_ context.Context,
	address common.Address,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	blockHash, err := service.record("eth_getCode", blockReference)
	if err != nil {
		return nil, err
	}
	state := service.states[blockHash][address.String()]
	return hexutil.Bytes(common.CopyBytes(state.code)), nil
}

func (service *integrationProxyService) GetStorageAt(
	_ context.Context,
	address common.Address,
	slot common.Hash,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	blockHash, err := service.record("eth_getStorageAt", blockReference)
	if err != nil {
		return nil, err
	}
	state := service.states[blockHash][address.String()]
	word := make([]byte, common.HashLength)
	if slot == enrich.EIP1967ImplementationSlot && state.implementation != nil {
		copy(word[12:], state.implementation.Bytes())
	}
	if slot == enrich.EIP1967BeaconSlot && state.beacon != nil {
		copy(word[12:], state.beacon.Bytes())
	}
	return hexutil.Bytes(word), nil
}

func (service *integrationProxyService) Call(
	_ context.Context,
	request map[string]any,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	blockHash, err := service.record("eth_call", blockReference)
	if err != nil {
		return nil, err
	}
	addressText, ok := request["to"].(string)
	if !ok || !common.IsHexAddress(addressText) {
		return nil, errors.New("decode proxy integration beacon address")
	}
	address := common.HexToAddress(addressText)
	state := service.states[blockHash][address.String()]
	if state.beaconImplementation == nil {
		return nil, errors.New("proxy integration beacon implementation is missing")
	}
	word := make([]byte, common.HashLength)
	copy(word[12:], state.beaconImplementation.Bytes())
	return hexutil.Bytes(word), nil
}

func (service *integrationProxyService) record(
	method string,
	blockReference rpc.BlockNumberOrHash,
) (string, error) {
	if blockReference.BlockHash == nil || !blockReference.RequireCanonical {
		return "", errors.New("proxy integration RPC was not EIP-1898 canonical")
	}
	blockHash := blockReference.BlockHash.String()
	if err := service.errors[blockHash]; err != nil {
		return "", err
	}
	service.mu.Lock()
	service.callBlock[blockHash] = append(service.callBlock[blockHash], service.name+":"+method)
	service.mu.Unlock()
	return blockHash, nil
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

	proxy := integrationContractAddress(0)
	implementationOne, implementationTwo, implementationThree := testAddress(701), testAddress(702), testAddress(703)
	createdBeaconProxy, beacon, beaconImplementation := testAddress(710), testAddress(711), testAddress(712)
	lateTransactionTarget, lateLogTarget := testAddress(720), testAddress(721)
	blockOne := proxyCreationBundle(t, 1, testHash(70_001), testHash(70_000), testHash(71_001))
	oldTwo := proxyUpgradeBundle(t, 2, testHash(70_002), testHash(70_001), testHash(71_002), proxy)
	newTwo := proxyUpgradeBundle(t, 2, testHash(80_002), testHash(70_001), testHash(81_002), proxy)
	blockThree, err := newIntegrationBundle(integrationBundleOptions{
		Number:     3,
		ParentHash: newTwo.Block.Hash(),
		ExtraData:  []byte("trace-create"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &lateTransactionTarget,
			Logs: []*types.Log{{
				Address: lateLogTarget,
				Topics:  []common.Hash{testHash(88_003)},
				Data:    []byte{},
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "trace-create"},
	})
	if err != nil {
		t.Fatalf("build trace-create bundle: %v", err)
	}
	registerFixtureIdentities(testHash(80_003), blockThree.Block.Hash(), testHash(81_003), blockThree.Block.Transactions()[0].Hash())

	states := map[string]map[string]proxyContractState{
		genesis.Block.Hash().String(): {
			testAddress(2).String(): {code: []byte{0x60, 0xa0}},
			testAddress(1).String(): {code: []byte{0x60, 0xa1}},
		},
		blockOne.Block.Hash().String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationOne},
			implementationOne.String(): {code: []byte{0x60, 0x11}},
		},
		oldTwo.Block.Hash().String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationTwo},
			implementationTwo.String(): {code: []byte{0x60, 0x22}},
		},
		newTwo.Block.Hash().String(): {
			proxy.String():               {code: []byte{0x60, 0x01}, implementation: &implementationThree},
			implementationThree.String(): {code: []byte{0x60, 0x33}},
		},
		blockThree.Block.Hash().String(): {
			createdBeaconProxy.String():   {code: []byte{0x60, 0x03}, beacon: &beacon},
			beacon.String():               {beaconImplementation: &beaconImplementation},
			beaconImplementation.String(): {code: []byte{0x60, 0x44}},
		},
	}
	var callMu sync.Mutex
	calls := make(map[string][]string)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "state-a", states, nil, &callMu, calls),
		proxyStateEndpoint(t, "state-b", states, nil, &callMu, calls),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, genesis, "genesis-predeploy"))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 2, mustBytes(t, genesis.Block.Hash()))

	commitCanonical(t, ctx, repository, blockOne)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, blockOne, "create"))
	assertCanonicalProxyImplementation(t, ctx, db, blockOne, proxy, implementationOne, "eip1967", nil)

	commitCanonical(t, ctx, repository, oldTwo)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, oldTwo, "upgrade-old"))
	assertCanonicalProxyImplementation(t, ctx, db, oldTwo, proxy, implementationTwo, "eip1967", nil)

	applyDerivedReorg(t, ctx, repository, blockOne, []chainbundle.Bundle{oldTwo}, []chainbundle.Bundle{newTwo}, "proxy implementation fork")
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, newTwo, "upgrade-new"))
	assertCanonicalProxyImplementation(t, ctx, db, newTwo, proxy, implementationThree, "eip1967", nil)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 2, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, newTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical`, 1, mustBytes(t, newTwo.Block.Hash()))
	assertOneStateEndpointPerBlock(t, calls)
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, newTwo, "upgrade-new-replay"))

	commitCanonical(t, ctx, repository, blockThree)
	execFixture(t, ctx, db, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		blockThree.Block.Hash().String())
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	blockThreeWord, _ := enrich.ParseWord(blockThree.Block.Hash().String())
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
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND octet_length(code) = 0 AND code_hash <> decode(repeat('00', 32), 'hex')`, 2, mustBytes(t, blockThree.Block.Hash()))

	tracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "trace-create", Client: newIntegrationRPCClient(t, "debug", &proxyTraceService{
			block: blockThree, created: createdBeaconProxy,
		}),
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
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage IN ('proxy','abi')`, 0, mustBytes(t, blockThree.Block.Hash()))

	processOne(t, ctx, worker)
	assertCanonicalProxyImplementation(t, ctx, db, blockThree, createdBeaconProxy, beaconImplementation, "beacon", &beacon)
	processOne(t, ctx, worker)
	if processed, err := worker.ProcessOne(ctx); err != nil || processed {
		t.Fatalf("downstream replay did not quiesce: processed=%t err=%v", processed, err)
	}
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "succeeded")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "succeeded")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1'`, 1, mustBytes(t, blockThree.Block.Hash()))
}

func TestProxyUnavailableMakesDependentABIUnavailableWithoutUnboundResult(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, _ := store.NewPostgresRepository(db)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	block := testBundle(0, testHash(90_000), testHash(0), testHash(91_000), "proxy-unavailable")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		block.Block.Hash().String())
	word, _ := enrich.ParseWord(block.Block.Hash().String())
	rpcError := &integrationRPCError{code: -32602, message: "block hash object is unsupported"}
	states := map[string]map[string]proxyContractState{}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "state-no-1898", states, map[string]error{block.Block.Hash().String(): rpcError}, &sync.Mutex{}, make(map[string][]string)),
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
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi' AND details ? 'unbound'`, 0, mustBytes(t, block.Block.Hash()))
}

func TestProxyPoisonCandidateDoesNotBlockValidProxyInSamePostgresBlock(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, _ := store.NewPostgresRepository(db)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	poison, valid := testAddress(930), testAddress(931)
	poisonImplementation, poisonBeacon, validImplementation := testAddress(932), testAddress(933), testAddress(934)
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number:     0,
		ParentHash: testHash(0),
		ExtraData:  []byte("proxy-poison"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &poison,
			Logs: []*types.Log{{
				Address: valid,
				Topics:  []common.Hash{testHash(93_200)},
				Data:    []byte{},
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "proxy-poison"},
	})
	if err != nil {
		t.Fatalf("build mixed proxy bundle: %v", err)
	}
	registerFixtureIdentities(testHash(93_000), block.Block.Hash(), testHash(93_100), block.Block.Transactions()[0].Hash())
	commitCanonical(t, ctx, repository, block)
	states := map[string]map[string]proxyContractState{
		block.Block.Hash().String(): {
			poison.String(): {
				code: []byte{0x60, 0x91}, implementation: &poisonImplementation, beacon: &poisonBeacon,
			},
			valid.String():               {code: []byte{0x60, 0x92}, implementation: &validImplementation},
			validImplementation.String(): {code: []byte{0x60, 0x93}},
		},
	}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "state-mixed", states, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	processor, _ := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	result, err := processor.Process(ctx, proxyJob(t, block, "poison-mixed"))
	if err != nil || result.State != enrich.ResultComplete || result.Details["rejected_candidates"] != "1" || result.Details["proxies"] != "1" {
		t.Fatalf("mixed proxy result=%+v err=%v", result, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 3, mustBytes(t, block.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND proxy_address = $2 AND canonical`, 0, mustBytes(t, block.Block.Hash()), mustBytes(t, poison))
	assertCanonicalProxyImplementation(t, ctx, db, block, valid, validImplementation, "eip1967", nil)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy' AND state = 'complete'`, 1, mustBytes(t, block.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@1' AND canonical`, 1, mustBytes(t, block.Block.Hash()))
}

func proxyStateEndpoint(
	t *testing.T,
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
		Name: name,
		Client: newIntegrationRPCClient(t, "eth", &integrationProxyService{
			name: name, states: states, errors: errorsByBlock, mu: mu, callBlock: calls,
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}
}

func proxyCreationBundle(t *testing.T, number uint64, hash, parent, transaction common.Hash) chainbundle.Bundle {
	t.Helper()
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: []byte("proxy-create"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, ContractCreation: true,
			Data: transaction.Bytes(),
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "proxy-create"},
	})
	if err != nil {
		t.Fatalf("build proxy creation bundle: %v", err)
	}
	registerFixtureIdentities(hash, bundle.Block.Hash(), transaction, bundle.Block.Transactions()[0].Hash())
	return bundle
}

func proxyUpgradeBundle(t *testing.T, number uint64, hash, parent, transaction common.Hash, proxy common.Address) chainbundle.Bundle {
	t.Helper()
	topic := enrich.SignatureHash("Upgraded(address)")
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: []byte("proxy-upgrade"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &proxy,
			Data: transaction.Bytes(),
			Logs: []*types.Log{{
				Address: proxy,
				Topics:  []common.Hash{topic},
				Data:    make([]byte, common.HashLength),
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "proxy-upgrade"},
	})
	if err != nil {
		t.Fatalf("build proxy upgrade bundle: %v", err)
	}
	registerFixtureIdentities(hash, bundle.Block.Hash(), transaction, bundle.Block.Transactions()[0].Hash())
	return bundle
}

func proxyJob(t *testing.T, block chainbundle.Bundle, suffix string) enrich.Job {
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
	block chainbundle.Bundle,
	proxy, implementation common.Address,
	kind string,
	beacon *common.Address,
) {
	t.Helper()
	var gotImplementation, gotBeacon []byte
	var gotKind string
	if err := db.QueryRowContext(ctx, `
		SELECT implementation_address, beacon_address, proxy_kind
		FROM proxy_observations
		WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2 AND canonical`,
		mustBytes(t, proxy), mustBytes(t, block.Block.Hash()),
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

func assertStageDetail(t *testing.T, ctx context.Context, db *sql.DB, block common.Hash, stage, key, want string) {
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

type proxyTraceService struct {
	block   chainbundle.Bundle
	created common.Address
}

func (service *proxyTraceService) TraceTransaction(
	_ context.Context,
	_ common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	transaction := service.block.Block.Transactions()[0]
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		return nil, fmt.Errorf("recover proxy trace sender: %w", err)
	}
	to := transaction.To()
	if to == nil {
		return nil, errors.New("proxy trace root transaction is contract creation")
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from.String(), "to": to.String(),
		"value": fmt.Sprintf("0x%x", transaction.Value()), "gas": "0x5208", "gasUsed": "0x100",
		"input": fmt.Sprintf("0x%x", transaction.Data()), "output": "0x",
		"calls": []any{map[string]any{
			"type": "CREATE2", "from": to.String(), "to": service.created.String(),
			"value": "0x0", "gas": "0x1000", "gasUsed": "0x80", "input": "0x6000", "output": "0x6003",
		}},
	})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
