//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	createdBeaconProxy, createdBeaconProxyTwo := testAddress(710), testAddress(713)
	beacon, beaconImplementation := testAddress(711), testAddress(712)
	lateTransactionTarget, lateLogTarget := testAddress(720), testAddress(721)
	blockOne := proxyCreationBundle(t, 1, testHash(70_001), testHash(70_000), testHash(71_001), proxy)
	oldTwo := proxyUpgradeBundle(t, 2, testHash(70_002), testHash(70_001), testHash(71_002), proxy, implementationTwo)
	newTwo := proxyUpgradeBundle(
		t, 2, testHash(80_002), testHash(70_001), testHash(81_002), proxy,
		implementationTwo, implementationThree,
	)
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
			createdBeaconProxy.String():    {code: []byte{0x60, 0x03}, beacon: &beacon},
			createdBeaconProxyTwo.String(): {code: []byte{0x60, 0x04}, beacon: &beacon},
			beacon.String():                {code: []byte{0x60, 0x43}, beaconImplementation: &beaconImplementation},
			beaconImplementation.String():  {code: []byte{0x60, 0x44}},
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
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	durableProxyWorker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "proxy-history", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProxyProcessComplete(t, ctx, processor, proxyJob(t, genesis, "genesis-predeploy"))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 2, mustBytes(t, genesis.Block.Hash()))

	commitCanonical(t, ctx, repository, blockOne)
	blockOneProxyJob := runDurableProxyBlock(t, ctx, db, queue, durableProxyWorker, blockOne)
	assertPublishedGeneration(t, ctx, db, blockOneProxyJob.Job.ID, 1)
	assertPublishedProxyObservationGeneration(t, ctx, db, blockOne, proxy, blockOneProxyJob.Job.ID, 1, true)
	assertCanonicalProxyImplementation(t, ctx, db, blockOne, proxy, implementationOne, "eip1967", nil)
	assertProxyInitializationEvent(t, ctx, db, blockOne, proxy, 1, true)
	assertStageDetail(t, ctx, db, blockOne.Block.Hash(), "proxy", "history_coverage", "event_only")

	commitCanonical(t, ctx, repository, oldTwo)
	oldTwoProxyJob := runDurableProxyBlock(t, ctx, db, queue, durableProxyWorker, oldTwo)
	assertPublishedGeneration(t, ctx, db, oldTwoProxyJob.Job.ID, 1)
	assertPublishedProxyObservationGeneration(t, ctx, db, oldTwo, proxy, oldTwoProxyJob.Job.ID, 1, true)
	assertCanonicalProxyImplementation(t, ctx, db, oldTwo, proxy, implementationTwo, "eip1967", nil)
	assertProxyUpgradeEvent(t, ctx, db, oldTwo, proxy, implementationTwo, true)
	assertProxyInitializationEvent(t, ctx, db, oldTwo, proxy, 2, true)

	applyDerivedReorg(t, ctx, repository, blockOne, []chainbundle.Bundle{oldTwo}, []chainbundle.Bundle{newTwo}, "proxy implementation fork")
	assertPublishedGeneration(t, ctx, db, oldTwoProxyJob.Job.ID, 1)
	assertPublishedProxyObservationGeneration(t, ctx, db, oldTwo, proxy, oldTwoProxyJob.Job.ID, 1, false)
	newTwoProxyJob := runDurableProxyBlock(t, ctx, db, queue, durableProxyWorker, newTwo)
	assertPublishedGeneration(t, ctx, db, newTwoProxyJob.Job.ID, 1)
	assertPublishedProxyObservationGeneration(t, ctx, db, newTwo, proxy, newTwoProxyJob.Job.ID, 1, true)
	assertCanonicalProxyImplementation(t, ctx, db, newTwo, proxy, implementationThree, "eip1967", nil)
	assertProxyUpgradeEvent(t, ctx, db, oldTwo, proxy, implementationTwo, false)
	assertProxyUpgradeEvent(t, ctx, db, newTwo, proxy, implementationTwo, true)
	assertProxyUpgradeEvent(t, ctx, db, newTwo, proxy, implementationThree, true)
	assertProxyInitializationEvent(t, ctx, db, oldTwo, proxy, 2, false)
	assertProxyInitializationEvent(t, ctx, db, newTwo, proxy, 2, true)
	assertProxyInitializationEvent(t, ctx, db, newTwo, proxy, 3, true)
	assertProxyOrderedTimeline(t, ctx, db, newTwo, proxy, []proxyTimelineEvent{
		{kind: "upgrade", logIndex: 0, value: implementationTwo.Hex()},
		{kind: "initialization", logIndex: 1, value: "2"},
		{kind: "upgrade", logIndex: 2, value: implementationThree.Hex()},
		{kind: "initialization", logIndex: 3, value: "3"},
	})
	assertProxyInitializationImplementations(t, ctx, db, newTwo, proxy, []proxyInitializationImplementation{
		{version: "2", logIndex: 1, implementation: implementationTwo},
		{version: "3", logIndex: 3, implementation: implementationThree},
	})
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical = FALSE`, 2, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@2' AND canonical = FALSE`, 1, mustBytes(t, oldTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1`, 1, mustBytes(t, newTwo.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@2' AND canonical`, 1, mustBytes(t, newTwo.Block.Hash()))
	assertOneStateEndpointPerBlock(t, calls)
	replayedNewTwo, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: newTwo.Block.Hash(), BlockNumber: 2,
		Replay: enrich.ReplaySource{Kind: "fixture", Key: "upgrade-new-replay"},
	})
	if err != nil || !replayedNewTwo.Replayed {
		t.Fatalf("request durable proxy replay: result=%+v err=%v", replayedNewTwo, err)
	}
	processOne(t, ctx, durableProxyWorker)
	assertJobStatus(t, ctx, db, replayedNewTwo.Job.ID, "succeeded")
	assertPublishedGeneration(t, ctx, db, replayedNewTwo.Job.ID, 2)
	assertPublishedProxyObservationGeneration(t, ctx, db, newTwo, proxy, replayedNewTwo.Job.ID, 2, true)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE durable_job_id = $1 AND job_generation = 1`, 0, replayedNewTwo.Job.ID)

	commitCanonical(t, ctx, repository, blockThree)
	execFixture(t, ctx, db, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		blockThree.Block.Hash().String())
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
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "history_coverage", "event_only")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "trace_coverage", "missing")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "state_diff_coverage", "missing")
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "succeeded")
	assertStageDetail(t, ctx, db, blockThreeWord, "abi", "proxy_dependency", "complete")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND octet_length(code) = 0 AND code_hash <> decode(repeat('00', 32), 'hex')`, 2, mustBytes(t, blockThree.Block.Hash()))

	tracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "trace-create", Client: newIntegrationRPCClient(t, "debug", &proxyTraceService{
			block: blockThree, created: []common.Address{createdBeaconProxy, createdBeaconProxyTwo},
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
	traceJobResult, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: blockThreeWord, BlockNumber: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	traceWorker, err := enrich.NewWorker(queue, []enrich.Processor{traceProcessor}, enrich.WorkerOptions{
		ID: "trace-proxy-dependency", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, traceWorker)
	assertJobStatus(t, ctx, db, traceJobResult.Job.ID, "succeeded")
	assertStageDetail(t, ctx, db, blockThreeWord, "trace", "proxy_requeued", "true")
	assertStageDetail(t, ctx, db, blockThreeWord, "trace", "abi_requeued", "true")
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "queued")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "queued")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage IN ('proxy','abi')`, 0, mustBytes(t, blockThree.Block.Hash()))
	stateDiffJobResult, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: blockThreeWord, BlockNumber: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	stateDiffProcessor, err := enrich.NewStateDiffRPCProcessor(db, tracePool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	stateDiffWorker, err := enrich.NewWorker(queue, []enrich.Processor{stateDiffProcessor}, enrich.WorkerOptions{
		ID: "state-diff-proxy-coverage", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, stateDiffWorker)
	assertJobStatus(t, ctx, db, stateDiffJobResult.Job.ID, "succeeded")
	assertStageDetail(t, ctx, db, blockThreeWord, "state_diff", "trace_requeued", "true")

	// StateDiff withdrew the first Trace generation. Rebuild Trace before Proxy
	// so CREATE attribution remains tied to the current execution evidence.
	processOne(t, ctx, traceWorker)
	assertJobStatus(t, ctx, db, traceJobResult.Job.ID, "succeeded")
	processOne(t, ctx, worker)
	assertCanonicalProxyImplementation(t, ctx, db, blockThree, createdBeaconProxy, beaconImplementation, "beacon", &beacon)
	assertCanonicalProxyImplementation(t, ctx, db, blockThree, createdBeaconProxyTwo, beaconImplementation, "beacon", &beacon)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM beacon_implementation_observations
		WHERE chain_id = 1 AND block_hash = $1 AND beacon_address = $2
		  AND stage_version = 2 AND canonical`, 1,
		mustBytes(t, blockThree.Block.Hash()), beacon.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM beacon_observation_generations AS witness
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		WHERE witness.chain_id = 1 AND witness.beacon_address = $1
		  AND witness.observation_block_hash = $2`, 1,
		beacon.Bytes(), mustBytes(t, blockThree.Block.Hash()))
	assertOneEthCall(t, calls[blockThree.Block.Hash().String()])
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "history_coverage", "complete")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "trace_coverage", "complete")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "state_diff_coverage", "complete")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "trace_job_id", traceJobResult.Job.ID)
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "trace_job_generation", "2")
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "state_diff_job_id", stateDiffJobResult.Job.ID)
	assertStageDetail(t, ctx, db, blockThreeWord, "proxy", "state_diff_job_generation", "1")
	processOne(t, ctx, worker)
	if processed, err := worker.ProcessOne(ctx); err != nil || processed {
		t.Fatalf("downstream replay did not quiesce: processed=%t err=%v", processed, err)
	}
	assertJobStatus(t, ctx, db, proxyJobResult.Job.ID, "succeeded")
	assertJobStatus(t, ctx, db, abiJob.Job.ID, "succeeded")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@2'`, 1, mustBytes(t, blockThree.Block.Hash()))
}

