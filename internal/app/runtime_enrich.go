package app

import (
	"errors"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
)

func (assembly runtimeAssembly) registerEnrichComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	registry := assembly.registry
	businessObserver := assembly.businessObserver
	rpcBuild := assembly.rpcBuild
	natsWake := assembly.natsWake
	outboxWake := assembly.outboxWake
	enrichJobWake := assembly.enrichJobWake
	componentRegistry := assembly.componentRegistry
	if roleSet[components.RoleEnrich] {
		if rpcBuild == nil || len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 {
			return errors.New("enrich role requires an HTTP state RPC endpoint for block-pinned token detection")
		}
		tokenDetector, err := enrich.NewPoolTokenDetector(rpcBuild.Pool, enrich.TokenProbeLimits{})
		if err != nil {
			return err
		}
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		stages := enrichmentDispatchStages(cfg.Features.Trace)
		dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
			PollInterval: cfg.Runtime.PollInterval,
			Stages:       stages,
			Wake:         outboxWake,
			Published: func() {
				if natsWake != nil {
					natsWake.Signal(accelerator.WakeJobs)
				}
			},
			Observer: businessObserver,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleEnrich, "30-enrichment-outbox", func() (components.Service, error) {
			return dispatcher, nil
		}); err != nil {
			return err
		}
		tokenProcessor, err := enrich.NewPostgresTokenProcessorWithDetector(db, tokenDetector)
		if err != nil {
			return err
		}
		proxyProcessor, err := enrich.NewPostgresProxyProcessorWithOptions(
			db, rpcBuild.Pool, enrich.ProxyLimits{}, enrich.ProxyDetectionOptions{
				Enabled: cfg.Features.ProxyDetectionV2, SafeEnabled: cfg.Features.SafeProxyDetection,
				DiamondEnabled: cfg.Features.DiamondProxyDetection,
				Observer:       registry,
			},
		)
		if err != nil {
			return err
		}
		abiProcessor, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
		if err != nil {
			return err
		}
		statsProcessor, err := enrich.NewPostgresStatsProcessor(db)
		if err != nil {
			return err
		}
		processors := []enrich.Processor{proxyProcessor, abiProcessor, tokenProcessor, statsProcessor}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleEnrich,
			"35-core-enrichment",
			"enrichment-worker",
			cfg.Runtime.WorkerCount,
			func(index int, serviceName string) (components.Service, error) {
				return enrich.NewWorker(queue, processors, enrich.WorkerOptions{
					ServiceName:   serviceName,
					ID:            runtimeWorkerID(indexedWorkerName("enrich", index)),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, Wake: enrichJobWake,
					Observer: businessObserver,
				})
			},
		); err != nil {
			return err
		}
	}
	return nil
}
