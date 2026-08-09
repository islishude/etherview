//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type traceStageService struct {
	calls int
	raw   json.RawMessage
	err   error
}

func (service *traceStageService) TraceTransaction(
	_ context.Context,
	_ common.Hash,
	_ map[string]any,
) (json.RawMessage, error) {
	service.calls++
	if service.err != nil {
		return nil, service.err
	}
	return append(json.RawMessage(nil), service.raw...), nil
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
			wantRPCCall: 2,
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
			service := &traceStageService{raw: raw, err: test.rpcError}
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
