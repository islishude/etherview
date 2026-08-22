package app

import (
	"errors"
	"fmt"
	"math"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	genesisstate "github.com/islishude/etherview/internal/genesis"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/syncer"
)

func (assembly runtimeAssembly) registerSyncComponents() error {
	ctx := assembly.ctx
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	logger := assembly.logger
	registry := assembly.registry
	tracker := assembly.tracker
	rpcBuild := assembly.rpcBuild
	repository := assembly.repository
	pendingRepository := assembly.pendingRepository
	runtimeEvents := assembly.runtimeEvents
	signalEvents := assembly.signalEvents
	coreRPCSource := assembly.coreRPCSource
	coreCanonicalizer := assembly.coreCanonicalizer
	componentRegistry := assembly.componentRegistry
	chainID := assembly.chainID
	if roleSet[components.RoleSync] {
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
		genesisQueue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
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
	}
	return nil
}
