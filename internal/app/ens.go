package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/state"
)

type ensCanonicalSource struct {
	source state.CanonicalSource
}

type ensServiceObserver interface {
	ethrpc.Observer
	ensresolver.Observer
}

func (source ensCanonicalSource) Tip(ctx context.Context) (ensresolver.BlockRef, error) {
	if source.source == nil {
		return ensresolver.BlockRef{}, errors.New("ENS canonical source is nil")
	}
	reference, err := source.source.Tip(ctx)
	return ensresolver.BlockRef{Number: reference.Number, Hash: reference.Hash}, err
}

func (source ensCanonicalSource) IsCanonical(ctx context.Context, reference ensresolver.BlockRef) (bool, error) {
	if source.source == nil {
		return false, errors.New("ENS canonical source is nil")
	}
	return source.source.IsCanonical(ctx, state.CanonicalRef{Number: reference.Number, Hash: reference.Hash})
}

func newENSService(
	ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	rpcBuild *RPCBuild,
	canonical state.CanonicalSource,
	observer ensServiceObserver,
	logger *slog.Logger,
) (*ensresolver.Service, error) {
	if !cfg.Features.ENS {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	officialPool, err := ensOfficialPool(ctx, cfg, rpcBuild, observer, logger)
	if err != nil {
		return nil, err
	}
	gateways := append([]string(nil), cfg.ENS.OfficialGateways...)
	gateways = append(gateways, cfg.ENS.Custom.Gateways...)
	client, err := metadata.New(metadata.Policy{
		Timeout: cfg.ENS.RequestTimeout, MaxBytes: cfg.ENS.MaxResponseBytes,
		MaxRedirects: 1, NoRedirects: true, UserAgent: "etherview-ens/1",
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("configure ENS CCIP client: %w", err)
	}
	gateway, err := ensresolver.NewHTTPGateway(client, gateways)
	if err != nil {
		return nil, fmt.Errorf("configure ENS gateway: %w", err)
	}
	normalizer, err := ensresolver.NewNormalizer()
	if err != nil {
		return nil, err
	}
	resolver, err := ensresolver.NewResolver(normalizer, gateway, cfg.ENS.MaxCCIPDepth)
	if err != nil {
		return nil, err
	}
	repository, err := ensresolver.NewRepository(db, cfg.Chain.ID)
	if err != nil {
		return nil, err
	}
	var custom *ensresolver.CustomOptions
	var customPool *ethrpc.Pool
	if cfg.ENS.Custom.UniversalResolver != "" {
		if rpcBuild == nil || len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 || canonical == nil {
			return nil, errors.New("custom ENS requires current-chain state RPC and canonical source")
		}
		coinType, ok := ensresolver.EVMCoinType(cfg.Chain.ID)
		if cfg.ENS.Custom.CoinType != "" {
			coinType, ok = new(big.Int).SetString(cfg.ENS.Custom.CoinType, 10)
		}
		if !ok || coinType == nil {
			return nil, errors.New("custom ENS coin type is invalid")
		}
		custom = &ensresolver.CustomOptions{
			Registry:          common.HexToAddress(cfg.ENS.Custom.Registry),
			UniversalResolver: common.HexToAddress(cfg.ENS.Custom.UniversalResolver),
			CoinType:          coinType, Gateways: append([]string(nil), cfg.ENS.Custom.Gateways...),
		}
		customPool = rpcBuild.Pool
	}
	return ensresolver.NewService(ensresolver.ServiceOptions{
		ChainID: cfg.Chain.ID, Repository: repository, Resolver: resolver,
		OfficialPool: officialPool, CustomPool: customPool, Canonical: ensCanonicalSource{source: canonical},
		OfficialGateways: cfg.ENS.OfficialGateways, Custom: custom,
		ResolutionFreshness: cfg.ENS.ResolutionFreshness, SnapshotTTL: cfg.ENS.SnapshotTTL,
		FailureTTL: cfg.ENS.FailureTTL, RequestTimeout: cfg.ENS.RequestTimeout,
		MaxBatchAddresses: cfg.ENS.MaxBatchAddresses, MaxConcurrency: cfg.ENS.MaxConcurrency,
		Observer: observer,
	})
}

func ensOfficialPool(
	ctx context.Context,
	cfg config.Config,
	rpcBuild *RPCBuild,
	observer ethrpc.Observer,
	logger *slog.Logger,
) (*ethrpc.Pool, error) {
	if len(cfg.ENS.OfficialRPCEndpoints) == 0 {
		if rpcBuild == nil || rpcBuild.Identity.ChainID != "1" ||
			rpcBuild.Identity.GenesisHash != ensresolver.EthereumMainnetGenesisHash ||
			len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 {
			return nil, errors.New("ENS requires a dedicated Ethereum Mainnet RPC or an exact Mainnet state pool")
		}
		return rpcBuild.Pool, nil
	}
	endpoints := make([]ethrpc.Endpoint, 0, len(cfg.ENS.OfficialRPCEndpoints))
	expected := ethrpc.ChainIdentity{ChainID: "1", GenesisHash: ensresolver.EthereumMainnetGenesisHash}
	for _, item := range cfg.ENS.OfficialRPCEndpoints {
		parsed, err := url.Parse(item.URL)
		if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("configure ENS RPC endpoint %q: invalid HTTP(S) URL", item.Name)
		}
		transport := &http.Transport{
			Proxy:             nil,
			DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 32, MaxIdleConnsPerHost: 8,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			ResponseHeaderTimeout: cfg.ENS.RequestTimeout,
		}
		client, err := ethrpc.NewClient(ctx, item.URL, ethrpc.ClientOptions{
			HTTPClient:        &http.Client{Transport: transport, Timeout: cfg.ENS.RequestTimeout},
			RequestsPerSecond: item.MaxRequests,
		})
		if err != nil {
			return nil, fmt.Errorf("configure ENS RPC endpoint %q: %w", item.Name, err)
		}
		probe := &ethrpc.Endpoint{Name: item.Name, Client: client}
		report, err := ethrpc.ProbeEndpoint(ctx, probe, ethrpc.ProbeOptions{Expected: &expected})
		if err != nil {
			return nil, fmt.Errorf("probe ENS RPC endpoint %q: %w", item.Name, err)
		}
		endpoints = append(endpoints, ethrpc.Endpoint{
			Name: item.Name, Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
			Capabilities: report,
		})
		logger.InfoContext(ctx, "ENS RPC endpoint verified", "rpc", item.Name, "chain_id", "1")
	}
	return ethrpc.NewPool(endpoints, ethrpc.PoolOptions{Observer: observer})
}
