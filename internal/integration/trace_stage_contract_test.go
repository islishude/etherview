//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

type traceStageService struct {
	calls     int
	blockHash common.Hash
	hashes    []common.Hash
	raw       json.RawMessage
	err       error
}

type stateDiffBlockFailureService struct {
	blockHash       common.Hash
	transactionHash common.Hash
	calls           int
}

type stateDiffCompletePrestateService struct {
	blockHash       common.Hash
	transactionHash common.Hash
	diffModes       []bool
}

func (service *stateDiffCompletePrestateService) TraceBlockByHash(
	_ context.Context,
	blockHash common.Hash,
	config map[string]any,
) (json.RawMessage, error) {
	if blockHash != service.blockHash {
		return nil, fmt.Errorf("state difference block hash = %s, want %s", blockHash, service.blockHash)
	}
	tracerConfig, _ := config["tracerConfig"].(map[string]any)
	diffMode, ok := tracerConfig["diffMode"].(bool)
	if !ok {
		return nil, fmt.Errorf("state difference trace omitted diffMode")
	}
	service.diffModes = append(service.diffModes, diffMode)
	result := json.RawMessage(`{}`)
	if diffMode {
		result = json.RawMessage(`{"pre":{},"post":{}}`)
	}
	return marshalIntegrationBlockTraceResults(
		[]common.Hash{service.transactionHash},
		func(common.Hash) (json.RawMessage, error) { return result, nil },
	)
}

func (service *stateDiffBlockFailureService) TraceBlockByHash(
	_ context.Context,
	blockHash common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	service.calls++
	if blockHash != service.blockHash {
		return nil, fmt.Errorf("state difference block hash = %s, want %s", blockHash, service.blockHash)
	}
	return json.Marshal([]map[string]any{{
		"txHash": service.transactionHash,
		"error":  "provider-secret historical state pruned",
	}})
}

func (service *traceStageService) TraceBlockByHash(
	_ context.Context,
	blockHash common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	service.calls++
	if service.err != nil {
		return nil, service.err
	}
	if blockHash != service.blockHash {
		return nil, fmt.Errorf("trace block hash = %s, want %s", blockHash, service.blockHash)
	}
	return marshalIntegrationBlockTraceResults(service.hashes, func(common.Hash) (json.RawMessage, error) {
		return service.raw, nil
	})
}

func (service *traceStageService) Transaction(
	_ context.Context,
	_ common.Hash,
) (json.RawMessage, error) {
	service.calls++
	if service.err != nil {
		return nil, service.err
	}
	return append(json.RawMessage(nil), service.raw...), nil
}

