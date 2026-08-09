//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type proxyStateDiffService struct {
	raw   json.RawMessage
	calls int
}

func (service *proxyStateDiffService) TraceTransaction(
	_ context.Context,
	_ common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	service.calls++
	return append(json.RawMessage(nil), service.raw...), nil
}

func TestStateDiffRequeuesProxyOnlyForCodeAndExactERC1967Slots(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block := testBundle(0, testHash(96_000), testHash(0), testHash(96_100), "proxy-state-diff-replay")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = clock_timestamp()`)
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryAddress := testAddress(960)
	implementationAddress := testAddress(961)
	beaconAddress := testAddress(962)
	adminAddress := testAddress(963)
	codeAddress := testAddress(964)
	silentImplementation := testAddress(965)
	states := map[string]map[string]proxyContractState{
		reference.Hash.String(): {
			implementationAddress.String(): {
				code: []byte{0x60, 0x96}, implementation: &silentImplementation,
			},
			silentImplementation.String(): {code: []byte{0x60, 0x97}},
			codeAddress.String():          {code: []byte{0x60, 0x98}},
		},
	}
	proxyPool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(
			t, "state-diff-proxy-state", states, nil,
			&sync.Mutex{}, make(map[string][]string),
		),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	proxyProcessor, err := enrich.NewPostgresProxyProcessor(db, proxyPool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	proxyJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	traceJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyLease, found, err := queue.Claim(ctx, "state-diff-proxy-one", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim initial Proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish initial Proxy: %v", err)
	}
	assertPublishedGeneration(t, ctx, db, proxyJob.Job.ID, 1)

	service := &proxyStateDiffService{raw: proxyStateDiffResponse(t, proxyStateDiffFixture{
		ordinaryAddress: ordinaryAddress,
	})}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "proxy-state-diff", Client: newIntegrationRPCClient(t, "debug", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stateProcessor, err := enrich.NewStateDiffRPCProcessor(db, pool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	stateJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	stateLease, found, err := queue.Claim(ctx, "ordinary-state-diff", []enrich.StageID{enrich.StateDiffStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim ordinary StateDiff: lease=%+v found=%t err=%v", stateLease, found, err)
	}
	ordinary, err := stateProcessor.ProcessLease(ctx, stateLease, queue)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Details["proxy_relevant_changes"] != "0" || ordinary.Details["trace_requeued"] != "true" {
		t.Fatalf("ordinary StateDiff result=%+v", ordinary)
	}
	assertPublishedGeneration(t, ctx, db, stateJob.Job.ID, 1)
	assertReplayGeneration(t, ctx, db, proxyJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 2, Claimed: 1, Completed: 1,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM transaction_state_changes
		WHERE chain_id = 1 AND block_hash = $1 AND field_kind = 'storage'
		  AND storage_key = $2 AND canonical`, 1,
		mustBytes(t, reference.Hash), common.HexToHash("0x01").Bytes())

	service.raw = proxyStateDiffResponse(t, proxyStateDiffFixture{
		implementationAddress: implementationAddress,
		beaconAddress:         beaconAddress,
		adminAddress:          adminAddress,
		codeAddress:           codeAddress,
	})
	replayed, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "fixture", Key: "exact-proxy-state"},
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("request exact StateDiff replay: result=%+v err=%v", replayed, err)
	}
	stateLease, found, err = queue.Claim(ctx, "exact-state-diff", []enrich.StageID{enrich.StateDiffStage}, time.Minute)
	if err != nil || !found || stateLease.Job.Generation != 2 {
		t.Fatalf("claim exact StateDiff: lease=%+v found=%t err=%v", stateLease, found, err)
	}
	relevant, err := stateProcessor.ProcessLease(ctx, stateLease, queue)
	if err != nil {
		t.Fatal(err)
	}
	if relevant.Details["proxy_relevant_changes"] != "4" || relevant.Details["trace_requeued"] != "true" {
		t.Fatalf("proxy-relevant StateDiff result=%+v", relevant)
	}
	assertPublishedGeneration(t, ctx, db, stateJob.Job.ID, 2)
	assertReplayGeneration(t, ctx, db, traceJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 4, Claimed: 0, Completed: 0,
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM transaction_state_changes
		WHERE chain_id = 1 AND block_hash = $1 AND canonical
		  AND (field_kind = 'code' OR
		       (field_kind = 'storage' AND storage_key IN ($2, $3, $4)))`, 4,
		mustBytes(t, reference.Hash), enrich.EIP1967ImplementationSlot.Bytes(),
		enrich.EIP1967BeaconSlot.Bytes(), enrich.EIP1967AdminSlot.Bytes())

	traceRaw := traceStageCallTracerResponse(t, block.Block.Transactions()[0])
	tracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state-diff-trace", Client: newIntegrationRPCClient(t, "debug", &traceStageService{raw: traceRaw}),
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
	traceLease, found, err := queue.Claim(ctx, "state-diff-trace-two", []enrich.StageID{enrich.TraceStage}, time.Minute)
	if err != nil || !found || traceLease.Job.Generation != 4 {
		t.Fatalf("claim state-dependent Trace: lease=%+v found=%t err=%v", traceLease, found, err)
	}
	if _, err := traceProcessor.ProcessLease(ctx, traceLease, queue); err != nil {
		t.Fatalf("publish state-dependent Trace: %v", err)
	}

	proxyLease, found, err = queue.Claim(
		ctx, "state-diff-proxy-two", []enrich.StageID{enrich.ProxyStage}, time.Minute,
	)
	if err != nil || !found || proxyLease.Job.Generation != 5 {
		t.Fatalf("claim silent-upgrade Proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish silent-upgrade Proxy: %v", err)
	}
	assertPublishedGeneration(t, ctx, db, proxyJob.Job.ID, 5)
	assertCanonicalProxyImplementation(
		t, ctx, db, block, implementationAddress, silentImplementation, "eip1967", nil,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_upgrade_events
		WHERE chain_id = 1 AND block_hash = $1 AND emitter_address = $2`, 0,
		mustBytes(t, reference.Hash), implementationAddress.Bytes())
	assertStageDetail(t, ctx, db, word, "proxy", "state_diff_coverage", "complete")
	assertStageDetail(t, ctx, db, word, "proxy", "trace_coverage", "complete")
	assertStageDetail(t, ctx, db, word, "proxy", "history_coverage", "complete")
	if service.calls != 2 {
		t.Fatalf("StateDiff RPC calls=%d want=2", service.calls)
	}
}

