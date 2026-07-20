package app

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/metadata"
)

func registerMetadataWorker(registry *components.Registry, db *sql.DB, cfg config.Config) error {
	if registry == nil {
		return fmt.Errorf("register metadata worker: nil component registry")
	}
	repository, err := metadata.NewPostgresRepository(db, strconv.FormatUint(cfg.Chain.ID, 10))
	if err != nil {
		return err
	}
	client, err := newMetadataClient(cfg)
	if err != nil {
		return fmt.Errorf("configure safe metadata client: %w", err)
	}
	worker, err := metadata.NewWorker(repository, client, metadata.WorkerOptions{
		WorkerID: runtimeWorkerID("metadata"), LeaseDuration: cfg.Runtime.LeaseDuration,
		PollInterval: cfg.Runtime.PollInterval,
	})
	if err != nil {
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
