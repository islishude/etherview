package app

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/components"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/metadata"
)

func (a runtimeAssembly) registerENSComponents() error {
	cfg := a.cfg
	if !a.roleSet[components.RoleEnrich] || !cfg.Features.ENS {
		return nil
	}
	if a.rpcBuild == nil || a.rpcBuild.Pool == nil {
		return errors.New("ENS requires current-chain RPC pool")
	}
	m, e := loadENSManifest(cfg.ENS.Manifest)
	if e != nil {
		return e
	}
	chain := strconv.FormatUint(cfg.Chain.ID, 10)
	if m.TargetChainID != chain || m.SourceChainID != chain || m.Policy != common.HexToHash(cfg.ENS.PolicyDigest) || (cfg.Chain.GenesisHash != "" && m.Genesis != common.HexToHash(cfg.Chain.GenesisHash)) {
		return enssync.ErrSourceIdentity
	}
	repo, e := enssync.NewRepository(a.db)
	if e != nil {
		return e
	}
	client, e := metadata.New(metadata.Policy{Timeout: cfg.ENS.RequestTimeout, MaxBytes: cfg.ENS.MaxResponseBytes, MaxRedirects: 1, NoRedirects: true, UserAgent: "etherview-ens/1"}, nil)
	if e != nil {
		return e
	}
	gateway, e := ensresolver.NewHTTPGateway(client, cfg.ENS.Gateways)
	if e != nil {
		return e
	}
	normalizer, e := ensresolver.NewNormalizer()
	if e != nil {
		return e
	}
	resolver, e := ensresolver.NewResolver(normalizer, gateway, cfg.ENS.MaxCCIPDepth)
	if e != nil {
		return e
	}
	background, e := enssync.NewRuntime(repo, enssync.RuntimeOptions{
		Source: enssync.RuntimeSource{Manifest: m, Pool: a.rpcBuild.Pool, Gateways: cfg.ENS.Gateways},
		Policy: enssync.EffectivePolicy{TargetChainID: chain, CoinType: m.CoinType, Digest: m.Policy}, PollInterval: cfg.Runtime.PollInterval, Logger: a.logger, Observer: a.businessObserver,
		Collector: enssync.CollectorOptions{MaxBytes: int(cfg.ENS.MaxResponseBytes), Health: cfg.ENS.ResolutionFreshness},
		Worker:    enssync.WorkerOptions{Resolver: resolver, Freshness: cfg.ENS.ResolutionFreshness, Retention: cfg.ENS.SnapshotTTL, Timeout: cfg.ENS.RequestTimeout, FailureDelay: cfg.ENS.FailureTTL}})
	if e != nil {
		return e
	}
	if e = a.componentRegistry.Register(components.RoleEnrich, "38-ens-collection", func() (components.Service, error) { return background.CollectionService(), nil }); e != nil {
		return e
	}
	return registerWorkerPool(a.componentRegistry, components.RoleEnrich, "39-ens-resolution", "ens-resolution", cfg.ENS.MaxConcurrency, func(index int, _ string) (components.Service, error) { return background.ResolutionService(index), nil })
}
func loadENSManifest(path string) (enssync.Manifest, error) {
	if strings.TrimSpace(path) == "" {
		return enssync.Manifest{}, errors.New("ENS deployment manifest is required")
	}
	file, e := os.Open(path)
	if e != nil {
		return enssync.Manifest{}, errors.New("ENS deployment manifest is unreadable")
	}
	defer func() { _ = file.Close() }()
	return enssync.ReadManifest(file)
}