func TestPublishedOrdinaryCallReplaysProxyWithoutInventingProxy(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	block := testBundle(0, testHash(97_000), testHash(0), testHash(97_100), "proxy-ordinary-call-replay")
	commitCanonical(t, ctx, repository, block)
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = clock_timestamp()`)
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]map[string]proxyContractState{reference.Hash.String(): {}}
	var callMu sync.Mutex
	calls := make(map[string][]string)
	statePool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "ordinary-call-state", states, nil, &callMu, calls),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	proxyProcessor, err := enrich.NewPostgresProxyProcessor(db, statePool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	proxyJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyLease, found, err := queue.Claim(ctx, "ordinary-call-proxy-one", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim initial Proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish initial Proxy: %v", err)
	}
	assertPublishedGeneration(t, ctx, db, proxyJob.Job.ID, 1)

	internalTarget := testAddress(970)
	transaction := block.Block.Transactions()[0]
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			trace_path, parent_path, depth, call_type, from_address, to_address,
			value, gas, gas_used, input, output, reverted, canonical
		) VALUES (1, 0, $1, $2, 0, '0', '', 1, 'CALL', $3, $4,
			0, 100, 50, '', '', FALSE, TRUE)`,
		mustBytes(t, reference.Hash), transaction.Hash().Bytes(), transaction.To().Bytes(), internalTarget.Bytes())
	manualReplay, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
		Replay: enrich.ReplaySource{Kind: "fixture", Key: "unpublished-ordinary-call"},
	})
	if err != nil || !manualReplay.Replayed {
		t.Fatalf("request unpublished trace probe: result=%+v err=%v", manualReplay, err)
	}
	proxyLease, found, err = queue.Claim(ctx, "ordinary-call-proxy-two", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found || proxyLease.Job.Generation != 2 {
		t.Fatalf("claim unpublished-trace Proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish Proxy without a Trace generation: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_code_observations
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2`, 0,
		mustBytes(t, reference.Hash), internalTarget.Bytes())

	traceService := &traceStageService{raw: ordinaryNestedCallTrace(t, block, internalTarget)}
	tracePool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "ordinary-call-trace", Client: newIntegrationRPCClient(t, "debug", traceService),
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
	traceJob, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	traceLease, found, err := queue.Claim(ctx, "ordinary-call-trace", []enrich.StageID{enrich.TraceStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim Trace: lease=%+v found=%t err=%v", traceLease, found, err)
	}
	traceResult, err := traceProcessor.ProcessLease(ctx, traceLease, queue)
	if err != nil {
		t.Fatal(err)
	}
	if traceResult.Details["creation_targets"] != "0" || traceResult.Details["proxy_requeued"] != "true" {
		t.Fatalf("ordinary CALL Trace result=%+v", traceResult)
	}
	assertPublishedGeneration(t, ctx, db, traceJob.Job.ID, 1)
	assertReplayGeneration(t, ctx, db, proxyJob.Job.ID, replayGenerationState{
		Status: "queued", Requested: 3, Claimed: 2, Completed: 2,
	})

	proxyLease, found, err = queue.Claim(ctx, "ordinary-call-proxy-three", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found || proxyLease.Job.Generation != 3 {
		t.Fatalf("claim published-trace Proxy: lease=%+v found=%t err=%v", proxyLease, found, err)
	}
	if _, err := proxyProcessor.ProcessLease(ctx, proxyLease, queue); err != nil {
		t.Fatalf("publish Proxy from ordinary CALL candidate: %v", err)
	}
	assertPublishedGeneration(t, ctx, db, proxyJob.Job.ID, 3)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND durable_job_id = $3 AND job_generation = 3
		  AND detection_state = 'not_detected' AND reason = 'empty_code'
		  AND details @> '{"discovery_sources":["trace_target"]}'::jsonb`, 1,
		mustBytes(t, reference.Hash), internalTarget.Bytes(), proxyJob.Job.ID)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observations
		WHERE chain_id = 1 AND block_hash = $1 AND proxy_address = $2`, 0,
		mustBytes(t, reference.Hash), internalTarget.Bytes())
}

type proxyStateDiffFixture struct {
	ordinaryAddress       common.Address
	implementationAddress common.Address
	beaconAddress         common.Address
	adminAddress          common.Address
	codeAddress           common.Address
}

func proxyStateDiffResponse(t *testing.T, fixture proxyStateDiffFixture) json.RawMessage {
	t.Helper()
	pre := make(map[string]any)
	post := make(map[string]any)
	before, after := common.HexToHash("0x01").Hex(), common.HexToHash("0x02").Hex()
	addStorage := func(address common.Address, key common.Hash) {
		if address == (common.Address{}) {
			return
		}
		pre[address.String()] = map[string]any{"storage": map[string]string{key.Hex(): before}}
		post[address.String()] = map[string]any{"storage": map[string]string{key.Hex(): after}}
	}
	addStorage(fixture.ordinaryAddress, common.HexToHash("0x01"))
	addStorage(fixture.implementationAddress, enrich.EIP1967ImplementationSlot)
	addStorage(fixture.beaconAddress, enrich.EIP1967BeaconSlot)
	addStorage(fixture.adminAddress, enrich.EIP1967AdminSlot)
	if fixture.codeAddress != (common.Address{}) {
		pre[fixture.codeAddress.String()] = map[string]any{"code": "0x6000"}
		post[fixture.codeAddress.String()] = map[string]any{"code": "0x6001"}
	}
	encoded, err := json.Marshal(map[string]any{"pre": pre, "post": post})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func ordinaryNestedCallTrace(
	t *testing.T,
	block chainbundle.Bundle,
	internalTarget common.Address,
) json.RawMessage {
	t.Helper()
	transaction := block.Block.Transactions()[0]
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		t.Fatalf("recover ordinary CALL sender: %v", err)
	}
	if transaction.To() == nil {
		t.Fatal("ordinary CALL fixture is contract creation")
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from.String(), "to": transaction.To().String(),
		"value": fmt.Sprintf("0x%x", transaction.Value()), "gas": "0x5208", "gasUsed": "0x100",
		"input": fmt.Sprintf("0x%x", transaction.Data()), "output": "0x",
		"calls": []any{map[string]any{
			"type": "CALL", "from": transaction.To().String(), "to": internalTarget.String(),
			"value": "0x0", "gas": "0x1000", "gasUsed": "0x80", "input": "0x1234", "output": "0x",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
