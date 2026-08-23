package enrich

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

type abiDecodeSummary struct {
	counts        map[DecodeStatus]int
	unbound       int
	diamondRouted int
	bindings      int
}

func (processor *PostgresABIProcessor) decodeObservations(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	observations []abiObservation,
	registry *ABIRegistry,
	baseBindings map[ABIIdentity][]persistedABIBinding,
	persistedBindings map[string]struct{},
	diamondAuxiliaryWarnings map[ABIIdentity]string,
	bindingsCount int,
) (abiDecodeSummary, error) {
	counts := map[DecodeStatus]int{}
	unbound, diamondRouted := 0, 0
	routeCache := make(map[diamondABIRouteKey]diamondABIRoute)
	facetBindingCache := make(map[diamondABIRouteKey]*persistedABIBinding)
	var err error
	for _, observation := range observations {
		if !observation.identityResolved {
			unbound++
			continue
		}
		identity := observation.identity
		activeRegistry := registry
		routeWarning := diamondAuxiliaryWarnings[identity]
		if selector, functionObservation := diamondFunctionObservation(observation); functionObservation {
			key := routeKeyForObservation(observation, selector)
			route, cached := routeCache[key]
			if !cached {
				route, err = resolveDiamondABIRoute(ctx, tx, job, observation, selector)
				if err != nil {
					return abiDecodeSummary{}, err
				}
				routeCache[key] = route
			}
			if route.detected {
				diamondRouted++
				routeWarning = route.warning
				routeRegistry, registryErr := NewABIRegistryWithLimits(processor.limits)
				if registryErr != nil {
					return abiDecodeSummary{}, Permanent(registryErr)
				}
				candidates := []persistedABIBinding{}
				if route.exact && route.facet != (common.Address{}) {
					candidates, registryErr = diamondFunctionCandidates(
						baseBindings[identity], route, selector, processor.limits,
					)
					if registryErr != nil {
						return abiDecodeSummary{}, Permanent(fmt.Errorf("filter target ABI for Diamond route: %w", registryErr))
					}
					if route.facet != observation.target {
						facetCandidate, bindingCached := facetBindingCache[key]
						if !bindingCached {
							candidate, candidateFound, loadErr := loadDiamondFacetABIBinding(
								ctx, tx, identity, route, selector, processor.limits,
							)
							if loadErr != nil {
								return abiDecodeSummary{}, loadErr
							}
							if candidateFound {
								facetCandidate = &candidate
							}
							facetBindingCache[key] = facetCandidate
						}
						if facetCandidate != nil {
							candidates = append(candidates, *facetCandidate)
							bindingKey := abiBindingKey(*facetCandidate)
							if _, alreadyPersisted := persistedBindings[bindingKey]; !alreadyPersisted {
								if err := persistABIBinding(ctx, tx, *facetCandidate); err != nil {
									return abiDecodeSummary{}, err
								}
								persistedBindings[bindingKey] = struct{}{}
								bindingsCount++
							}
						} else if routeWarning == "" {
							routeWarning = "active Diamond facet has no selector-matching verified ABI"
						}
					}
				}
				for _, candidate := range candidates {
					if err := routeRegistry.RegisterJSON(candidate.binding, candidate.abi); err != nil {
						return abiDecodeSummary{}, Permanent(fmt.Errorf("register Diamond-routed ABI binding: %w", err))
					}
				}
				activeRegistry = routeRegistry
			}
		}
		result := decodeABIObservation(activeRegistry, identity, observation)
		appendDecodeWarning(&result, routeWarning)
		if err := persistABIDecoding(ctx, tx, job, identity, observation, result); err != nil {
			return abiDecodeSummary{}, err
		}
		counts[result.result.Status]++
	}
	return abiDecodeSummary{
		counts: counts, unbound: unbound, diamondRouted: diamondRouted, bindings: bindingsCount,
	}, nil
}
