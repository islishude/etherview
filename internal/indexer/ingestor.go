package indexer

import (
	"context"
	"errors"
	"fmt"

	"github.com/islishude/etherview/internal/ethrpc"
)

// Ingestor acquires one purpose-appropriate endpoint for a block and keeps all
// block/receipt calls on that endpoint. This prevents a load-balanced RPC pool
// from combining facts from different upstream views during a reorg.
type Ingestor struct {
	Pool          *ethrpc.Pool
	Fetcher       ethrpc.Fetcher
	Canonicalizer *Canonicalizer
}

func (i *Ingestor) ByNumber(ctx context.Context, purpose ethrpc.Purpose, number ethrpc.Quantity) (ApplyResult, error) {
	if i == nil || i.Pool == nil || i.Canonicalizer == nil {
		return ApplyResult{}, errors.New("indexer ingestor is not fully configured")
	}
	if purpose != ethrpc.PurposeHead && purpose != ethrpc.PurposeHistory {
		return ApplyResult{}, fmt.Errorf("block ingestion cannot use RPC purpose %q", purpose)
	}
	endpoint, err := i.Pool.Acquire(purpose)
	if err != nil {
		return ApplyResult{}, err
	}
	bundle, err := i.Fetcher.ByNumber(ctx, endpoint, number)
	if err != nil {
		i.Pool.ReportFailure(endpoint.Name)
		return ApplyResult{}, err
	}
	result, err := i.Canonicalizer.Apply(ctx, bundle)
	if err != nil {
		return ApplyResult{}, err
	}
	i.Pool.ReportSuccess(endpoint.Name)
	return result, nil
}

type PoolBundleSource struct {
	Pool    *ethrpc.Pool
	Fetcher ethrpc.Fetcher
	Purpose ethrpc.Purpose
}

func (s *PoolBundleSource) BundleByHash(ctx context.Context, hash ethrpc.Hash) (ethrpc.Bundle, bool, error) {
	if s == nil || s.Pool == nil {
		return ethrpc.Bundle{}, false, errors.New("RPC bundle source has no pool")
	}
	purpose := s.Purpose
	if purpose == "" {
		purpose = ethrpc.PurposeHistory
	}
	endpoint, err := s.Pool.Acquire(purpose)
	if err != nil {
		return ethrpc.Bundle{}, false, err
	}
	bundle, err := s.Fetcher.ByHash(ctx, endpoint, hash)
	if err != nil {
		s.Pool.ReportFailure(endpoint.Name)
		return ethrpc.Bundle{}, false, err
	}
	s.Pool.ReportSuccess(endpoint.Name)
	return bundle, true, nil
}
