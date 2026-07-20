package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
)

type RPCBuild struct {
	Pool        *ethrpc.Pool
	Identity    ethrpc.ChainIdentity
	Reports     map[string]ethrpc.CapabilityReport
	WakeURLs    []string
	EndpointNum int
}

func buildRPC(ctx context.Context, cfg config.Config, logger *slog.Logger) (RPCBuild, error) {
	if logger == nil {
		logger = slog.Default()
	}
	chainID := strconv.FormatUint(cfg.Chain.ID, 10)
	expected := ethrpc.ChainIdentity{ChainID: chainID}
	if cfg.Chain.GenesisHash != "" {
		hash, err := ethrpc.ParseHash(cfg.Chain.GenesisHash)
		if err != nil {
			return RPCBuild{}, err
		}
		expected.GenesisHash = hash
	}
	result := RPCBuild{Reports: make(map[string]ethrpc.CapabilityReport)}
	endpoints := make([]ethrpc.Endpoint, 0, len(cfg.RPC.Endpoints))
	var firstHistoryUnavailable *ethrpc.HistoryUnavailableError
	for _, item := range cfg.RPC.Endpoints {
		parsed, err := url.Parse(item.URL)
		if err != nil {
			// URL parser errors may echo credentials from user-info or query
			// parameters. Keep the endpoint name but never return the raw URL.
			return RPCBuild{}, fmt.Errorf("parse RPC endpoint %q: invalid URL", item.Name)
		}
		if parsed.Scheme == "ws" || parsed.Scheme == "wss" {
			result.WakeURLs = append(result.WakeURLs, item.URL)
			logger.InfoContext(ctx, "registered WebSocket wake endpoint", "rpc", item.Name)
			continue
		}
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: cfg.RPC.RequestTimeout,
		}
		client, err := ethrpc.NewHTTPClient(item.URL, ethrpc.HTTPClientOptions{
			HTTPClient: &http.Client{Transport: transport, Timeout: cfg.RPC.RequestTimeout},
		})
		if err != nil {
			return RPCBuild{}, fmt.Errorf("configure RPC endpoint %q: %w", item.Name, err)
		}
		var caller ethrpc.Caller = client
		if item.MaxRequests > 0 {
			limited, err := ethrpc.NewRateClient(client, item.MaxRequests)
			if err != nil {
				return RPCBuild{}, fmt.Errorf("rate limit RPC endpoint %q: %w", item.Name, err)
			}
			caller = limited
		}
		endpoint := ethrpc.Endpoint{Name: item.Name, Client: caller, Purposes: rpcPurposes(item.Purposes)}
		report, err := ethrpc.ProbeEndpoint(ctx, &endpoint, ethrpc.ProbeOptions{
			Expected:   &expected,
			StartBlock: ethrpc.QuantityFromUint64(cfg.Chain.StartBlock),
		})
		if err != nil {
			return RPCBuild{}, fmt.Errorf("probe RPC endpoint %q: %w", item.Name, err)
		}
		if expected.GenesisHash == "" {
			expected.GenesisHash = report.GenesisHash
		}
		endpoint.Capabilities = report
		result.Reports[item.Name] = report
		if endpoint.Supports(ethrpc.PurposeHistory) &&
			report.Status(ethrpc.CapabilityHistoricalData) == ethrpc.AvailabilityUnavailable {
			delete(endpoint.Purposes, ethrpc.PurposeHistory)
			issue := report.HistoryUnavailable
			if issue == nil {
				issue = &ethrpc.HistoryUnavailableError{
					Kind:       ethrpc.HistoryUnavailableResult,
					StartBlock: ethrpc.QuantityFromUint64(cfg.Chain.StartBlock),
				}
			}
			if firstHistoryUnavailable == nil || issue.Kind == ethrpc.HistoryPrunedResult {
				copy := *issue
				firstHistoryUnavailable = &copy
			}
			logger.WarnContext(ctx, "disabled unavailable RPC history purpose",
				"rpc", item.Name,
				"error_code", string(issue.Kind),
				"start_block", cfg.Chain.StartBlock,
			)
		}
		if hasEnabledRPCPurpose(endpoint.Purposes) {
			endpoints = append(endpoints, endpoint)
		}
		logger.InfoContext(ctx, "RPC endpoint verified",
			"rpc", item.Name,
			"chain_id", report.ChainID,
			"capabilities", ethrpc.SortedCapabilities(report),
			"warnings", len(report.Warnings),
		)
	}
	if len(endpoints) == 0 {
		if firstHistoryUnavailable != nil {
			return RPCBuild{}, fmt.Errorf(
				"no usable HTTP(S) RPC endpoint remains after history capability validation: %w",
				firstHistoryUnavailable,
			)
		}
		return RPCBuild{}, errors.New("no HTTP(S) RPC endpoint is configured for authoritative polling")
	}
	pool, err := ethrpc.NewPool(endpoints, ethrpc.PoolOptions{})
	if err != nil {
		return RPCBuild{}, err
	}
	result.Pool = pool
	result.Identity = expected
	result.EndpointNum = len(endpoints)
	return result, nil
}

func hasEnabledRPCPurpose(purposes map[ethrpc.Purpose]bool) bool {
	for _, enabled := range purposes {
		if enabled {
			return true
		}
	}
	return false
}

func rpcPurposes(input []string) map[ethrpc.Purpose]bool {
	result := make(map[ethrpc.Purpose]bool)
	for _, purpose := range input {
		if purpose == "all" {
			result[ethrpc.PurposeHead] = true
			result[ethrpc.PurposeHistory] = true
			result[ethrpc.PurposeState] = true
			result[ethrpc.PurposeTrace] = true
			result[ethrpc.PurposeMempool] = true
			continue
		}
		result[ethrpc.Purpose(purpose)] = true
	}
	return result
}