func TestProxyStageIgnoresLegitimateZeroAddressTarget(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	zero := common.Address{}
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number: 0, ParentHash: testHash(0), ExtraData: []byte("zero-address-call"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &zero, Data: []byte{0x12, 0x34},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "zero-address-call"},
	})
	if err != nil {
		t.Fatalf("build zero-address call block: %v", err)
	}
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = clock_timestamp()`)
	reference := mustBlockRef(t, block)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(
			t, "zero-address-call", map[string]map[string]proxyContractState{
				reference.Hash.String(): {},
			}, nil, &sync.Mutex{}, make(map[string][]string),
		),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "zero-address-proxy", LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1)
	assertStageDetail(t, ctx, db, word, "proxy", "outcome", "complete")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2`, 0,
		reference.Hash.Bytes(), zero.Bytes())
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
	poison, valid, oversized := testAddress(930), testAddress(931), testAddress(935)
	poisonImplementation, poisonBeacon, validImplementation := testAddress(932), testAddress(933), testAddress(934)
	oversizedImplementation := testAddress(936)
	oversizedRuntime := append(cloneRuntime(oversizedImplementation), make([]byte, enrich.MaxCloneImmutableArgs+1)...)
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number:     0,
		ParentHash: testHash(0),
		ExtraData:  []byte("proxy-poison"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &poison,
			Logs: []*types.Log{
				{Address: valid, Topics: []common.Hash{testHash(93_200)}, Data: []byte{}},
				{Address: oversized, Topics: []common.Hash{testHash(93_201)}, Data: []byte{}},
			},
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
			oversized.String():           {code: oversizedRuntime},
		},
	}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "state-mixed", states, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	processor, _ := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	result, err := processor.Process(ctx, proxyJob(t, block, "poison-mixed"))
	if err != nil || result.State != enrich.ResultComplete || result.Details["rejected_candidates"] != "2" || result.Details["proxies"] != "1" {
		t.Fatalf("mixed proxy result=%+v err=%v", result, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM contract_code_observations WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 4, mustBytes(t, block.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_observations WHERE chain_id = 1 AND block_hash = $1 AND proxy_address = $2 AND canonical`, 0, mustBytes(t, block.Block.Hash()), mustBytes(t, poison))
	assertCanonicalProxyImplementation(t, ctx, db, block, valid, validImplementation, "eip1967", nil)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND block_hash = $1 AND stage_version = 2 AND canonical
		  AND ((address = $2 AND reason = 'ambiguous_slots') OR
		       (address = $3 AND reason = 'immutable_args_too_large'))`, 2,
		mustBytes(t, block.Block.Hash()), poison.Bytes(), oversized.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_stage_results WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy' AND state = 'complete'`, 1, mustBytes(t, block.Block.Hash()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM block_journals WHERE chain_id = 1 AND block_hash = $1 AND stage = 'proxy@2' AND canonical`, 1, mustBytes(t, block.Block.Hash()))
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

func proxyCreationBundle(
	t *testing.T,
	number uint64,
	hash, parent, transaction common.Hash,
	proxy common.Address,
) chainbundle.Bundle {
	t.Helper()
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: []byte("proxy-create"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, ContractCreation: true,
			Data: transaction.Bytes(),
			Logs: []*types.Log{{
				Address: proxy,
				Topics:  []common.Hash{enrich.SignatureHash("Initialized(uint64)")},
				Data:    initializedVersionWord(1),
			}},
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

func proxyUpgradeBundle(
	t *testing.T,
	number uint64,
	hash, parent, transaction common.Hash,
	proxy common.Address,
	implementations ...common.Address,
) chainbundle.Bundle {
	t.Helper()
	if len(implementations) == 0 {
		t.Fatal("proxy upgrade bundle requires at least one implementation")
	}
	topic := enrich.SignatureHash("Upgraded(address)")
	logs := make([]*types.Log, 0, len(implementations)*2)
	for index, implementation := range implementations {
		logs = append(logs,
			&types.Log{
				Address: proxy,
				Topics:  []common.Hash{topic, common.BytesToHash(implementation.Bytes())},
				Data:    []byte{},
			},
			&types.Log{
				Address: proxy,
				Topics:  []common.Hash{enrich.SignatureHash("Initialized(uint64)")},
				Data:    initializedVersionWord(uint64(index + 2)),
			},
		)
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: []byte("proxy-upgrade"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &proxy,
			Data: transaction.Bytes(),
			Logs: logs,
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

type proxyTimelineEvent struct {
	kind     string
	logIndex int64
	value    string
}

func assertProxyOrderedTimeline(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	contract common.Address,
	want []proxyTimelineEvent,
) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT kind, log_index, target_address, version, transaction_hash
		FROM (
			SELECT 'upgrade'::text AS kind, log_index, target_address,
			       NULL::text AS version, transaction_hash
			FROM proxy_upgrade_events
			WHERE chain_id = 1 AND block_hash = $1 AND emitter_address = $2
			  AND stage_version = 2 AND canonical
			UNION ALL
			SELECT 'initialization'::text AS kind, log_index, NULL::bytea AS target_address,
			       version::text, transaction_hash
			FROM proxy_initialization_events
			WHERE chain_id = 1 AND block_hash = $1 AND contract_address = $2
			  AND stage_version = 2 AND canonical
		) AS timeline
		ORDER BY log_index`, mustBytes(t, block.Block.Hash()), contract.Bytes())
	if err != nil {
		t.Fatalf("query proxy event timeline: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	wantTransaction := block.Block.Transactions()[0].Hash()
	index := 0
	for rows.Next() {
		var kind string
		var logIndex int64
		var target, transactionHash []byte
		var version sql.NullString
		if err := rows.Scan(&kind, &logIndex, &target, &version, &transactionHash); err != nil {
			t.Fatalf("scan proxy event timeline: %v", err)
		}
		if index >= len(want) {
			t.Fatalf("proxy event timeline contains unexpected event kind=%s log=%d", kind, logIndex)
		}
		value := version.String
		if kind == "upgrade" {
			value = common.BytesToAddress(target).Hex()
		}
		if got := (proxyTimelineEvent{kind: kind, logIndex: logIndex, value: value}); got != want[index] {
			t.Fatalf("proxy event timeline[%d]=%+v want=%+v", index, got, want[index])
		}
		if string(transactionHash) != string(wantTransaction.Bytes()) {
			t.Fatalf("proxy event timeline[%d] transaction=%x want=%s", index, transactionHash, wantTransaction)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate proxy event timeline: %v", err)
	}
	if index != len(want) {
		t.Fatalf("proxy event timeline length=%d want=%d", index, len(want))
	}
}

type proxyInitializationImplementation struct {
	version        string
	logIndex       int64
	implementation common.Address
}

func assertProxyInitializationImplementations(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	contract common.Address,
	want []proxyInitializationImplementation,
) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT initialization.version::text, initialization.log_index,
		       preceding_upgrade.target_address,
		       initialization.transaction_hash, preceding_upgrade.transaction_hash
		FROM proxy_initialization_events AS initialization
		JOIN LATERAL (
			SELECT upgrade.target_address, upgrade.transaction_hash
			FROM proxy_upgrade_events AS upgrade
			WHERE upgrade.chain_id = initialization.chain_id
			  AND upgrade.block_hash = initialization.block_hash
			  AND upgrade.emitter_address = initialization.contract_address
			  AND upgrade.stage_version = initialization.stage_version
			  AND upgrade.canonical
			  AND upgrade.log_index < initialization.log_index
			ORDER BY upgrade.log_index DESC
			LIMIT 1
		) AS preceding_upgrade ON TRUE
		WHERE initialization.chain_id = 1
		  AND initialization.block_hash = $1
		  AND initialization.contract_address = $2
		  AND initialization.stage_version = 2
		  AND initialization.canonical
		ORDER BY initialization.log_index`, mustBytes(t, block.Block.Hash()), contract.Bytes())
	if err != nil {
		t.Fatalf("query initialization implementations: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	wantTransaction := block.Block.Transactions()[0].Hash().Bytes()
	index := 0
	for rows.Next() {
		var version string
		var logIndex int64
		var implementation, initializationTransaction, upgradeTransaction []byte
		if err := rows.Scan(
			&version, &logIndex, &implementation,
			&initializationTransaction, &upgradeTransaction,
		); err != nil {
			t.Fatalf("scan initialization implementation: %v", err)
		}
		if index >= len(want) {
			t.Fatalf("initialization implementation contains unexpected version=%s log=%d", version, logIndex)
		}
		got := proxyInitializationImplementation{
			version: version, logIndex: logIndex,
			implementation: common.BytesToAddress(implementation),
		}
		if got != want[index] {
			t.Fatalf("initialization implementation[%d]=%+v want=%+v", index, got, want[index])
		}
		if string(initializationTransaction) != string(wantTransaction) ||
			string(upgradeTransaction) != string(wantTransaction) {
			t.Fatalf(
				"initialization implementation[%d] transaction init=%x upgrade=%x want=%x",
				index, initializationTransaction, upgradeTransaction, wantTransaction,
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate initialization implementations: %v", err)
	}
	if index != len(want) {
		t.Fatalf("initialization implementation length=%d want=%d", index, len(want))
	}
}

func assertProxyUpgradeEvent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	emitter, implementation common.Address,
	canonical bool,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_upgrade_events
		WHERE chain_id = 1 AND block_hash = $1 AND stage_version = 2
		  AND emitter_address = $2 AND event_kind = 'implementation'
		  AND target_address = $3 AND canonical = $4`, 1,
		mustBytes(t, block.Block.Hash()), emitter.Bytes(), implementation.Bytes(), canonical)
}

func assertProxyInitializationEvent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	contract common.Address,
	version uint64,
	canonical bool,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_initialization_events
		WHERE chain_id = 1 AND block_hash = $1 AND stage_version = 2
		  AND contract_address = $2 AND version = $3::numeric
		  AND canonical = $4`, 1,
		mustBytes(t, block.Block.Hash()), contract.Bytes(), fmt.Sprint(version), canonical)
}

func initializedVersionWord(version uint64) []byte {
	word := make([]byte, common.HashLength)
	for index := range 8 {
		word[common.HashLength-1-index] = byte(version)
		version >>= 8
	}
	return word
}

func runDurableProxyBlock(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	queue *enrich.PostgresJobQueue,
	worker *enrich.Worker,
	block chainbundle.Bundle,
) enrich.EnqueueResult {
	t.Helper()
	reference := mustBlockRef(t, block)
	execFixture(t, ctx, db, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1
		  AND topic = 'core.block.canonical'
		  AND message_key = $1
		  AND published_at IS NULL`, reference.Hash.String())
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("enqueue durable Proxy for block %s: %v", reference.Hash, err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
	return enqueued
}

func assertPublishedProxyObservationGeneration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	proxy common.Address,
	jobID string,
	generation int64,
	canonical bool,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observation_generations AS witness
		JOIN proxy_observations AS observation
		  ON observation.chain_id = witness.chain_id
		 AND observation.proxy_address = witness.proxy_address
		 AND observation.block_hash = witness.observation_block_hash
		 AND observation.stage_version = witness.observation_stage_version
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		WHERE witness.chain_id = 1
		  AND witness.proxy_address = $1
		  AND witness.observation_block_hash = $2
		  AND witness.durable_job_id = $3
		  AND witness.job_generation = $4
		  AND observation.canonical = $5`, 1,
		proxy.Bytes(), mustBytes(t, block.Block.Hash()), jobID, generation, canonical)
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
	created []common.Address
}

func (service *proxyTraceService) TraceBlockByHash(
	_ context.Context,
	blockHash common.Hash,
	options map[string]any,
) (json.RawMessage, error) {
	if blockHash != service.block.Block.Hash() {
		return nil, fmt.Errorf("proxy trace block hash = %s, want %s", blockHash, service.block.Block.Hash())
	}
	hashes := make([]common.Hash, len(service.block.Block.Transactions()))
	for index, transaction := range service.block.Block.Transactions() {
		hashes[index] = transaction.Hash()
	}
	if options["tracer"] == "prestateTracer" {
		return marshalIntegrationBlockTraceResults(hashes, func(common.Hash) (json.RawMessage, error) {
			return integrationPrestateTraceResult(
				json.RawMessage(`{"pre":{},"post":{}}`), options,
			)
		})
	}
	transaction := service.block.Block.Transactions()[0]
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		return nil, fmt.Errorf("recover proxy trace sender: %w", err)
	}
	to := transaction.To()
	if to == nil {
		return nil, errors.New("proxy trace root transaction is contract creation")
	}
	calls := make([]any, 0, len(service.created))
	for index, created := range service.created {
		calls = append(calls, map[string]any{
			"type": "CREATE2", "from": to.String(), "to": created.String(),
			"value": "0x0", "gas": "0x1000", "gasUsed": "0x80",
			"input": fmt.Sprintf("0x60%02x", index), "output": fmt.Sprintf("0x60%02x", index+3),
		})
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from.String(), "to": to.String(),
		"value": fmt.Sprintf("0x%x", transaction.Value()), "gas": "0x5208", "gasUsed": "0x100",
		"input": fmt.Sprintf("0x%x", transaction.Data()), "output": "0x",
		"calls": calls,
	})
	if err != nil {
		return nil, err
	}
	return marshalIntegrationBlockTraceResults(hashes, func(common.Hash) (json.RawMessage, error) {
		return encoded, nil
	})
}

func assertOneEthCall(t *testing.T, entries []string) {
	t.Helper()
	got := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry, ":eth_call") {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("RPC eth_call calls=%d want=1 entries=%v", got, entries)
	}
}
