package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

func (a runtimeAssembly) registerENSComponents() (err error) {
	cfg := a.cfg
	if !a.roleSet[components.RoleENS] || !cfg.Features.ENS {
		return nil
	}
	repo, e := enssync.NewRepository(a.db)
	if e != nil {
		return e
	}
	client, e := metadata.New(metadata.Policy{Timeout: cfg.ENS.RequestTimeout, MaxBytes: cfg.ENS.MaxResponseBytes, MaxRedirects: 1, NoRedirects: true, UserAgent: "etherview-ens/1"}, nil)
	if e != nil {
		return e
	}
	gateways := append([]string(nil), cfg.ENS.Custom.Gateways...)
	if cfg.ENS.Mode != "custom_only" {
		gateways = append(gateways, cfg.ENS.OfficialGateways...)
	}
	gateway, e := ensresolver.NewHTTPGateway(client, gateways)
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
	coin, _ := ensresolver.EVMCoinType(cfg.Chain.ID)
	customCoin := cfg.ENS.Custom.CoinType
	if customCoin == "" {
		customCoin = coin.String()
	}
	options := enssync.RuntimeOptions{Policy: enssync.EffectivePolicy{TargetChainID: strconv.FormatUint(cfg.Chain.ID, 10), CoinType: coin.String(), CustomCoinType: customCoin, Mode: cfg.ENS.Mode, Digest: common.HexToHash(cfg.ENS.PolicyDigest)}, PollInterval: cfg.Runtime.PollInterval, Logger: a.logger, Observer: a.businessObserver,
		Collector: enssync.CollectorOptions{MaxBytes: int(cfg.ENS.MaxResponseBytes), Health: cfg.ENS.ResolutionFreshness},
		Worker:    enssync.WorkerOptions{Resolver: resolver, Freshness: cfg.ENS.ResolutionFreshness, Retention: cfg.ENS.SnapshotTTL, Timeout: cfg.ENS.RequestTimeout, FailureDelay: cfg.ENS.FailureTTL}}
	var clients []*rpc.Client
	defer func() {
		if err != nil {
			for _, client := range clients {
				client.Close()
			}
		}
	}()
	for _, namespace := range []enscontract.Namespace{enscontract.Official, enscontract.Custom} {
		file := cfg.ENS.OfficialManifest
		endpoints := cfg.ENS.OfficialRPCEndpoints
		sourceCoin := coin.String()
		allowed := cfg.ENS.OfficialGateways
		if namespace == enscontract.Official {
			if cfg.ENS.Mode == "custom_only" {
				continue
			}
			if len(endpoints) == 0 && cfg.Chain.ID == 1 {
				endpoints = cfg.RPC.Endpoints
			}
		} else {
			if cfg.ENS.Custom.UniversalResolver == "" {
				continue
			}
			file = cfg.ENS.CustomManifest
			endpoints = cfg.RPC.Endpoints
			sourceCoin = customCoin
			allowed = cfg.ENS.Custom.Gateways
		}
		m, e := loadENSManifest(file)
		if e != nil {
			return e
		}
		if m.TargetChainID != options.Policy.TargetChainID || m.Policy != options.Policy.Digest || m.Namespace != namespace || m.CoinType != sourceCoin {
			return enssync.ErrSourceIdentity
		}
		if namespace == enscontract.Custom && (m.Registry != common.HexToAddress(cfg.ENS.Custom.Registry) || m.UniversalResolver != common.HexToAddress(cfg.ENS.Custom.UniversalResolver)) {
			return enssync.ErrSourceIdentity
		}
		pool, opened, e := newENSSourcePool(a.ctx, endpoints, cfg, a.businessObserver)
		clients = append(clients, opened...)
		if e != nil {
			return e
		}
		options.Sources = append(options.Sources, enssync.RuntimeSource{Manifest: m, Pool: pool, Gateways: allowed})
	}
	background, e := enssync.NewRuntime(repo, options)
	if e != nil {
		return e
	}
	if e = a.componentRegistry.Register(components.RoleENS, "38-ens-collection", func() (components.Service, error) {
		return &ensCollectionOwner{service: background.CollectionService(), clients: clients}, nil
	}); e != nil {
		return e
	}
	return registerWorkerPool(a.componentRegistry, components.RoleENS, "39-ens-resolution", "ens-resolution", cfg.ENS.MaxConcurrency, func(index int, _ string) (components.Service, error) { return background.ResolutionService(index), nil })
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
func newENSSourcePool(ctx context.Context, endpoints []config.RPCEndpoint, cfg config.Config, observer ethrpc.Observer) (*ethrpc.Pool, []*rpc.Client, error) {
	var opened []*rpc.Client
	var sources []ethrpc.Endpoint
	for _, endpoint := range endpoints {
		parsed, e := url.Parse(endpoint.URL)
		if e != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		state := false
		history := false
		for _, purpose := range endpoint.Purposes {
			state = state || purpose == "state" || purpose == "all"
			history = history || purpose == "history" || purpose == "all"
		}
		if !state && !history {
			continue
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client, e := ethrpc.NewClient(ctx, endpoint.URL, ethrpc.ClientOptions{HTTPClient: &http.Client{Transport: transport, Timeout: cfg.ENS.RequestTimeout}, MaxResponseBytes: cfg.ENS.MaxResponseBytes, RequestsPerSecond: endpoint.MaxRequests})
		if e != nil {
			return nil, opened, errors.New("ENS RPC client configuration is invalid")
		}
		opened = append(opened, client)
		sources = append(sources, ethrpc.Endpoint{Name: endpoint.Name, Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: state, ethrpc.PurposeHistory: history}})
	}
	pool, e := ethrpc.NewPool(sources, ethrpc.PoolOptions{Observer: observer})
	return pool, opened, e
}

type ensCollectionOwner struct {
	service *enssync.CollectionService
	clients []*rpc.Client
}

func (o *ensCollectionOwner) Name() string { return o.service.Name() }
func (o *ensCollectionOwner) Run(ctx context.Context) error {
	defer func() {
		for _, client := range o.clients {
			client.Close()
		}
	}()
	return o.service.Run(ctx)
}
