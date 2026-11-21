package app

import (
	"strconv"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/metadata"
)

func (assembly runtimeAssembly) registerMetadataComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	logger := assembly.logger
	businessObserver := assembly.businessObserver
	componentRegistry := assembly.componentRegistry
	if !cfg.Features.NFTMetadata {
		return nil
	}
	if roleSet[components.RoleMetadata] {
		repo, err := metadata.NewPostgresRepository(db, strconv.FormatUint(cfg.Chain.ID, 10))
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleMetadata, "42-nft-metadata-discovery", func() (components.Service, error) {
			return metadata.NewSourceDiscoverer(repo, nil, metadata.SourceDiscovererOptions{PollInterval: cfg.Runtime.PollInterval, MaxAttempts: metadata.DefaultMaxAttempts, Logger: logger})
		}); err != nil {
			return err
		}
		return registerMetadataWorkers(componentRegistry, db, cfg, logger, businessObserver)
	}
	return nil
}
