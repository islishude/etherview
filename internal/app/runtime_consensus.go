package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
)

func consensusArchive(ctx context.Context, cfg config.Config) (*accelerator.BlobArchive, error) {
	b := cfg.Consensus.Blobs
	if !b.Enabled {
		return nil, nil
	}
	return accelerator.NewBlobArchive(ctx, b.Endpoint, accelerator.S3Options{Bucket: b.Bucket, Region: b.Region, AccessKey: b.AccessKey, SecretKey: b.SecretKey, SessionToken: b.SessionToken, PathStyle: b.PathStyle, OperationTimeout: cfg.Consensus.RequestTimeout})
}
func (a runtimeAssembly) registerConsensusComponents() error {
	if !a.cfg.Consensus.Enabled || (!a.roleSet[components.RoleSync] && !a.roleSet[components.RoleEnrich]) {
		return nil
	}
	queue, err := enrich.NewPostgresJobQueue(a.db)
	if err != nil {
		return err
	}
	archive, err := consensusArchive(a.ctx, a.cfg)
	if err != nil {
		return err
	}
	namespace := ""
	if a.cfg.Consensus.Blobs.Enabled {
		// genesis_hash is optional in configuration; the bound database identity is authoritative.
		identity, err := store.ReadChainIdentity(a.ctx, a.db, strconv.FormatUint(a.cfg.Chain.ID, 10))
		if err != nil {
			return err
		}
		namespace = identity.ChainID + "/" + strings.TrimPrefix(identity.GenesisHash.Hex(), "0x")
	}
	if a.roleSet[components.RoleSync] {
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
	}
	if !a.roleSet[components.RoleEnrich] {
		return nil
	}
	requests := &enrich.RequestsProcessor{DB: a.db}
	if err := registerWorkerPool(a.componentRegistry, components.RoleEnrich, "36-requests", "worker-requests", a.cfg.Consensus.WorkerCount, func(index int, serviceName string) (components.Service, error) {
		return enrich.NewWorker(queue, []enrich.Processor{requests}, enrich.WorkerOptions{ServiceName: serviceName, ID: runtimeWorkerID(indexedWorkerName("requests", index)), LeaseDuration: a.cfg.Runtime.LeaseDuration, PollInterval: a.cfg.Runtime.PollInterval, Observer: a.businessObserver})
	}); err != nil {
		return err
	}

	for _, blobs := range []bool{false, true} {
		if blobs && !a.cfg.Consensus.Blobs.Enabled {
			continue
		}
		name := "consensus"
		if blobs {
			name = "blob"
		}
		processor := &enrich.ConsensusProcessor{DB: a.db, Archive: archive, Namespace: namespace, Blobs: blobs}
		if err := registerWorkerPool(a.componentRegistry, components.RoleEnrich, "36-"+name, "worker-"+name, a.cfg.Consensus.WorkerCount, func(index int, serviceName string) (components.Service, error) {
			return enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ServiceName: serviceName, ID: runtimeWorkerID(indexedWorkerName(name, index)), LeaseDuration: a.cfg.Runtime.LeaseDuration, PollInterval: a.cfg.Runtime.PollInterval, Observer: a.businessObserver})
		}); err != nil {
			return err
		}
	}
	return nil
}
