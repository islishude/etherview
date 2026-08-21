package app

import (
	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/maintenance"
)

func (assembly runtimeAssembly) registerMaintenanceComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	logger := assembly.logger
	registry := assembly.registry
	businessObserver := assembly.businessObserver
	repository := assembly.repository
	coreRPCSource := assembly.coreRPCSource
	coreCanonicalizer := assembly.coreCanonicalizer
	componentRegistry := assembly.componentRegistry
	chainID := assembly.chainID
	if roleSet[components.RoleMaintenance] {
		rollupWorker, err := analytics.NewRollupWorker(db, analytics.RollupWorkerOptions{
			ChainID: cfg.Chain.ID, PollInterval: cfg.Runtime.PollInterval,
			Logger: logger, Observer: registry,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleMaintenance, "44-historical-analytics-rollup", func() (components.Service, error) {
			return rollupWorker, nil
		}); err != nil {
			return err
		}
		requestRepository, err := maintenance.NewPostgresRepository(db)
		if err != nil {
			return err
		}
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		executor, err := maintenance.NewExecutor(chainID, coreRPCSource, coreCanonicalizer, repository, queue)
		if err != nil {
			return err
		}
		if err := registerMaintenanceWorkers(componentRegistry, requestRepository, executor, cfg.Runtime.WorkerCount, maintenance.WorkerOptions{
			PollInterval: cfg.Runtime.PollInterval, Observer: businessObserver,
		}); err != nil {
			return err
		}
		catalogCleaner, err := maintenance.NewPostgresCatalogCleaner(db)
		if err != nil {
			return err
		}
		if err := registerCatalogHousekeeper(componentRegistry, catalogCleaner, logger, maintenance.CatalogHousekeeperOptions{
			ChainID: cfg.Chain.ID, Interval: cfg.Maintenance.Interval,
			RetentionGenerations: cfg.Maintenance.SearchRetentionGenerations,
			AdapterDeleteBatch:   cfg.Maintenance.AdapterDeleteBatch,
		}); err != nil {
			return err
		}
		// Authentication and billing housekeeping deliberately receives only
		// the writer pool. The maintenance role does not load the API-only
		// session, fingerprint, or facilitator-header Secrets.
		if err := registerAuthBillingHousekeepers(
			componentRegistry, db, cfg, logger,
		); err != nil {
			return err
		}
	}
	return nil
}
