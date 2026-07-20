package maintenance

import (
	"context"
	"errors"
	"fmt"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/store"
)

// BundleSource is the numbered RPC path used by a core repair. Implementations
// must pin every returned bundle to one endpoint; syncer.RPCSource supplies
// that guarantee through ethrpc.Fetcher.
type BundleSource interface {
	BundleByNumber(context.Context, ethrpc.Purpose, uint64) (ethrpc.Bundle, error)
}

// BundleCanonicalizer is deliberately the same apply boundary used by normal
// history synchronization. Maintenance must not invent a second canonicality
// implementation or use the authoritative-head truncation path.
type BundleCanonicalizer interface {
	Refresh(context.Context, ethrpc.Bundle, store.RefreshOptions) (indexer.ApplyResult, error)
}

type CanonicalBlockSource interface {
	CanonicalBlock(context.Context, string, uint64) (store.BlockRef, bool, error)
}

// ReplayQueue exposes only durable scheduling operations needed by reindex.
// Requeue is responsible for preserving an active lease.
type ReplayQueue interface {
	Enqueue(context.Context, enrich.EnqueueRequest) (enrich.EnqueueResult, error)
	Requeue(context.Context, enrich.Job) error
}

// Executor is the production RangeExecutor shared by monolith and split-role
// deployments. A repair re-fetches core bundles; a reindex only schedules
// block-hash-scoped derived work. These operations are intentionally separate.
type Executor struct {
	chainID       string
	source        BundleSource
	canonicalizer BundleCanonicalizer
	canonical     CanonicalBlockSource
	queue         ReplayQueue
}

var _ RangeExecutor = (*Executor)(nil)

func NewExecutor(
	chainID string,
	source BundleSource,
	canonicalizer BundleCanonicalizer,
	canonical CanonicalBlockSource,
	queue ReplayQueue,
) (*Executor, error) {
	if err := validateCanonicalDecimal(chainID, 78); err != nil || chainID == "0" {
		return nil, errors.New("maintenance executor chain ID must be a canonical positive decimal integer")
	}
	if source == nil {
		return nil, errors.New("maintenance executor requires an RPC bundle source")
	}
	if canonicalizer == nil {
		return nil, errors.New("maintenance executor requires a canonicalizer")
	}
	if canonical == nil {
		return nil, errors.New("maintenance executor requires a canonical block source")
	}
	if queue == nil {
		return nil, errors.New("maintenance executor requires an enrichment queue")
	}
	return &Executor{
		chainID: chainID, source: source, canonicalizer: canonicalizer,
		canonical: canonical, queue: queue,
	}, nil
}

func (executor *Executor) Repair(ctx context.Context, request Request) error {
	if err := executor.validate(request, OperationRepair); err != nil {
		return err
	}
	if request.Stage != "core" {
		return fmt.Errorf("%w: repair only supports core, got %q", ErrInvalidRequest, request.Stage)
	}
	return forBlockRange(ctx, request.FromBlock, request.ToBlock, func(number uint64) error {
		bundle, err := executor.source.BundleByNumber(ctx, ethrpc.PurposeHistory, number)
		if err != nil {
			return fmt.Errorf("repair core block %d: fetch history bundle: %w", number, err)
		}
		actual, err := bundle.Number()
		if err != nil {
			return fmt.Errorf("repair core block %d: decode fetched height: %w", number, err)
		}
		if actual != number {
			return fmt.Errorf("repair core block %d: RPC source returned height %d", number, actual)
		}
		if _, err := executor.canonicalizer.Refresh(ctx, bundle, store.RefreshOptions{
			AllowFinalized: request.AllowFinalized,
		}); err != nil {
			return fmt.Errorf("repair core block %d: apply canonical bundle: %w", number, err)
		}
		return nil
	})
}

func (executor *Executor) Reindex(ctx context.Context, request Request) error {
	if err := executor.validate(request, OperationReindex); err != nil {
		return err
	}
	stage, ok := replayStage(request.Stage)
	if !ok {
		return fmt.Errorf(
			"%w: reindex only supports token, stats, or trace v1, got %q",
			ErrInvalidRequest, request.Stage,
		)
	}
	// Success means that every immutable block/stage job was scheduled,
	// explicitly replayed, or was already protected by an active lease. It does
	// not claim that the asynchronous enrichment output is already complete.
	return forBlockRange(ctx, request.FromBlock, request.ToBlock, func(number uint64) error {
		reference, exists, err := executor.canonical.CanonicalBlock(ctx, executor.chainID, number)
		if err != nil {
			return fmt.Errorf("reindex %s block %d: read canonical block: %w", stage, number, err)
		}
		if !exists {
			return fmt.Errorf("reindex %s block %d: canonical block is unavailable", stage, number)
		}
		if reference.Number != number {
			return fmt.Errorf("reindex %s block %d: canonical source returned height %d", stage, number, reference.Number)
		}
		hashBytes, err := reference.Hash.Bytes()
		if err != nil {
			return fmt.Errorf("reindex %s block %d: decode canonical hash: %w", stage, number, err)
		}
		blockHash, err := enrich.WordFromBytes(hashBytes)
		if err != nil {
			return fmt.Errorf("reindex %s block %d: convert canonical hash: %w", stage, number, err)
		}
		result, err := executor.queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: executor.chainID,
			BlockHash: blockHash, BlockNumber: number,
		})
		if err != nil {
			return fmt.Errorf("reindex %s block %d: enqueue durable job: %w", stage, number, err)
		}
		if result.Created {
			return nil
		}
		if err := executor.queue.Requeue(ctx, result.Job); err != nil && !errors.Is(err, enrich.ErrJobBusy) {
			return fmt.Errorf("reindex %s block %d: replay existing job: %w", stage, number, err)
		}
		return nil
	})
}

func (executor *Executor) validate(request Request, operation Operation) error {
	if executor == nil || executor.source == nil || executor.canonicalizer == nil || executor.canonical == nil || executor.queue == nil {
		return errors.New("use nil or incomplete maintenance executor")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if request.ChainID != executor.chainID {
		return fmt.Errorf(
			"%w: request chain %s does not match configured chain %s",
			ErrInvalidRequest, request.ChainID, executor.chainID,
		)
	}
	if request.Operation != operation {
		return fmt.Errorf(
			"%w: %s executor cannot run %s request",
			ErrInvalidRequest, operation, request.Operation,
		)
	}
	return nil
}

func replayStage(name string) (enrich.StageID, bool) {
	switch name {
	case "token":
		return enrich.StageID{Name: "token", Version: 1}, true
	case "stats":
		return enrich.StatsStage, true
	case "trace":
		return enrich.StageID{Name: "trace", Version: 1}, true
	default:
		return enrich.StageID{}, false
	}
}

func forBlockRange(ctx context.Context, from, to uint64, visit func(uint64) error) error {
	for number := from; ; number++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(number); err != nil {
			return err
		}
		if number == to {
			return nil
		}
	}
}
