package app

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5/pgtype"
)

func configuredCompleteness(cfg config.Config, rpcBuild *RPCBuild) gen.Completeness {
	trace := gen.StageStateUnavailable
	if cfg.Features.Trace && (rpcBuild == nil || traceRPCAvailable(rpcBuild.Pool)) {
		trace = gen.StageStatePending
	}
	return gen.Completeness{
		Core: gen.StageStateComplete, Trace: trace,
		Metadata:       configuredStageState(cfg.Features.NFTMetadata),
		State:          historicalStateCompleteness(cfg, rpcBuild),
		UserOperations: configuredStageState(cfg.Features.UserOperations),
	}
}

func configuredStageState(enabled bool) gen.StageState {
	if enabled {
		return gen.StageStatePending
	}
	return gen.StageStateUnavailable
}

func historicalStateCompleteness(cfg config.Config, rpcBuild *RPCBuild) gen.StageState {
	if !cfg.Features.HistoricalState {
		return gen.StageStateUnavailable
	}
	if rpcBuild == nil {
		return gen.StageStatePending
	}
	stateEndpoint := false
	unknown := false
	for _, endpoint := range cfg.RPC.Endpoints {
		if !rpcPurposes(endpoint.Purposes)[ethrpc.PurposeState] {
			continue
		}
		stateEndpoint = true
		report, exists := rpcBuild.Reports[endpoint.Name]
		if !exists {
			unknown = true
			continue
		}
		switch report.Status(ethrpc.CapabilityHistoricalState) {
		case ethrpc.AvailabilityAvailable:
			return gen.StageStateComplete
		case ethrpc.AvailabilityUnknown:
			unknown = true
		}
	}
	if stateEndpoint && unknown {
		return gen.StageStatePending
	}
	return gen.StageStateUnavailable
}

func traceRPCAvailable(pool *ethrpc.Pool) bool {
	if pool == nil {
		return false
	}
	for range pool.Names(ethrpc.PurposeTrace) {
		endpoint, err := pool.Acquire(ethrpc.PurposeTrace)
		if err != nil {
			return false
		}
		if endpoint.Capabilities.Status(ethrpc.CapabilityDebugTrace) != ethrpc.AvailabilityUnavailable ||
			endpoint.Capabilities.Status(ethrpc.CapabilityParityTrace) != ethrpc.AvailabilityUnavailable {
			return true
		}
	}
	return false
}

// syncCapabilityReader keeps the API's model defaults in step with the latest
// successful sync probe, including when the API starts before sync.
type syncCapabilityReader struct {
	q        *dbgen.Queries
	chain    pgtype.Numeric
	observed atomic.Pointer[dbgen.ReadSyncCapabilitiesRow]
}

func (*syncCapabilityReader) Name() string { return "sync-capability-reader" }
func (r *syncCapabilityReader) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		value, err := r.q.ReadSyncCapabilities(ctx, r.chain)
		if err == nil {
			r.observed.Store(&value)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (r *syncCapabilityReader) apply(value gen.Completeness) gen.Completeness {
	if observed := r.observed.Load(); observed != nil {
		if value.State == gen.StageStatePending {
			value.State = gen.StageState(observed.HistoricalState)
		}
		if value.Trace == gen.StageStatePending {
			value.Trace = gen.StageState(observed.Trace)
		}
	}
	return value
}
