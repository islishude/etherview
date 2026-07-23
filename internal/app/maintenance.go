package app

import (
	"log/slog"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/maintenance"
)

func registerMaintenanceWorkers(
	registry *components.Registry,
	repository maintenance.Repository,
	executor maintenance.RangeExecutor,
	count int,
	options maintenance.WorkerOptions,
) error {
	return registerWorkerPool(
		registry,
		components.RoleMaintenance,
		"45-maintenance",
		"maintenance-worker",
		count,
		func(index int, serviceName string) (components.Service, error) {
			workerOptions := options
			workerOptions.ServiceName = serviceName
			workerOptions.WorkerID = runtimeWorkerID(indexedWorkerName("maintenance", index))
			return maintenance.NewWorker(repository, executor, workerOptions)
		},
	)
}

func registerCatalogHousekeeper(
	registry *components.Registry,
	cleaner maintenance.CatalogCleaner,
	logger *slog.Logger,
	options maintenance.CatalogHousekeeperOptions,
) error {
	housekeeper, err := maintenance.NewCatalogHousekeeper(cleaner, logger, options)
	if err != nil {
		return err
	}
	return registry.Register(components.RoleMaintenance, "46-search-catalog-maintenance", func() (components.Service, error) {
		return housekeeper, nil
	})
}