func TestTraceStageTerminalOutcomesAreDurable(t *testing.T) {
	for _, test := range []struct {
		name        string
		debug       ethrpc.Availability
		parity      ethrpc.Availability
		raw         json.RawMessage
		rpcError    error
		wantState   enrich.ResultState
		wantError   string
		wantRPCCall int
		configure   func(*enrich.TraceLimits)
		twoTx       bool
		assertEmpty bool
	}{
		{
			name: "missing_capability", debug: ethrpc.AvailabilityUnavailable, parity: ethrpc.AvailabilityUnavailable,
			wantState: enrich.ResultUnavailable,
			wantError: "trace RPC capability unavailable: configured endpoint exposes neither debug nor trace module",
		},
		{
			name: "pruned_history", debug: ethrpc.AvailabilityAvailable, parity: ethrpc.AvailabilityUnavailable,
			rpcError:  &integrationRPCError{code: -32000, message: "historical state pruned"},
			wantState: enrich.ResultUnavailable, wantError: "trace RPC capability unavailable", wantRPCCall: 1,
		},
		{
			name: "timeout", debug: ethrpc.AvailabilityAvailable, parity: ethrpc.AvailabilityUnavailable,
			rpcError:  context.DeadlineExceeded,
			wantState: enrich.ResultFailed, wantError: "JSON-RPC error code -32000", wantRPCCall: 1,
		},
		{
			name: "empty_trace_transaction", debug: ethrpc.AvailabilityUnavailable, parity: ethrpc.AvailabilityAvailable,
			raw:         json.RawMessage(`[]`),
			wantState:   enrich.ResultFailed,
			wantError:   "empty_trace_transaction",
			wantRPCCall: 1,
		},
		{
			name: "whole_block_frame_budget", debug: ethrpc.AvailabilityAvailable, parity: ethrpc.AvailabilityUnavailable,
			wantState:   enrich.ResultFailed,
			wantError:   "whole_block_frame_budget",
			wantRPCCall: 1,
			configure:   func(limits *enrich.TraceLimits) { limits.MaxBlockFrames = 1 },
			twoTx:       true, assertEmpty: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			repository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			bundle := traceStageBundle(t, test.twoTx)
			commitCanonical(t, ctx, repository, bundle)
			reference := mustBlockRef(t, bundle)
			blockHash, err := enrich.ParseWord(reference.Hash.String())
			if err != nil {
				t.Fatal(err)
			}

			raw := test.raw
			if test.twoTx {
				raw = traceStageCallTracerResponse(t, bundle.Block.Transactions()[0])
			}
			hashes := make([]common.Hash, len(bundle.Block.Transactions()))
			for index, transaction := range bundle.Block.Transactions() {
				hashes[index] = transaction.Hash()
			}
			service := &traceStageService{
				blockHash: bundle.Block.Hash(), hashes: hashes, raw: raw, err: test.rpcError,
			}
			client := newIntegrationRPCClientServices(t, map[string]any{
				"debug": service,
				"trace": service,
			})
			pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
				Name: "trace-contract", Client: client,
				Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
				Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
					ethrpc.CapabilityDebugTrace: test.debug, ethrpc.CapabilityParityTrace: test.parity,
				}},
			}}, ethrpc.PoolOptions{})
			if err != nil {
				t.Fatal(err)
			}
			limits := enrich.TraceLimits{}
			if test.configure != nil {
				limits = enrich.DefaultTraceLimits()
				test.configure(&limits)
			}
			processor, err := enrich.NewTraceRPCProcessor(db, pool, limits)
			if err != nil {
				t.Fatal(err)
			}
			queue, err := enrich.NewPostgresJobQueue(db)
			if err != nil {
				t.Fatal(err)
			}
			enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
				Stage: enrich.TraceStage, ChainID: "1", BlockHash: blockHash,
				BlockNumber: reference.Number, MaxAttempts: 1,
			})
			if err != nil || !enqueued.Created {
				t.Fatalf("enqueue trace job = %+v, err=%v", enqueued, err)
			}
			worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
				ID: "trace-terminal-" + test.name, LeaseDuration: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := worker.ProcessOne(ctx)
			if err != nil || !processed {
				t.Fatalf("process trace job=%t err=%v", processed, err)
			}
			assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "failed", 1)
			wantError := test.wantError
			switch wantError {
			case "empty_trace_transaction":
				wantError = fmt.Sprintf(
					"normalize trace_transaction %s: trace_transaction returned no transaction root frame",
					bundle.Block.Transactions()[0].Hash(),
				)
			case "whole_block_frame_budget":
				wantError = fmt.Sprintf(
					"account callTracer transaction %s: trace exceeds configured limit: block frame count",
					bundle.Block.Transactions()[1].Hash(),
				)
			}
			assertStageResult(t, ctx, db, enqueued.Job, test.wantState, wantError, map[string]string{})
			if service.calls != test.wantRPCCall {
				t.Fatalf("RPC calls=%d, want %d", service.calls, test.wantRPCCall)
			}
			if test.assertEmpty {
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM normalized_traces
					WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, reference.Hash))
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM block_journals
					WHERE chain_id = 1 AND block_hash = $1 AND stage = 'trace@3'`, 0, mustBytes(t, reference.Hash))
			}
		})
	}
}

func TestStateDiffStageBlockItemFailurePublishesUnavailableAtomically(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	bundle := traceStageBundle(t, false)
	commitCanonical(t, ctx, repository, bundle)
	reference := mustBlockRef(t, bundle)
	blockHash, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	service := &stateDiffBlockFailureService{
		blockHash: bundle.Block.Hash(), transactionHash: bundle.Block.Transactions()[0].Hash(),
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state-diff-block-failure", Client: newIntegrationRPCClient(t, "debug", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewStateDiffRPCProcessor(db, pool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: blockHash,
		BlockNumber: reference.Number, MaxAttempts: 1,
	})
	if err != nil || !enqueued.Created {
		t.Fatalf("enqueue state difference job = %+v, err=%v", enqueued, err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "state-diff-block-failure", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process state difference job=%t err=%v", processed, err)
	}
	assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "failed", 1)
	assertStageResult(
		t, ctx, db, enqueued.Job, enrich.ResultUnavailable,
		"state difference RPC capability unavailable", map[string]string{},
	)
	if service.calls != 1 {
		t.Fatalf("block trace RPC calls=%d, want 1", service.calls)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM transaction_state_changes
		WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'state_diff@3'`, 0, mustBytes(t, reference.Hash))
}

