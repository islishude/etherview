//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type traceStageCaller struct {
	calls int
	raw   json.RawMessage
	err   error
}

func (caller *traceStageCaller) Call(_ context.Context, _ string, _ []any, result any) error {
	caller.calls++
	if caller.err != nil {
		return caller.err
	}
	destination, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("trace stage fixture received an invalid result destination")
	}
	*destination = append((*destination)[:0], caller.raw...)
	return nil
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
			rpcError:  &ethrpc.RPCError{Code: -32000, Message: "historical state pruned"},
			wantState: enrich.ResultUnavailable, wantError: "JSON-RPC error -32000: historical state pruned", wantRPCCall: 1,
		},
		{
			name: "timeout", debug: ethrpc.AvailabilityAvailable, parity: ethrpc.AvailabilityUnavailable,
			rpcError:  context.DeadlineExceeded,
			wantState: enrich.ResultFailed, wantError: context.DeadlineExceeded.Error(), wantRPCCall: 1,
		},
		{
			name: "empty_trace_transaction", debug: ethrpc.AvailabilityUnavailable, parity: ethrpc.AvailabilityAvailable,
			raw:         json.RawMessage(`[]`),
			wantState:   enrich.ResultFailed,
			wantError:   "normalize trace_transaction 0x000000000000000000000000000000000000000000000000000000000000fde8: trace_transaction returned no transaction root frame",
			wantRPCCall: 1,
		},
		{
			name: "whole_block_frame_budget", debug: ethrpc.AvailabilityAvailable, parity: ethrpc.AvailabilityUnavailable,
			wantState: enrich.ResultFailed,
			wantError: "account callTracer transaction " + testHash(65_001).String() +
				": trace exceeds configured limit: block frame count",
			wantRPCCall: 2,
			configure:   func(limits *enrich.TraceLimits) { limits.MaxBlockFrames = 1 },
			twoTx:       true, assertEmpty: true,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			repository, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			bundle := testBundle(0, testHash(64_000), testHash(0), testHash(65_000), "trace-terminal")
			if test.twoTx {
				appendTraceStageTransaction(&bundle)
			}
			commitCanonical(t, ctx, repository, bundle)
			reference := mustBlockRef(t, bundle)
			blockHash, err := enrich.ParseWord(reference.Hash.String())
			if err != nil {
				t.Fatal(err)
			}

			raw := test.raw
			if test.twoTx {
				raw = traceStageCallTracerResponse(t, bundle.Block.Transactions[0].Transaction)
			}
			caller := &traceStageCaller{raw: raw, err: test.rpcError}
			pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
				Name: "trace-contract", Client: caller,
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
			assertStageResult(t, ctx, db, enqueued.Job, test.wantState, test.wantError, map[string]string{})
			if caller.calls != test.wantRPCCall {
				t.Fatalf("RPC calls=%d, want %d", caller.calls, test.wantRPCCall)
			}
			if test.assertEmpty {
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM normalized_traces
					WHERE chain_id = 1 AND block_hash = $1`, 0, mustBytes(t, reference.Hash))
				assertRowCount(t, ctx, db, `
					SELECT count(*) FROM block_journals
					WHERE chain_id = 1 AND block_hash = $1 AND stage = 'trace@1'`, 0, mustBytes(t, reference.Hash))
			}
		})
	}
}

func appendTraceStageTransaction(bundle *ethrpc.Bundle) {
	first := bundle.Block.Transactions[0].Transaction
	second := *first
	secondHash := testHash(65_001)
	secondIndex := ethrpc.QuantityFromUint64(1)
	second.Hash = secondHash
	second.TransactionIndex = &secondIndex
	second.Nonce = ethrpc.QuantityFromUint64(1)
	bundle.Block.Transactions = append(bundle.Block.Transactions, ethrpc.TransactionRef{Hash: secondHash, Transaction: &second})

	secondReceipt := bundle.Receipts[0]
	secondReceipt.TransactionHash = secondHash
	secondReceipt.TransactionIndex = secondIndex
	secondReceipt.Logs = nil
	cumulativeGas := ethrpc.QuantityFromUint64(42_000)
	secondReceipt.CumulativeGasUsed = cumulativeGas
	bundle.Receipts = append(bundle.Receipts, secondReceipt)
	bundle.Block.GasUsed = cumulativeGas
}

func traceStageCallTracerResponse(t *testing.T, transaction *ethrpc.Transaction) json.RawMessage {
	t.Helper()
	if transaction == nil || transaction.To == nil {
		t.Fatal("trace block budget fixture requires a call transaction")
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "CALL", "from": transaction.From.String(), "to": transaction.To.String(),
		"value": transaction.Value.String(), "gas": "0x5208", "gasUsed": "0x5208",
		"input": transaction.Input.String(), "output": "0x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
