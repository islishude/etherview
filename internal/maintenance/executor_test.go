package maintenance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/store"
)

type executorBundleSource struct {
	bundles  map[uint64]ethrpc.Bundle
	purposes []ethrpc.Purpose
	numbers  []uint64
	err      error
}

func (source *executorBundleSource) BundleByNumber(_ context.Context, purpose ethrpc.Purpose, number uint64) (ethrpc.Bundle, error) {
	source.purposes = append(source.purposes, purpose)
	source.numbers = append(source.numbers, number)
	if source.err != nil {
		return ethrpc.Bundle{}, source.err
	}
	return source.bundles[number], nil
}

type executorCanonicalizer struct {
	bundles []ethrpc.Bundle
	options []store.RefreshOptions
	err     error
}

func (canonicalizer *executorCanonicalizer) Refresh(
	_ context.Context,
	bundle ethrpc.Bundle,
	options store.RefreshOptions,
) (indexer.ApplyResult, error) {
	canonicalizer.bundles = append(canonicalizer.bundles, bundle)
	canonicalizer.options = append(canonicalizer.options, options)
	return indexer.ApplyResult{}, canonicalizer.err
}

type executorCanonicalSource struct {
	blocks  map[uint64]store.BlockRef
	numbers []uint64
	chains  []string
	err     error
}

func (source *executorCanonicalSource) CanonicalBlock(_ context.Context, chainID string, number uint64) (store.BlockRef, bool, error) {
	source.chains = append(source.chains, chainID)
	source.numbers = append(source.numbers, number)
	if source.err != nil {
		return store.BlockRef{}, false, source.err
	}
	reference, exists := source.blocks[number]
	return reference, exists, nil
}

type executorReplayQueue struct {
	enqueued       []enrich.EnqueueRequest
	results        []enrich.EnqueueResult
	enqueueErr     error
	requeued       []enrich.Job
	requeueErrByID map[string]error
}

func (queue *executorReplayQueue) Enqueue(_ context.Context, request enrich.EnqueueRequest) (enrich.EnqueueResult, error) {
	queue.enqueued = append(queue.enqueued, request)
	if queue.enqueueErr != nil {
		return enrich.EnqueueResult{}, queue.enqueueErr
	}
	if len(queue.results) == 0 {
		return enrich.EnqueueResult{Created: true}, nil
	}
	result := queue.results[0]
	queue.results = queue.results[1:]
	return result, nil
}

func (queue *executorReplayQueue) Requeue(_ context.Context, job enrich.Job) error {
	queue.requeued = append(queue.requeued, job)
	return queue.requeueErrByID[job.ID]
}

