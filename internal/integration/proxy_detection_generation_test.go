//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestProxyDetectionGenerationPromotesPublishedImmutableCloneEvidence(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	candidate, implementation := integrationContractAddress(0), testAddress(9_800)
	immutableArgs := []byte("generation-exact-clone")
	runtime := append(cloneRuntime(implementation), immutableArgs...)
	initcode := openZeppelinImmutableCloneInitcode(runtime)
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number:     0,
		ParentHash: testHash(0),
		ExtraData:  []byte("proxy-detection-generation"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, ContractCreation: true, Data: initcode,
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "proxy-detection-generation"},
	})
	if err != nil {
		t.Fatalf("build proxy detection generation bundle: %v", err)
	}
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1
		  AND topic = 'core.block.canonical'
		  AND message_key = $1`, block.Block.Hash().String())

	states := map[string]map[string]proxyContractState{
		block.Block.Hash().String(): {
			candidate.String(): {code: runtime},
		},
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(
			t, "proxy-detection-generation", states, nil,
			&sync.Mutex{}, make(map[string][]string),
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
	reference := mustBlockRef(t, block)
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

	first, found, err := queue.Claim(
		ctx, "proxy-detection-generation-one", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || first.Job.Generation != 1 {
		t.Fatalf("claim proxy detection generation one: lease=%+v found=%t err=%v", first, found, err)
	}
	assertProxyDetectionGenerationRejected(
		t, ctx, db, enqueued.Job.ID, 2, reference, testAddress(9_801),
	)
	result, err := processor.ProcessLease(ctx, first, queue)
	if err != nil || result.State != enrich.ResultComplete ||
		result.Details["rejected_candidates"] != "1" || result.Details["proxies"] != "0" {
		t.Fatalf("publish proxy detection generation one: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_detection_evidence
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND durable_job_id = $3 AND job_generation = 1
		  AND detection_state = 'rejected'
		  AND reason = 'immutable_args_creation_unverified'
		  AND canonical`, 1, reference.Hash.Bytes(), candidate.Bytes(), enqueued.Job.ID)
	assertCurrentProxyDetectionGeneration(
		t, ctx, db, reference, candidate, enqueued.Job.ID, 1,
	)

	creationTrace := rootImmutableCloneCreationTrace(
		t, block.Block.Transactions()[0], candidate, runtime,
	)
	traceJob := publishCloneTrace(t, ctx, db, queue, reference, creationTrace)
	assertCurrentProxyDetectionGeneration(
		t, ctx, db, reference, candidate, enqueued.Job.ID, 0,
	)

	second, found, err := queue.Claim(
		ctx, "proxy-detection-generation-two", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || second.Job.Generation != 2 {
		t.Fatalf("claim proxy detection generation two: lease=%+v found=%t err=%v", second, found, err)
	}
	assertProxyDetectionGenerationRejected(
		t, ctx, db, enqueued.Job.ID, 1, reference, testAddress(9_802),
	)
	result, err = processor.ProcessLease(ctx, second, queue)
	if err != nil || result.State != enrich.ResultComplete ||
		result.Details["rejected_candidates"] != "0" || result.Details["proxies"] != "1" {
		t.Fatalf("publish proxy detection generation two: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 2)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_detection_evidence
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND durable_job_id = $3 AND job_generation = 1
		  AND detection_state = 'rejected'
		  AND reason = 'immutable_args_creation_unverified'
		  AND canonical`, 1, reference.Hash.Bytes(), candidate.Bytes(), enqueued.Job.ID)
	assertCurrentProxyDetectionGeneration(
		t, ctx, db, reference, candidate, enqueued.Job.ID, 0,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_detection_evidence AS evidence
		JOIN published_block_stage_results AS published
		  ON published.chain_id = evidence.chain_id
		 AND published.block_hash = evidence.block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = evidence.stage_version
		 AND published.durable_job_id = evidence.durable_job_id
		 AND published.job_generation = evidence.job_generation
		WHERE evidence.chain_id = 1 AND evidence.block_hash = $1
		  AND evidence.address = $2 AND evidence.durable_job_id = $3
		  AND evidence.job_generation = 1`, 0,
		reference.Hash.Bytes(), candidate.Bytes(), enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observations AS observation
		JOIN proxy_observation_generations AS witness
		  ON witness.chain_id = observation.chain_id
		 AND witness.proxy_address = observation.proxy_address
		 AND witness.observation_block_hash = observation.block_hash
		 AND witness.observation_stage_version = observation.stage_version
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		 AND published.state = 'complete'
		WHERE observation.chain_id = 1 AND observation.block_hash = $1
		  AND observation.proxy_address = $2 AND observation.proxy_kind = 'eip1167'
		  AND observation.proxy_pattern = 'clone' AND observation.evidence_state = 'exact'
		  AND observation.implementation_address = $3
		  AND observation.implementation_code_hash = $4
		  AND observation.immutable_args = $5
		  AND observation.details->>'minimal_runtime' = 'openzeppelin_immutable_args'
		  AND observation.details->>'immutable_args_creation_authenticated' = 'true'
		  AND witness.durable_job_id = $6 AND witness.job_generation = 2`, 1,
		reference.Hash.Bytes(), candidate.Bytes(), implementation.Bytes(),
		crypto.Keccak256Hash(nil).Bytes(), immutableArgs, enqueued.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_code_observations
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND code_hash = $3 AND octet_length(code) = 0 AND canonical`, 1,
		reference.Hash.Bytes(), implementation.Bytes(), crypto.Keccak256Hash(nil).Bytes())

	// Replay invalidation and successful publication use distinct durable source
	// identities. Exercise the hostile interleaving where Proxy runs after the
	// old Trace proof is withdrawn but before the replacement Trace completes.
	validTraceReplay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 3,
		Replay: enrich.ReplaySource{Kind: "fixture", Key: "clone-trace-success-interleaving"},
	})
	if err != nil || !validTraceReplay.Replayed {
		t.Fatalf("replay immutable Clone Trace for interleaving: result=%+v err=%v", validTraceReplay, err)
	}
	third, found, err := queue.Claim(
		ctx, "proxy-before-trace-generation-two", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || third.Job.Generation != 3 {
		t.Fatalf("claim Proxy before replacement Trace: lease=%+v found=%t err=%v", third, found, err)
	}
	result, err = processor.ProcessLease(ctx, third, queue)
	if err != nil || result.State != enrich.ResultComplete ||
		result.Details["rejected_candidates"] != "1" || result.Details["proxies"] != "0" {
		t.Fatalf("publish Proxy without replacement Trace proof: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 3)

	validTracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "immutable-clone-trace-generation-two",
		Client:   newIntegrationRPCClient(t, "debug", &immutableCloneTraceService{db: db, raw: creationTrace}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	validTraceProcessor, err := enrich.NewTraceRPCProcessor(db, validTracePool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	validTraceLease, found, err := queue.Claim(
		ctx, "immutable-clone-trace-generation-two", []enrich.StageID{enrich.TraceStage}, time.Minute,
	)
	if err != nil || !found || validTraceLease.Job.Generation != 2 {
		t.Fatalf("claim replacement Trace: lease=%+v found=%t err=%v", validTraceLease, found, err)
	}
	validTraceResult, err := validTraceProcessor.ProcessLease(ctx, validTraceLease, queue)
	if err != nil || validTraceResult.State != enrich.ResultComplete ||
		validTraceResult.Details["proxy_requeued"] != "true" {
		t.Fatalf("publish replacement Trace: result=%+v err=%v", validTraceResult, err)
	}
	assertPublishedGeneration(t, ctx, db, traceJob.Job.ID, 2)
	fourth, found, err := queue.Claim(
		ctx, "proxy-after-trace-generation-two", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || fourth.Job.Generation != 4 {
		t.Fatalf("claim Proxy after replacement Trace: lease=%+v found=%t err=%v", fourth, found, err)
	}
	result, err = processor.ProcessLease(ctx, fourth, queue)
	if err != nil || result.State != enrich.ResultComplete ||
		result.Details["rejected_candidates"] != "0" || result.Details["proxies"] != "1" {
		t.Fatalf("restore exact Clone after replacement Trace: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 4)

	// Withdrawing the published Trace generation must immediately withdraw the
	// exact Proxy publication that depended on its CREATE proof. The next Trace
	// attempt intentionally loses the capability; the old exact Clone must not
	// become current again while Proxy waits for a fresh proof.
	replayedTrace, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 3,
		Replay: enrich.ReplaySource{Kind: "fixture", Key: "clone-trace-capability-lost"},
	})
	if err != nil || !replayedTrace.Replayed {
		t.Fatalf("replay immutable Clone Trace: result=%+v err=%v", replayedTrace, err)
	}
	assertReplayGeneration(t, ctx, db, enqueued.Job.ID, replayGenerationState{
		Status: "queued", Requested: 5, Claimed: 4, Completed: 4,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM published_block_stage_results
		WHERE chain_id = 1 AND block_hash = $1
		  AND stage IN ('trace', 'proxy')`, 0, reference.Hash.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM normalized_traces
		WHERE chain_id = 1 AND block_hash = $1`, 1, reference.Hash.Bytes())

	unavailablePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "immutable-clone-trace-unavailable",
		Client:   newIntegrationRPCClient(t, "debug", &immutableCloneTraceService{}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace:  ethrpc.AvailabilityUnavailable,
			ethrpc.CapabilityParityTrace: ethrpc.AvailabilityUnavailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unavailableProcessor, err := enrich.NewTraceRPCProcessor(db, unavailablePool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	unavailableWorker, err := enrich.NewWorker(
		queue, []enrich.Processor{unavailableProcessor}, enrich.WorkerOptions{
			ID: "immutable-clone-trace-unavailable", LeaseDuration: time.Second,
			PollInterval: time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, unavailableWorker)
	assertJobStatus(t, ctx, db, traceJob.Job.ID, "failed")
	assertStageResult(
		t, ctx, db, replayedTrace.Job, enrich.ResultUnavailable,
		"trace RPC capability unavailable: configured endpoint exposes neither debug nor trace module",
		map[string]string{},
	)
	assertReplayGeneration(t, ctx, db, enqueued.Job.ID, replayGenerationState{
		Status: "queued", Requested: 5, Claimed: 4, Completed: 4,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_observations AS observation
		JOIN proxy_observation_generations AS witness
		  ON witness.chain_id = observation.chain_id
		 AND witness.proxy_address = observation.proxy_address
		 AND witness.observation_block_hash = observation.block_hash
		 AND witness.observation_stage_version = observation.stage_version
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		WHERE observation.chain_id = 1 AND observation.block_hash = $1
		  AND observation.proxy_address = $2
		  AND observation.proxy_pattern = 'clone'
		  AND observation.evidence_state = 'exact'`, 0,
		reference.Hash.Bytes(), candidate.Bytes())
}

func TestPublishedCREATEAndCREATE2TracesAuthenticateImmutableCloneArguments(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	factory := testAddress(9_810)
	createClone, create2Clone := testAddress(9_811), testAddress(9_812)
	createImplementation, create2Implementation := testAddress(9_813), testAddress(9_814)
	createArgs, create2Args := []byte("create-immutable"), []byte("create2-immutable")
	createRuntime := append(cloneRuntime(createImplementation), createArgs...)
	create2Runtime := append(cloneRuntime(create2Implementation), create2Args...)
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number: 0, ParentHash: testHash(0), ExtraData: []byte("published-clone-creations"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &factory, Data: []byte{0x12, 0x34},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "published-clone-creations"},
	})
	if err != nil {
		t.Fatalf("build published clone creation bundle: %v", err)
	}
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = clock_timestamp()`)
	reference := mustBlockRef(t, block)
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	_ = publishCloneTrace(t, ctx, db, queue, reference, nestedImmutableCloneCreationTrace(
		t, block.Block.Transactions()[0], []immutableCloneTraceCreation{
			{kind: "CREATE", address: createClone, runtime: createRuntime},
			{kind: "CREATE2", address: create2Clone, runtime: create2Runtime},
		},
	))

	states := map[string]map[string]proxyContractState{
		reference.Hash.String(): {
			factory.String():      {code: []byte{0x60, 0x00}},
			createClone.String():  {code: createRuntime},
			create2Clone.String(): {code: create2Runtime},
		},
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "published-clone-state", states, nil, &sync.Mutex{}, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
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
	lease, found, err := queue.Claim(ctx, "published-clone-proxy", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim published clone Proxy: lease=%+v found=%t err=%v", lease, found, err)
	}
	result, err := processor.ProcessLease(ctx, lease, queue)
	if err != nil || result.State != enrich.ResultComplete ||
		result.Details["proxies"] != "2" || result.Details["rejected_candidates"] != "0" {
		t.Fatalf("publish immutable clones: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM normalized_traces
		WHERE chain_id = 1 AND block_hash = $1 AND canonical
		  AND ((call_type = 'CREATE' AND created_address = $2 AND input = $4 AND output = $6)
		    OR (call_type = 'CREATE2' AND created_address = $3 AND input = $5 AND output = $7))`, 2,
		reference.Hash.Bytes(), createClone.Bytes(), create2Clone.Bytes(),
		openZeppelinImmutableCloneInitcode(createRuntime), openZeppelinImmutableCloneInitcode(create2Runtime),
		createRuntime, create2Runtime)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observations
		WHERE chain_id = 1 AND block_hash = $1 AND stage_version = 2 AND canonical
		  AND proxy_kind = 'eip1167' AND proxy_pattern = 'clone'
		  AND evidence_state = 'exact'
		  AND implementation_code_hash = $2
		  AND details->>'minimal_runtime' = 'openzeppelin_immutable_args'
		  AND details->>'immutable_args_creation_authenticated' = 'true'
		  AND ((proxy_address = $3 AND implementation_address = $5 AND immutable_args = $7)
		    OR (proxy_address = $4 AND implementation_address = $6 AND immutable_args = $8))`, 2,
		reference.Hash.Bytes(), crypto.Keccak256Hash(nil).Bytes(),
		createClone.Bytes(), create2Clone.Bytes(), createImplementation.Bytes(), create2Implementation.Bytes(),
		createArgs, create2Args)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_code_observations
		WHERE chain_id = 1 AND block_hash = $1 AND address IN ($2, $3)
		  AND code_hash = $4 AND octet_length(code) = 0 AND canonical`, 2,
		reference.Hash.Bytes(), createImplementation.Bytes(), create2Implementation.Bytes(),
		crypto.Keccak256Hash(nil).Bytes())
}

type immutableCloneTraceCreation struct {
	kind    string
	address common.Address
	runtime []byte
}

type immutableCloneTraceService struct {
	db  *sql.DB
	raw json.RawMessage
}

func (service *immutableCloneTraceService) TraceBlockByHash(
	ctx context.Context,
	blockHash common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	return marshalDatabaseBlockTraceResults(ctx, service.db, blockHash, func(common.Hash) (json.RawMessage, error) {
		return service.raw, nil
	})
}

func publishCloneTrace(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	queue *enrich.PostgresJobQueue,
	reference store.BlockRef,
	raw json.RawMessage,
) enrich.EnqueueResult {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "immutable-clone-trace",
		Client:   newIntegrationRPCClient(t, "debug", &immutableCloneTraceService{db: db, raw: raw}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewTraceRPCProcessor(db, pool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word,
		BlockNumber: reference.Number, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "immutable-clone-trace", []enrich.StageID{enrich.TraceStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim immutable clone Trace: lease=%+v found=%t err=%v", lease, found, err)
	}
	result, err := processor.ProcessLease(ctx, lease, queue)
	if err != nil || result.State != enrich.ResultComplete {
		t.Fatalf("publish immutable clone Trace: result=%+v err=%v", result, err)
	}
	assertPublishedGeneration(t, ctx, db, enqueued.Job.ID, 1)
	return enqueued
}

func rootImmutableCloneCreationTrace(
	t *testing.T,
	transaction *types.Transaction,
	created common.Address,
	runtime []byte,
) json.RawMessage {
	t.Helper()
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		t.Fatalf("recover immutable clone creator: %v", err)
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CREATE", "from": from.String(), "to": created.String(),
		"value": hexutil.EncodeBig(transaction.Value()), "gas": "0x100000", "gasUsed": "0x1000",
		"input": hexutil.Encode(transaction.Data()), "output": hexutil.Encode(runtime),
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func nestedImmutableCloneCreationTrace(
	t *testing.T,
	transaction *types.Transaction,
	creations []immutableCloneTraceCreation,
) json.RawMessage {
	t.Helper()
	if transaction.To() == nil {
		t.Fatal("nested immutable clone fixture requires a call transaction")
	}
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		t.Fatalf("recover immutable clone factory caller: %v", err)
	}
	calls := make([]any, 0, len(creations))
	for _, creation := range creations {
		calls = append(calls, map[string]any{
			"type": creation.kind, "from": transaction.To().String(), "to": creation.address.String(),
			"value": "0x0", "gas": "0x10000", "gasUsed": "0x1000",
			"input":  hexutil.Encode(openZeppelinImmutableCloneInitcode(creation.runtime)),
			"output": hexutil.Encode(creation.runtime),
		})
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from.String(), "to": transaction.To().String(),
		"value": hexutil.EncodeBig(transaction.Value()), "gas": "0x100000", "gasUsed": "0x3000",
		"input": hexutil.Encode(transaction.Data()), "output": "0x", "calls": calls,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func openZeppelinImmutableCloneInitcode(runtime []byte) []byte {
	size := len(runtime)
	return append([]byte{
		0x61, byte(size >> 8), byte(size), 0x3d, 0x81, 0x60, 0x0a, 0x3d, 0x39, 0xf3,
	}, runtime...)
}

func assertProxyDetectionGenerationRejected(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	jobID string,
	generation int64,
	reference store.BlockRef,
	address common.Address,
) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO proxy_detection_evidence (
			chain_id, address, block_number, block_hash, stage_version,
			code_hash, candidate_kind, detection_state, reason, canonical,
			durable_job_id, job_generation
		) VALUES (
			1, $1, $2::numeric, $3, 2,
			$4, 'proxy', 'not_detected', 'not_proxy', TRUE,
			$5::bigint, $6::bigint
		)`, address.Bytes(), reference.Number, reference.Hash.Bytes(),
		common.HexToHash("0x9801").Bytes(), jobID, generation)
	if err == nil || !strings.Contains(err.Error(), "active proxy@2 lease") {
		t.Fatalf("wrong-generation proxy evidence error=%v", err)
	}
}

func assertCurrentProxyDetectionGeneration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	reference store.BlockRef,
	address common.Address,
	jobID string,
	want int64,
) {
	t.Helper()
	var count int64
	var generation sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT count(*), max(evidence.job_generation)
		FROM proxy_detection_evidence AS evidence
		JOIN published_block_stage_results AS published
		  ON published.chain_id = evidence.chain_id
		 AND published.block_hash = evidence.block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = evidence.stage_version
		 AND published.state = 'complete'
		 AND published.durable_job_id = evidence.durable_job_id
		 AND published.job_generation = evidence.job_generation
		WHERE evidence.chain_id = 1
		  AND evidence.block_hash = $1
		  AND evidence.address = $2
		  AND evidence.durable_job_id = $3
		  AND evidence.canonical`, reference.Hash.Bytes(), address.Bytes(), jobID).Scan(
		&count, &generation,
	)
	if err != nil {
		t.Fatalf("query current proxy detection generation: %v", err)
	}
	if want == 0 {
		if count != 0 || generation.Valid {
			t.Fatalf("current proxy detection count=%d generation=%+v want none", count, generation)
		}
		return
	}
	if count != 1 || !generation.Valid || generation.Int64 != want {
		t.Fatalf("current proxy detection count=%d generation=%+v want=%d", count, generation, want)
	}
}