func TestStateDiffStageCompletePrestatePublishesNativeTransferProjection(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	destination := testAddress(66_000)
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: 0, ParentHash: testHash(0), ExtraData: []byte("complete-prestate-native-transfer"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &destination, Value: big.NewInt(0),
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "complete-prestate-native-transfer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerFixtureIdentities(
		testHash(66_001), bundle.Block.Hash(), testHash(66_002), bundle.Block.Transactions()[0].Hash(),
	)
	commitCanonical(t, ctx, repository, bundle)
	reference := mustBlockRef(t, bundle)
	blockHash, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	transactionHash := bundle.Block.Transactions()[0].Hash()
	service := &stateDiffCompletePrestateService{
		blockHash: bundle.Block.Hash(), transactionHash: transactionHash,
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state-diff-complete-prestate", Client: newIntegrationRPCClient(t, "debug", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewStateDiffRPCProcessor(db, pool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: blockHash,
		BlockNumber: reference.Number, MaxAttempts: 1,
	})
	if err != nil || !enqueued.Created {
		t.Fatalf("enqueue state difference job = %+v, err=%v", enqueued, err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "state-diff-complete-prestate", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process state difference job=%t err=%v", processed, err)
	}
	assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "succeeded", 1)
	if fmt.Sprint(service.diffModes) != "[true false]" {
		t.Fatalf("state difference diff modes = %v, want [true false]", service.diffModes)
	}
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at = clock_timestamp()`)
	transactionReader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	transactions, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 1 || transactions[0].Method == nil || *transactions[0].Method != "Native Transfer" {
		t.Fatalf("transaction projection = %+v", transactions)
	}
	catalogReader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	calldata, err := catalogReader.TransactionCalldata(ctx, "1", transactionHash.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if calldata.Execution.Resolution != "empty" || calldata.Decoding.Status != "not_applicable" || calldata.Input != "0x" {
		t.Fatalf("transaction calldata projection = %+v", calldata)
	}
}

func traceStageBundle(t *testing.T, twoTransactions bool) chainbundle.Bundle {
	t.Helper()
	transactionCount := 1
	if twoTransactions {
		transactionCount = 2
	}
	transactions := make([]integrationTransactionOptions, transactionCount)
	for index := range transactions {
		destination := testAddress(uint64(2 + index))
		transactions[index] = integrationTransactionOptions{
			Type: types.DynamicFeeTxType,
			To:   &destination,
			Data: []byte{byte(index)},
		}
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number:       0,
		ParentHash:   testHash(0),
		ExtraData:    []byte("trace-terminal"),
		Transactions: transactions,
		Withdrawals:  []*types.Withdrawal{},
		RawExtra:     map[string]any{"integrationVariant": "trace-terminal"},
	})
	if err != nil {
		t.Fatalf("build trace stage bundle: %v", err)
	}
	registerFixtureIdentities(testHash(64_000), bundle.Block.Hash(), testHash(65_000), bundle.Block.Transactions()[0].Hash())
	if twoTransactions {
		registerFixtureHash(testHash(65_001), bundle.Block.Transactions()[1].Hash())
	}
	return bundle
}

func traceStageCallTracerResponse(t *testing.T, transaction *types.Transaction) json.RawMessage {
	t.Helper()
	if transaction == nil || transaction.To() == nil {
		t.Fatal("trace block budget fixture requires a call transaction")
	}
	from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		t.Fatalf("recover trace fixture sender: %v", err)
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": from.String(), "to": transaction.To().String(),
		"value": fmt.Sprintf("0x%x", transaction.Value()), "gas": "0x5208", "gasUsed": "0x5208",
		"input": fmt.Sprintf("0x%x", transaction.Data()), "output": "0x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
