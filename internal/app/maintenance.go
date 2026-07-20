package app

import (
	"log/slog"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/maintenance"
)

func registerMaintenanceWorker(
	registry *components.Registry,
	repository maintenance.Repository,
	executor maintenance.RangeExecutor,
	options maintenance.WorkerOptions,
) error {
	worker, err := maintenance.NewWorker(repository, executor, options)
	if err != nil {
		return err
	}
	return registry.Register(components.RoleMaintenance, "45-maintenance", func() (components.Service, error) {
		return worker, nil
	})
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
