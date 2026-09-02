package app

import (
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
)

func configuredCompleteness(cfg config.Config, rpcBuild *RPCBuild) gen.Completeness {
	trace := gen.StageStateUnavailable
	if cfg.Features.Trace && rpcBuild != nil && traceRPCAvailable(rpcBuild.Pool) {
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
	if !cfg.Features.HistoricalState || rpcBuild == nil {
		return gen.StageStateUnavailable
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
