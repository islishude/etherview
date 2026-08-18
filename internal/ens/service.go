package ens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	directionForward = "forward"
	directionPrimary = "primary"
)

type CanonicalSource interface {
	Tip(context.Context) (BlockRef, error)
	IsCanonical(context.Context, BlockRef) (bool, error)
}

type CustomOptions struct {
	Registry          common.Address
	UniversalResolver common.Address
	CoinType          *big.Int
	Gateways          []string
}

type ServiceOptions struct {
	ChainID             uint64
	Repository          *Repository
	Resolver            *Resolver
	OfficialPool        *ethrpc.Pool
	CustomPool          *ethrpc.Pool
	Canonical           CanonicalSource
	OfficialGateways    []string
	Custom              *CustomOptions
	ResolutionFreshness time.Duration
	SnapshotTTL         time.Duration
	FailureTTL          time.Duration
	RequestTimeout      time.Duration
	MaxBatchAddresses   int
	MaxConcurrency      int
	Now                 func() time.Time
	Observer            Observer
}

type MetricObservation struct {
	Source    Source
	Direction string
	Outcome   string
	Duration  time.Duration
}

type Observer interface {
	RecordENS(MetricObservation)
}

type Service struct {
	chainID             uint64
	repository          *Repository
	resolver            *Resolver
	officialPool        *ethrpc.Pool
	customPool          *ethrpc.Pool
	canonical           CanonicalSource
	officialGateways    []string
	custom              *CustomOptions
	coinType            *big.Int
	policyKey           string
	resolutionFreshness time.Duration
	snapshotTTL         time.Duration
	failureTTL          time.Duration
	requestTimeout      time.Duration
	maxBatchAddresses   int
	maxConcurrency      int
	now                 func() time.Time
	observer            Observer
}

type ForwardResolution struct {
	ObservationID int64
	Outcome       Outcome
	Name          string
	Address       common.Address
	Source        Source
}

