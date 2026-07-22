package app

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

func registerMetadataWorker(registry *components.Registry, db *sql.DB, pool *ethrpc.Pool, cfg config.Config, observers ...metadata.FetchObserver) error {
	if registry == nil {
		return fmt.Errorf("register metadata worker: nil component registry")
	}
	repository, err := metadata.NewPostgresRepository(db, strconv.FormatUint(cfg.Chain.ID, 10))
	if err != nil {
		return err
	}
	discoverer, err := metadata.NewSourceDiscoverer(repository, pool, metadata.SourceDiscovererOptions{
		PollInterval: cfg.Runtime.PollInterval, MaxAttempts: metadata.DefaultMaxAttempts,
	})
	if err != nil {
		return err
	}
	client, err := newMetadataClient(cfg)
	if err != nil {
		return fmt.Errorf("configure safe metadata client: %w", err)
	}
	workerOptions := metadata.WorkerOptions{
		WorkerID: runtimeWorkerID("metadata"), LeaseDuration: cfg.Runtime.LeaseDuration,
		PollInterval: cfg.Runtime.PollInterval,
	}
	if len(observers) > 0 {
		workerOptions.Observer = observers[0]
	}
	worker, err := metadata.NewWorker(repository, client, workerOptions)
	if err != nil {
		return err
	}
	if err := registry.Register(components.RoleMetadata, "42-nft-metadata-discovery", func() (components.Service, error) {
		return discoverer, nil
	}); err != nil {
		return err
	}
	return registry.Register(components.RoleMetadata, "45-nft-metadata", func() (components.Service, error) {
		return worker, nil
	})
}

func newMetadataClient(cfg config.Config) (*metadata.Client, error) {
	return metadata.New(metadata.Policy{
		Timeout: cfg.Metadata.FetchTimeout, MaxBytes: int64(cfg.Metadata.MaxDocumentBytes),
		MaxRedirects: cfg.Metadata.MaxRedirects, IPFSGateway: cfg.Metadata.IPFSGateway,
	}, nil)
}
