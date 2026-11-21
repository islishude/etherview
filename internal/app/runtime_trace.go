package app

import (
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/enrich"
)

func (assembly runtimeAssembly) registerTraceComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	businessObserver := assembly.businessObserver
	traceJobWake := assembly.traceJobWake
	componentRegistry := assembly.componentRegistry
	if cfg.Features.Trace && roleSet[components.RoleTrace] {
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		traceProcessor, err := enrich.NewTraceRPCProcessor(db, nil, enrich.TraceLimits{})
		if err != nil {
			return err
		}
		stateDiffProcessor, err := enrich.NewStateDiffRPCProcessor(db, nil, enrich.StateDiffLimits{})
		if err != nil {
			return err
		}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleTrace,
			"37-trace-enrichment",
			"trace-enrichment-worker",
			cfg.Runtime.WorkerCount,
			func(index int, serviceName string) (components.Service, error) {
				return enrich.NewWorker(queue, []enrich.Processor{traceProcessor, stateDiffProcessor}, enrich.WorkerOptions{
					ServiceName:   serviceName,
					ID:            runtimeWorkerID(indexedWorkerName("trace", index)),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, Wake: traceJobWake,
					Observer: businessObserver,
				})
			},
		); err != nil {
			return err
		}
	}
	return nil
}