type PrimaryResolution struct {
	ObservationID int64
	Outcome       Outcome
	Name          string
	Address       common.Address
	Source        Source
	Code          string
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.ChainID == 0 || options.Repository == nil || options.Resolver == nil || options.OfficialPool == nil {
		return nil, errors.New("ENS service core dependencies are incomplete")
	}
	coinType, ok := EVMCoinType(options.ChainID)
	if !ok {
		return nil, errors.New("ENS service chain coin type is unavailable")
	}
	if len(options.OfficialGateways) == 0 || len(options.OfficialGateways) > 4 {
		return nil, errors.New("ENS official gateways must contain 1 to 4 URLs")
	}
	if options.ResolutionFreshness <= 0 || options.SnapshotTTL < options.ResolutionFreshness ||
		options.FailureTTL <= 0 || options.RequestTimeout <= 0 || options.MaxBatchAddresses <= 0 ||
		options.MaxBatchAddresses > 100 || options.MaxConcurrency <= 0 || options.MaxConcurrency > 16 {
		return nil, errors.New("ENS service bounds are invalid")
	}
	if options.Custom != nil {
		if options.CustomPool == nil || options.Canonical == nil || options.Custom.Registry == (common.Address{}) ||
			options.Custom.UniversalResolver == (common.Address{}) || options.Custom.CoinType == nil ||
			options.Custom.CoinType.Sign() <= 0 || options.Custom.CoinType.BitLen() > 256 || len(options.Custom.Gateways) > 4 {
			return nil, errors.New("custom ENS service dependencies are invalid")
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	policyKey, err := ensPolicyKey(options, coinType)
	if err != nil {
		return nil, err
	}
	return &Service{
		chainID: options.ChainID, repository: options.Repository, resolver: options.Resolver,
		officialPool: options.OfficialPool, customPool: options.CustomPool, canonical: options.Canonical,
		officialGateways: slices.Clone(options.OfficialGateways), custom: cloneCustomOptions(options.Custom),
		coinType: coinType, policyKey: policyKey, resolutionFreshness: options.ResolutionFreshness,
		snapshotTTL: options.SnapshotTTL, failureTTL: options.FailureTTL,
		requestTimeout: options.RequestTimeout, maxBatchAddresses: options.MaxBatchAddresses,
		maxConcurrency: options.MaxConcurrency, now: options.Now, observer: options.Observer,
	}, nil
}

func cloneCustomOptions(options *CustomOptions) *CustomOptions {
	if options == nil {
		return nil
	}
	return &CustomOptions{
		Registry: options.Registry, UniversalResolver: options.UniversalResolver,
		CoinType: new(big.Int).Set(options.CoinType), Gateways: slices.Clone(options.Gateways),
	}
}

func ensPolicyKey(options ServiceOptions, coinType *big.Int) (string, error) {
	type customPolicy struct {
		Registry          string   `json:"registry"`
		UniversalResolver string   `json:"universal_resolver"`
		CoinType          string   `json:"coin_type"`
		Gateways          []string `json:"gateways"`
		Endpoints         []string `json:"endpoints"`
	}
	policy := struct {
		Version           int           `json:"version"`
		ChainID           uint64        `json:"chain_id"`
		CoinType          string        `json:"coin_type"`
		UniversalResolver string        `json:"universal_resolver"`
		Gateways          []string      `json:"gateways"`
		Endpoints         []string      `json:"endpoints"`
		Custom            *customPolicy `json:"custom,omitempty"`
	}{
		Version: 1, ChainID: options.ChainID, CoinType: coinType.String(),
		UniversalResolver: strings.ToLower(OfficialUniversalResolverAddress),
		Gateways:          slices.Clone(options.OfficialGateways),
		Endpoints:         options.OfficialPool.Names(ethrpc.PurposeState),
	}
	if options.Custom != nil {
		policy.Custom = &customPolicy{
			Registry:          strings.ToLower(options.Custom.Registry.Hex()),
			UniversalResolver: strings.ToLower(options.Custom.UniversalResolver.Hex()),
			CoinType:          options.Custom.CoinType.String(), Gateways: slices.Clone(options.Custom.Gateways),
			Endpoints: options.CustomPool.Names(ethrpc.PurposeState),
		}
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode ENS policy identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (service *Service) currentGeneration(ctx context.Context) (Generation, error) {
	now := service.now().UTC()
	if current, exists, err := service.repository.FreshGeneration(ctx, service.policyKey, now); err != nil {
		return Generation{}, err
	} else if exists {
		return current, nil
	}
	officialEndpoint, err := service.officialPool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return Generation{}, resolutionError(CodeRPCUnavailable)
	}
	callCtx, cancel := context.WithTimeout(ctx, service.requestTimeout)
	officialBlock, sourceErr := FinalizedMainnetRef(callCtx, officialEndpoint)
	cancel()
	if sourceErr != nil {
		service.officialPool.ReportFailure(officialEndpoint.Name)
		return Generation{}, sourceErr
	}
	service.officialPool.ReportSuccess(officialEndpoint.Name)
	candidate := GenerationCandidate{
		PolicyKey: service.policyKey, CoinType: service.coinType,
		OfficialEndpoint: officialEndpoint.Name, OfficialBlock: officialBlock,
		CreatedAt: now, FreshUntil: now.Add(service.resolutionFreshness),
		RetainUntil: now.Add(service.snapshotTTL),
	}
	if service.custom != nil {
		customBlock, err := service.canonical.Tip(ctx)
		if err != nil {
			return Generation{}, fmt.Errorf("read custom ENS canonical tip: %w", err)
		}
		customEndpoint, err := service.customPool.Acquire(ethrpc.PurposeState)
		if err != nil {
			return Generation{}, resolutionError(CodeRPCUnavailable)
		}
		profile := service.customProfile(customBlock)
		callCtx, cancel = context.WithTimeout(ctx, service.requestTimeout)
		verifyErr := VerifyCustomDeployment(callCtx, customEndpoint, profile)
		cancel()
		if verifyErr != nil {
			service.customPool.ReportFailure(customEndpoint.Name)
			return Generation{}, verifyErr
		}
		canonical, err := service.canonical.IsCanonical(ctx, customBlock)
		if err != nil {
			return Generation{}, fmt.Errorf("recheck custom ENS canonical tip: %w", err)
		}
		if !canonical {
			return Generation{}, errors.New("custom ENS canonical tip changed during generation creation")
		}
		service.customPool.ReportSuccess(customEndpoint.Name)
		candidate.CustomEndpoint = customEndpoint.Name
		candidate.CustomCoinType = service.custom.CoinType
		candidate.CustomBlock = &customBlock
	}
	return service.repository.CreateGeneration(ctx, candidate)
}

func (service *Service) officialProfile(generation Generation) Profile {
	return Profile{
		Source: SourceOfficial, UniversalResolver: common.HexToAddress(OfficialUniversalResolverAddress),
		CoinType: new(big.Int).Set(generation.CoinType), Gateways: slices.Clone(service.officialGateways),
		Block: generation.OfficialBlock,
	}
}

func (service *Service) customProfile(block BlockRef) Profile {
	return Profile{
		Source: SourceCustom, Registry: service.custom.Registry,
		UniversalResolver: service.custom.UniversalResolver,
		CoinType:          new(big.Int).Set(service.custom.CoinType), Gateways: slices.Clone(service.custom.Gateways),
		Block: block,
	}
}

func (service *Service) customGenerationProfile(generation Generation) (Profile, bool) {
	if service.custom == nil || generation.CustomBlock == nil || generation.CustomCoinType == nil {
		return Profile{}, false
	}
	profile := service.customProfile(*generation.CustomBlock)
	profile.CoinType = new(big.Int).Set(generation.CustomCoinType)
	return profile, true
}

func (service *Service) ResolveForward(ctx context.Context, rawName string) (ForwardResolution, error) {
	name, err := service.resolver.normalizer.Normalize(ctx, rawName)
	if err != nil {
		return ForwardResolution{}, err
	}
	generation, err := service.currentGeneration(ctx)
	if err != nil {
		return ForwardResolution{}, err
	}
	result, err := service.resolveForwardSource(ctx, generation, SourceOfficial, name)
	if err != nil || result.Outcome == OutcomeResolved {
		if err == nil && result.Outcome == OutcomeResolved {
			err = service.repository.EnsureObservationPublished(ctx, result.ObservationID)
		}
		return result, err
	}
	if _, enabled := service.customGenerationProfile(generation); !enabled {
		return result, nil
	}
	result, err = service.resolveForwardSource(ctx, generation, SourceCustom, name)
	if err == nil && result.Outcome == OutcomeResolved {
		err = service.repository.EnsureObservationPublished(ctx, result.ObservationID)
	}
	return result, err
}

func (service *Service) resolveForwardSource(
	ctx context.Context,
	generation Generation,
	source Source,
	name string,
) (ForwardResolution, error) {
	if cached, exists, err := service.repository.Observation(
		ctx, generation.ID, source, directionForward, name,
	); err != nil {
		return ForwardResolution{}, err
	} else if exists {
		service.observe(source, directionForward, "cache_hit", time.Time{})
		return forwardResolution(cached), nil
	}
	now := service.now().UTC()
	if code, exists, err := service.repository.FreshFailure(
		ctx, generation.ID, source, directionForward, name, now,
	); err != nil {
		return ForwardResolution{}, err
	} else if exists {
		return ForwardResolution{}, resolutionError(code)
	}
	profile, caller, pool, err := service.source(generation, source)
	if err != nil {
		return ForwardResolution{}, err
	}
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, service.requestTimeout)
	resolved, resolveErr := service.resolver.forwardNormalized(callCtx, caller, profile, name)
	cancel()
	if resolveErr != nil {
		service.observe(source, directionForward, "error", started)
		service.reportSourceResult(pool, caller.Name, resolveErr)
		if ctx.Err() != nil {
			return ForwardResolution{}, ctx.Err()
		}
		code := resolutionCode(resolveErr)
		if err := service.repository.RecordFailure(
			ctx, generation.ID, source, directionForward, name, code, now, now.Add(service.failureTTL),
		); err != nil {
			return ForwardResolution{}, err
		}
		return ForwardResolution{}, resolutionError(code)
	}
	service.observe(source, directionForward, string(resolved.Outcome), started)
	pool.ReportSuccess(caller.Name)
	observation := Observation{
		GenerationID: generation.ID, Source: source, Direction: directionForward,
		LookupKey: name, Outcome: resolved.Outcome, Name: name, Address: resolved.Address,
		Resolver: resolved.Resolver, ObservedAt: now,
	}
	stored, err := service.repository.RecordObservation(ctx, observation)
	if err != nil {
		return ForwardResolution{}, err
	}
	return forwardResolution(stored), nil
}

func forwardResolution(observation Observation) ForwardResolution {
	return ForwardResolution{
		ObservationID: observation.ID, Outcome: observation.Outcome, Name: observation.Name,
		Address: observation.Address, Source: observation.Source,
	}
}

func (service *Service) ResolvePrimary(
	ctx context.Context,
	generation Generation,
	address common.Address,
) (PrimaryResolution, error) {
	result, err := service.resolvePrimarySource(ctx, generation, SourceOfficial, address)
	if err != nil || result.Outcome == OutcomeResolved {
		return result, err
	}
	if _, enabled := service.customGenerationProfile(generation); !enabled {
		return result, nil
	}
	return service.resolvePrimarySource(ctx, generation, SourceCustom, address)
}

func (service *Service) ResolveCurrentPrimary(
	ctx context.Context,
	address common.Address,
) (PrimaryResolution, error) {
	generation, err := service.currentGeneration(ctx)
	if err != nil {
		return PrimaryResolution{}, err
	}
	return service.ResolvePrimary(ctx, generation, address)
}

func (service *Service) resolvePrimarySource(
	ctx context.Context,
	generation Generation,
	source Source,
	address common.Address,
) (PrimaryResolution, error) {
	lookupKey := strings.ToLower(address.Hex())
	if cached, exists, err := service.repository.Observation(
		ctx, generation.ID, source, directionPrimary, lookupKey,
	); err != nil {
		return PrimaryResolution{}, err
	} else if exists {
		service.observe(source, directionPrimary, "cache_hit", time.Time{})
		return primaryResolution(cached), nil
	}
	now := service.now().UTC()
	if code, exists, err := service.repository.FreshFailure(
		ctx, generation.ID, source, directionPrimary, lookupKey, now,
	); err != nil {
		return PrimaryResolution{}, err
	} else if exists {
		return PrimaryResolution{Address: address, Code: code}, resolutionError(code)
	}
	profile, caller, pool, err := service.source(generation, source)
	if err != nil {
		return PrimaryResolution{}, err
	}
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, service.requestTimeout)
	resolved, resolveErr := service.resolver.Reverse(callCtx, caller, profile, address)
	cancel()
	if resolveErr != nil {
		service.observe(source, directionPrimary, "error", started)
		service.reportSourceResult(pool, caller.Name, resolveErr)
		if ctx.Err() != nil {
			return PrimaryResolution{}, ctx.Err()
		}
		code := resolutionCode(resolveErr)
		if err := service.repository.RecordFailure(
			ctx, generation.ID, source, directionPrimary, lookupKey, code, now, now.Add(service.failureTTL),
		); err != nil {
			return PrimaryResolution{}, err
		}
		return PrimaryResolution{Address: address, Code: code}, resolutionError(code)
	}
	service.observe(source, directionPrimary, string(resolved.Outcome), started)
	pool.ReportSuccess(caller.Name)
	observation := Observation{
		GenerationID: generation.ID, Source: source, Direction: directionPrimary,
		LookupKey: lookupKey, Outcome: resolved.Outcome, Name: resolved.Name,
		Address: address, Resolver: resolved.Resolver, ReverseResolver: resolved.ReverseResolver,
		ObservedAt: now,
	}
	stored, err := service.repository.RecordObservation(ctx, observation)
	if err != nil {
		return PrimaryResolution{}, err
	}
	if stored.Outcome == OutcomeResolved {
		_, err = service.repository.RecordObservation(ctx, Observation{
			GenerationID: generation.ID, Source: source, Direction: directionForward,
			LookupKey: stored.Name, Outcome: OutcomeResolved, Name: stored.Name,
			Address: stored.Address, Resolver: stored.Resolver, ObservedAt: now,
		})
		if err != nil {
			return PrimaryResolution{}, err
		}
	}
	return primaryResolution(stored), nil
}

func primaryResolution(observation Observation) PrimaryResolution {
	return PrimaryResolution{
		ObservationID: observation.ID, Outcome: observation.Outcome, Name: observation.Name,
		Address: observation.Address, Source: observation.Source,
	}
}

func (service *Service) source(
	generation Generation,
	source Source,
) (Profile, *ethrpc.Endpoint, *ethrpc.Pool, error) {
	switch source {
	case SourceOfficial:
		endpoint, err := service.officialPool.AcquireNamed(ethrpc.PurposeState, generation.OfficialEndpoint)
		return service.officialProfile(generation), endpoint, service.officialPool, err
	case SourceCustom:
		profile, ok := service.customGenerationProfile(generation)
		if !ok {
			return Profile{}, nil, nil, errors.New("custom ENS source is unavailable")
		}
		endpoint, err := service.customPool.AcquireNamed(ethrpc.PurposeState, generation.CustomEndpoint)
		return profile, endpoint, service.customPool, err
	default:
		return Profile{}, nil, nil, errors.New("ENS source is invalid")
	}
}

func (service *Service) reportSourceResult(pool *ethrpc.Pool, endpoint string, err error) {
	var resolution *ResolutionError
	if errors.As(err, &resolution) && (resolution.Code == CodeRPCUnavailable || resolution.Code == CodeSourceIdentity) {
		pool.ReportFailure(endpoint)
		return
	}
	pool.ReportSuccess(endpoint)
}

func resolutionCode(err error) string {
	var resolution *ResolutionError
	if errors.As(err, &resolution) && resolution.Code != "" {
		return resolution.Code
	}
	return CodeResolverFailure
}

func (service *Service) observe(source Source, direction, outcome string, started time.Time) {
	if service == nil || service.observer == nil {
		return
	}
	duration := time.Duration(0)
	if !started.IsZero() {
		duration = time.Since(started)
	}
	service.observer.RecordENS(MetricObservation{
		Source: source, Direction: direction, Outcome: outcome, Duration: duration,
	})
}

func (service *Service) NewAddressNameSnapshot(ctx context.Context) (Generation, string, error) {
	generation, err := service.currentGeneration(ctx)
	if err != nil {
		return Generation{}, "", err
	}
	now := service.now().UTC()
	id, err := service.repository.CreateSnapshot(ctx, generation.ID, now, now.Add(service.snapshotTTL))
	return generation, id, err
}

func (service *Service) AddressNameGeneration(
	ctx context.Context,
	snapshotID string,
) (Generation, error) {
	return service.repository.SnapshotGeneration(ctx, snapshotID, service.policyKey, service.now().UTC())
}

func (service *Service) ResolveAddressBatch(
	ctx context.Context,
	addresses []common.Address,
	snapshotID string,
) ([]PrimaryResolution, string, error) {
	if len(addresses) == 0 || len(addresses) > service.maxBatchAddresses {
		return nil, "", errors.New("ENS address batch size is invalid")
	}
	var generation Generation
	var err error
	if snapshotID == "" {
		generation, snapshotID, err = service.NewAddressNameSnapshot(ctx)
	} else {
		generation, err = service.AddressNameGeneration(ctx, snapshotID)
	}
	if err != nil {
		return nil, "", err
	}
	results := make([]PrimaryResolution, len(addresses))
	semaphore := make(chan struct{}, service.maxConcurrency)
	errCh := make(chan error, len(addresses))
	for index, address := range addresses {
		go func() {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
			resolved, resolveErr := service.ResolvePrimary(ctx, generation, address)
			if resolveErr != nil {
				if ctx.Err() != nil {
					errCh <- ctx.Err()
					return
				}
				resolved.Address = address
				resolved.Code = resolutionCode(resolveErr)
			}
			results[index] = resolved
			errCh <- nil
		}()
	}
	for range addresses {
		if batchErr := <-errCh; batchErr != nil {
			return nil, "", batchErr
		}
	}
	return results, snapshotID, nil
}
