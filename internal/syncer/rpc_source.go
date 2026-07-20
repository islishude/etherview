package syncer

import (
	"context"
	"errors"
	"fmt"

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
	var quantity ethrpc.Quantity
	if err := endpoint.Client.Call(ctx, "eth_blockNumber", nil, &quantity); err != nil {
		s.Pool.ReportFailure(endpoint.Name)
		return 0, err
	}
	s.Pool.ReportSuccess(endpoint.Name)
	return quantity.Uint64()
}

func (s *RPCSource) BundleByNumber(ctx context.Context, purpose ethrpc.Purpose, number uint64) (ethrpc.Bundle, error) {
	endpoint, err := s.endpoint(purpose)
	if err != nil {
		return ethrpc.Bundle{}, err
	}
	bundle, err := s.Fetcher.ByNumber(ctx, endpoint, ethrpc.QuantityFromUint64(number))
	if err != nil {
		s.Pool.ReportFailure(endpoint.Name)
		return ethrpc.Bundle{}, err
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
	var block *ethrpc.Block
	if err := endpoint.Client.Call(ctx, "eth_getBlockByNumber", []any{tag, false}, &block); err != nil {
		return nil, fmt.Errorf("fetch %s block: %w", tag, err)
	}
	if block == nil || block.Number == nil || block.Hash == nil {
		return nil, fmt.Errorf("fetch %s block: result is null or incomplete", tag)
	}
	number, err := block.Number.Uint64()
	if err != nil {
		return nil, err
	}
	return &store.BlockRef{Number: number, Hash: *block.Hash, ParentHash: block.ParentHash}, nil
}
