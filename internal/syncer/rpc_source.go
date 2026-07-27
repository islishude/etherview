package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type RPCSource struct {
	Pool    *ethrpc.Pool
	Fetcher ethrpc.Fetcher
}

func (s *RPCSource) Head(ctx context.Context) (uint64, error) {
	endpoint, err := s.endpoint(ethrpc.PurposeHead)
	if err != nil {
		return 0, err
	}
	var quantity hexutil.Uint64
	if err := endpoint.CallContext(ctx, &quantity, "eth_blockNumber"); err != nil {
		s.Pool.ReportFailure(endpoint.Name)
		return 0, ethrpc.SanitizeError(err)
	}
	s.Pool.ReportSuccess(endpoint.Name)
	return uint64(quantity), nil
}

func (s *RPCSource) BundleByNumber(ctx context.Context, purpose ethrpc.Purpose, number uint64) (chainbundle.Bundle, error) {
	endpoint, err := s.endpoint(purpose)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	bundle, err := s.Fetcher.ByNumber(ctx, endpoint, number)
	if err != nil {
		s.Pool.ReportFailure(endpoint.Name)
		return chainbundle.Bundle{}, err
	}
	s.Pool.ReportSuccess(endpoint.Name)
	return bundle, nil
}

func (s *RPCSource) Finality(ctx context.Context) (*store.BlockRef, *store.BlockRef, error) {
	endpoint, err := s.endpoint(ethrpc.PurposeHead)
	if err != nil {
		return nil, nil, err
	}
	var safe, finalized *store.BlockRef
	if endpoint.Capabilities.Status(ethrpc.CapabilitySafeTag) == ethrpc.AvailabilityAvailable {
		safe, err = blockRefByTag(ctx, endpoint, "safe")
		if err != nil {
			s.Pool.ReportFailure(endpoint.Name)
			return nil, nil, err
		}
	}
	if endpoint.Capabilities.Status(ethrpc.CapabilityFinalizedTag) == ethrpc.AvailabilityAvailable {
		finalized, err = blockRefByTag(ctx, endpoint, "finalized")
		if err != nil {
			s.Pool.ReportFailure(endpoint.Name)
			return nil, nil, err
		}
	}
	s.Pool.ReportSuccess(endpoint.Name)
	return safe, finalized, nil
}

func (s *RPCSource) endpoint(purpose ethrpc.Purpose) (*ethrpc.Endpoint, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("RPC sync source has no pool")
	}
	endpoint, err := s.Pool.Acquire(purpose)
	if err != nil && purpose == ethrpc.PurposeHead {
		// A history endpoint is authoritative enough for polling when operators
		// intentionally use one endpoint for both paths.
		endpoint, err = s.Pool.Acquire(ethrpc.PurposeHistory)
	}
	return endpoint, err
}

func blockRefByTag(ctx context.Context, endpoint *ethrpc.Endpoint, tag string) (*store.BlockRef, error) {
	var raw json.RawMessage
	if err := endpoint.CallContext(
		ctx, &raw, "eth_getBlockByNumber", tag, false,
	); err != nil {
		return nil, fmt.Errorf(
			"fetch %s block: %w",
			tag,
			ethrpc.SanitizeError(err),
		)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("fetch %s block: result is null or incomplete", tag)
	}
	header, err := chainbundle.DecodeHeader(raw)
	if err != nil || header.Number == nil || !header.Number.IsUint64() {
		return nil, fmt.Errorf("fetch %s block: result is invalid", tag)
	}
	return &store.BlockRef{
		Number: header.Number.Uint64(), Hash: header.Hash(),
		ParentHash: header.ParentHash,
	}, nil
}