func TestExecutorRepairUsesHistorySourceAndCanonicalizerForEveryBlock(t *testing.T) {
	t.Parallel()
	source := &executorBundleSource{bundles: map[uint64]ethrpc.Bundle{}}
	for number := uint64(7); number <= 9; number++ {
		source.bundles[number] = executorBundle(number)
	}
	canonicalizer := &executorCanonicalizer{}
	executor := mustExecutor(t, source, canonicalizer, &executorCanonicalSource{}, &executorReplayQueue{})
	request := validRequest()
	request.FromBlock, request.ToBlock = 7, 9
	request.AllowFinalized = true
	if err := executor.Repair(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(source.numbers) != "[7 8 9]" || len(canonicalizer.bundles) != 3 {
		t.Fatalf("fetched=%v applied=%d", source.numbers, len(canonicalizer.bundles))
	}
	for _, purpose := range source.purposes {
		if purpose != ethrpc.PurposeHistory {
			t.Fatalf("repair used RPC purpose %q", purpose)
		}
	}
	for _, options := range canonicalizer.options {
		if !options.AllowFinalized {
			t.Fatal("repair did not forward the explicit finalized override")
		}
	}
}

func TestExecutorRepairRejectsWrongReturnedHeightAndDoesNotAdvance(t *testing.T) {
	t.Parallel()
	source := &executorBundleSource{bundles: map[uint64]ethrpc.Bundle{7: executorBundle(8)}}
	canonicalizer := &executorCanonicalizer{}
	executor := mustExecutor(t, source, canonicalizer, &executorCanonicalSource{}, &executorReplayQueue{})
	request := validRequest()
	request.FromBlock, request.ToBlock = 7, 8
	err := executor.Repair(context.Background(), request)
	if err == nil || len(canonicalizer.bundles) != 0 || fmt.Sprint(source.numbers) != "[7]" {
		t.Fatalf("error=%v fetched=%v applied=%d", err, source.numbers, len(canonicalizer.bundles))
	}
}

func TestExecutorReindexMapsV1StagesToCanonicalBlockJobs(t *testing.T) {
	t.Parallel()
	for _, stage := range []enrich.StageID{enrich.TokenStage, enrich.StatsStage, enrich.TraceStage} {
		stage := stage
		t.Run(stage.Name, func(t *testing.T) {
			t.Parallel()
			reference := executorBlockRef(11)
			canonical := &executorCanonicalSource{blocks: map[uint64]store.BlockRef{11: reference}}
			queue := &executorReplayQueue{}
			executor := mustExecutor(t, &executorBundleSource{}, &executorCanonicalizer{}, canonical, queue)
			request := validRequest()
			request.Operation, request.Stage = OperationReindex, stage.Name
			request.FromBlock, request.ToBlock = 11, 11
			if err := executor.Reindex(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if len(queue.enqueued) != 1 {
				t.Fatalf("enqueued=%d", len(queue.enqueued))
			}
			got := queue.enqueued[0]
			if got.Stage != stage || got.Stage.Version != stage.Version || got.ChainID != "1" || got.BlockNumber != 11 || got.BlockHash.String() != reference.Hash.String() {
				t.Fatalf("enqueue=%+v", got)
			}
			if len(canonical.chains) != 1 || canonical.chains[0] != "1" {
				t.Fatalf("canonical chains=%v", canonical.chains)
			}
		})
	}
}

func TestExecutorReindexReplaysExistingJobButPreservesActiveLease(t *testing.T) {
	t.Parallel()
	canonical := &executorCanonicalSource{blocks: map[uint64]store.BlockRef{
		20: executorBlockRef(20), 21: executorBlockRef(21),
	}}
	busyJob := executorJob("70", enrich.TokenStage, canonical.blocks[20])
	terminalJob := executorJob("71", enrich.TokenStage, canonical.blocks[21])
	queue := &executorReplayQueue{
		results: []enrich.EnqueueResult{
			{Job: busyJob, Created: false},
			{Job: terminalJob, Created: false},
		},
		requeueErrByID: map[string]error{"70": enrich.ErrJobBusy},
	}
	executor := mustExecutor(t, &executorBundleSource{}, &executorCanonicalizer{}, canonical, queue)
	request := validRequest()
	request.Operation, request.Stage = OperationReindex, enrich.TokenStage.Name
	request.FromBlock, request.ToBlock = 20, 21
	if err := executor.Reindex(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(queue.enqueued) != 2 || len(queue.requeued) != 2 {
		t.Fatalf("enqueued=%d requeued=%v", len(queue.enqueued), queue.requeued)
	}
	if queue.requeued[0].ID != "70" || queue.requeued[1].ID != "71" {
		t.Fatalf("requeued=%v", queue.requeued)
	}
}

func TestExecutorReindexPropagatesIdentityMismatchInsteadOfHidingIt(t *testing.T) {
	t.Parallel()
	reference := executorBlockRef(30)
	job := executorJob("90", enrich.StatsStage, reference)
	identityErr := errors.New("enrichment job identity changed before replay")
	queue := &executorReplayQueue{
		results:        []enrich.EnqueueResult{{Job: job}},
		requeueErrByID: map[string]error{"90": identityErr},
	}
	executor := mustExecutor(t, &executorBundleSource{}, &executorCanonicalizer{}, &executorCanonicalSource{
		blocks: map[uint64]store.BlockRef{30: reference},
	}, queue)
	request := validRequest()
	request.Operation, request.Stage = OperationReindex, enrich.StatsStage.Name
	request.FromBlock, request.ToBlock = 30, 30
	err := executor.Reindex(context.Background(), request)
	if !errors.Is(err, identityErr) || len(queue.requeued) != 1 {
		t.Fatalf("error=%v requeued=%v", err, queue.requeued)
	}
}

func TestExecutorNeverAliasesRepairAndReindex(t *testing.T) {
	t.Parallel()
	source := &executorBundleSource{bundles: map[uint64]ethrpc.Bundle{100: executorBundle(100)}}
	canonicalizer := &executorCanonicalizer{}
	canonical := &executorCanonicalSource{blocks: map[uint64]store.BlockRef{100: executorBlockRef(100)}}
	queue := &executorReplayQueue{}
	executor := mustExecutor(t, source, canonicalizer, canonical, queue)

	tests := []struct {
		name    string
		request Request
		run     func(context.Context, Request) error
	}{
		{"repair-operation", reindexRequest("token"), executor.Repair},
		{"reindex-operation", validRequest(), executor.Reindex},
		{"repair-stage", repairRequest("stats"), executor.Repair},
		{"reindex-stage", reindexRequest("core"), executor.Reindex},
	}
	for _, test := range tests {
		if err := test.run(context.Background(), test.request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s error=%v", test.name, err)
		}
	}
	if len(source.numbers) != 0 || len(canonicalizer.bundles) != 0 || len(canonical.numbers) != 0 || len(queue.enqueued) != 0 {
		t.Fatalf("repair/reindex dependency was called: source=%v apply=%d canonical=%v enqueue=%d", source.numbers, len(canonicalizer.bundles), canonical.numbers, len(queue.enqueued))
	}
}

func TestExecutorRangeIncludesMaximumUint64WithoutOverflow(t *testing.T) {
	t.Parallel()
	source := &executorBundleSource{bundles: map[uint64]ethrpc.Bundle{math.MaxUint64: executorBundle(math.MaxUint64)}}
	executor := mustExecutor(t, source, &executorCanonicalizer{}, &executorCanonicalSource{}, &executorReplayQueue{})
	request := validRequest()
	request.FromBlock, request.ToBlock = math.MaxUint64, math.MaxUint64
	if err := executor.Repair(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(source.numbers) != 1 || source.numbers[0] != math.MaxUint64 {
		t.Fatalf("numbers=%v", source.numbers)
	}
}

func TestNewExecutorAndRequestChainValidation(t *testing.T) {
	t.Parallel()
	source := &executorBundleSource{}
	canonicalizer := &executorCanonicalizer{}
	canonical := &executorCanonicalSource{}
	queue := &executorReplayQueue{}
	for _, test := range []struct {
		chainID       string
		source        BundleSource
		canonicalizer BundleCanonicalizer
		canonical     CanonicalBlockSource
		queue         ReplayQueue
	}{
		{"", source, canonicalizer, canonical, queue},
		{"0", source, canonicalizer, canonical, queue},
		{"01", source, canonicalizer, canonical, queue},
		{"1", nil, canonicalizer, canonical, queue},
		{"1", source, nil, canonical, queue},
		{"1", source, canonicalizer, nil, queue},
		{"1", source, canonicalizer, canonical, nil},
	} {
		if _, err := NewExecutor(test.chainID, test.source, test.canonicalizer, test.canonical, test.queue); err == nil {
			t.Fatalf("configuration %+v was accepted", test)
		}
	}
	executor := mustExecutor(t, source, canonicalizer, canonical, queue)
	request := validRequest()
	request.ChainID = "2"
	if err := executor.Repair(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("chain mismatch error=%v", err)
	}
}

func mustExecutor(
	t *testing.T,
	source BundleSource,
	canonicalizer BundleCanonicalizer,
	canonical CanonicalBlockSource,
	queue ReplayQueue,
) *Executor {
	t.Helper()
	executor, err := NewExecutor("1", source, canonicalizer, canonical, queue)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func executorBundle(number uint64) ethrpc.Bundle {
	quantity := ethrpc.QuantityFromUint64(number)
	return ethrpc.Bundle{Block: ethrpc.Block{Number: &quantity}}
}

func executorBlockRef(number uint64) store.BlockRef {
	hash, err := ethrpc.ParseHash(fmt.Sprintf("0x%064x", number+1))
	if err != nil {
		panic(err)
	}
	return store.BlockRef{Number: number, Hash: hash}
}

func executorJob(id string, stage enrich.StageID, reference store.BlockRef) enrich.Job {
	hash, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		panic(err)
	}
	return enrich.Job{
		ID: id, Stage: stage, ChainID: "1",
		BlockHash: hash, BlockNumber: reference.Number,
	}
}

func repairRequest(stage string) Request {
	request := validRequest()
	request.Stage = stage
	return request
}

func reindexRequest(stage string) Request {
	request := validRequest()
	request.Operation = OperationReindex
	request.Stage = stage
	return request
}
