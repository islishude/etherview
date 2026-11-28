package app

import (
	"errors"
	"fmt"
	"math"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/consensus"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	genesisstate "github.com/islishude/etherview/internal/genesis"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/syncer"
)

func (assembly runtimeAssembly) registerSyncComponents(rpcBuild *RPCBuild) error {
	ctx := assembly.ctx
	cfg := assembly.cfg
	db := assembly.db
	logger := assembly.logger
	registry := assembly.registry
	tracker := assembly.tracker
	repository := assembly.repository
	pendingRepository := assembly.pendingRepository
	runtimeEvents := assembly.runtimeEvents
	signalEvents := assembly.signalEvents
	componentRegistry := assembly.componentRegistry
	chainID := assembly.chainID
	if rpcBuild == nil {
		return errors.New("sync role requires an RPC pool")
	}
	if len(rpcBuild.Pool.Names(ethrpc.PurposeHistory)) == 0 {
		return errors.New("sync role requires an HTTP history RPC endpoint")
	}
	fetcher := ethrpc.Fetcher{ReceiptStrategy: ethrpc.ReceiptStrategyAuto, ReceiptBatchSize: cfg.RPC.BatchSize}
	bundleSource := &indexer.PoolBundleSource{Pool: rpcBuild.Pool, Fetcher: fetcher, Purpose: ethrpc.PurposeHistory}
	headBundleSource := &indexer.PoolBundleSource{Pool: rpcBuild.Pool, Fetcher: fetcher, Purpose: ethrpc.PurposeHead}
	coreCanonicalizer := &indexer.Canonicalizer{
		ChainID: chainID, StartBlock: cfg.Chain.StartBlock,
		MaxReorgDepth: cfg.Chain.MaxReorgDepth, Repository: repository,
		Source: bundleSource, HeadSource: headBundleSource,
	}
	coreRPCSource := &syncer.RPCSource{Pool: rpcBuild.Pool, Fetcher: fetcher}
	if err := componentRegistry.Register(components.RoleSync, "06-rpc-recovery", func() (components.Service, error) { return rpcBuild.Recovery, nil }); err != nil {
		return err
	}
	if cfg.Features.Trace {
		if err := componentRegistry.Register(components.RoleSync, "16-trace-inputs", func() (components.Service, error) {
			return &enrich.TraceInputCollector{DB: db, Pool: rpcBuild.Pool, ChainID: assembly.chainID, Timeout: cfg.Runtime.LeaseDuration, PollInterval: cfg.Runtime.PollInterval, Logger: assembly.logger}, nil
		}); err != nil {
			return err
		}
	}
	if len(rpcBuild.Pool.Names(ethrpc.PurposeHead)) == 0 {
		return errors.New("sync role requires HTTP RPC endpoints for both head and history purposes")
	}
	head, err := coreRPCSource.Head(ctx)
	if err != nil {
		return fmt.Errorf("read initial RPC head: %w", err)
	}
	if cfg.Chain.StartBlock <= head {
		if head == math.MaxUint64 {
			return errors.New("RPC head exceeds partition provisioning range")
		}
		if err := repository.EnsureBlockPartitions(ctx, cfg.Chain.StartBlock, head+1); err != nil {
			return fmt.Errorf("provision block partitions through RPC head: %w", err)
		}
	}
	service := &syncer.Service{
		ChainID: chainID, StartBlock: cfg.Chain.StartBlock,
		PollInterval: cfg.Runtime.PollInterval, Workers: cfg.Runtime.BackfillWorkers,
		BackfillBatchBlocks:     uint64(cfg.Runtime.BackfillBatchBlocks),
		BackfillBatchBytes:      uint64(cfg.Runtime.BackfillBatchBytes),
		BackfillBatchRows:       uint64(cfg.Runtime.BackfillBatchRows),
		SyncProgressLogInterval: cfg.Observability.SyncProgressLogInterval,
		WorkerID:                runtimeWorkerID("core-backfill"),
		LeaseDuration:           cfg.Runtime.LeaseDuration,
		Source:                  coreRPCSource, Repository: repository, Canonicalizer: coreCanonicalizer,
		Status: runtimeEvents, EventWake: signalEvents,
		Tracker: tracker, Observer: registry, Logger: logger,
	}
	if len(rpcBuild.WakeURLs) > 0 {
		headWake, err := syncer.NewHeadWake(rpcBuild.WakeURLs, syncer.HeadWakeOptions{Logger: logger})
		if err != nil {
			return err
		}
		service.Wake = headWake.Signal()
		if err := componentRegistry.Register(components.RoleSync, "05-new-head-wake", func() (components.Service, error) {
			return headWake, nil
		}); err != nil {
			return err
		}
	}
	if err := componentRegistry.Register(components.RoleSync, "10-core-sync", func() (components.Service, error) {
		return service, nil
	}); err != nil {
		return err
	}
	if err := componentRegistry.Register(components.RoleSync, "11-stage-state-inputs", func() (components.Service, error) {
		return &syncer.StateInputCollector{DB: db, ChainID: chainID, Pool: rpcBuild.Pool, Timeout: cfg.RPC.RequestTimeout, PollInterval: cfg.Runtime.PollInterval, Logger: logger}, nil
	}); err != nil {
		return err
	}
	if err := componentRegistry.Register(components.RoleSync, "11-balance-acquisition", func() (components.Service, error) {
		return &state.BalanceCollector{Store: state.PostgresNativeBalances{DB: db, ChainID: chainID}, Pool: rpcBuild.Pool, Timeout: cfg.RPC.RequestTimeout, Logger: logger}, nil
	}); err != nil {
		return err
	}
	if err := componentRegistry.Register(components.RoleSync, "11-bal-acquisition", func() (components.Service, error) {
		return &syncer.BALCollector{DB: db, ChainID: chainID, Fetcher: &ethrpc.BALFetcher{Pool: rpcBuild.Pool}, Timeout: cfg.RPC.RequestTimeout, PollInterval: cfg.Runtime.PollInterval, Logger: logger}, nil
	}); err != nil {
		return err
	}
	genesisQueue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		return err
	}
	repairRepository, err := maintenance.NewPostgresRepository(db)
	if err != nil {
		return err
	}
	repairRepository, err = repairRepository.ForOperation(maintenance.OperationRepair)
	if err != nil {
		return err
	}
	repairExecutor, err := maintenance.NewExecutor(chainID, coreRPCSource, coreCanonicalizer, repository, genesisQueue)
	if err != nil {
		return err
	}
	if err := componentRegistry.Register(components.RoleSync, "13-core-repair", func() (components.Service, error) {
		return maintenance.NewWorker(repairRepository, repairExecutor, maintenance.WorkerOptions{WorkerID: runtimeWorkerID("core-repair"), ServiceName: "core-repair", PollInterval: cfg.Runtime.PollInterval, Observer: assembly.businessObserver})
	}); err != nil {
		return err
	}
	genesisImporter, err := genesisstate.NewImporter(
		db, cfg.Chain, genesisQueue, cfg.Runtime.PollInterval, logger,
	)
	if err != nil {
		return err
	}
	if err := componentRegistry.Register(components.RoleSync, "12-genesis-state", func() (components.Service, error) {
		return genesisImporter, nil
	}); err != nil {
		return err
	}
	if cfg.Features.Mempool {
		poller, err := mempool.NewPoller(mempool.PoolSource{Pool: rpcBuild.Pool}, pendingRepository, mempool.PollerOptions{
			ChainID: cfg.Chain.ID, PollInterval: cfg.Mempool.PollInterval,
			Retention: cfg.Mempool.Retention, MaxTransactions: cfg.Mempool.MaxTransactions,
			MaxResponseBytes: cfg.Mempool.MaxResponseBytes, Logger: logger,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleSync, "15-pending-mempool", func() (components.Service, error) {
			return poller, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a runtimeAssembly) registerBeaconAcquisition(queue *enrich.PostgresJobQueue, archive consensus.Archive, namespace string) error {
	client := consensus.NewClient(a.cfg.Consensus)
	if err := a.componentRegistry.Register(components.RoleSync, "32-beacon-inputs", func() (components.Service, error) {
		return &syncer.BeaconCollector{DB: a.db, ChainID: a.cfg.Chain.ID, Client: client, Archive: archive, Namespace: namespace, Timeout: 8 * a.cfg.Consensus.RequestTimeout, PollInterval: a.cfg.Runtime.PollInterval, Logger: a.logger}, nil
	}); err != nil {
		return err
	}
	discovery := &enrich.ConsensusDiscovery{DB: a.db, Queue: queue, Client: client, Config: a.cfg, Logger: a.logger}
	if err := a.componentRegistry.Register(components.RoleSync, "31-consensus-discovery", func() (components.Service, error) { return discovery, nil }); err != nil {
		return err
	}
	return nil
}
