package app

import (
	"fmt"
	"slices"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

// productionComponentKeys is the durable role/feature graph contract used by
// both monolith and split processes. Serve compares it with the components it
// actually registered, so a new runtime component cannot silently diverge
// from the parity tests below.
func productionComponentKeys(cfg config.Config, roles []components.Role, wakeEnabled bool) []string {
	set := make(map[string]struct{})
	add := func(key string) { set[key] = struct{}{} }
	for _, role := range roles {
		add("00-operations-http")
		add("02-durable-metrics")
		if cfg.Observability.OTLPTraceEndpoint != "" {
			add("03-opentelemetry-traces")
		}
		if cfg.Adapters.NATSURL != "" && roleUsesNATSWake(role, cfg) {
			add("04-optional-nats-wake")
		}
		switch role {
		case components.RoleAPI:
			add("08-runtime-event-relay")
			add("09-home-snapshot-feed")
			add("20-public-api")
			if cfg.Features.Verification {
				add("35-compiler-catalog-refresher")
				addWorkerComponentKeys(
					add, "40-contract-verification", cfg.Verification.WorkerCount,
				)
				if cfg.Verification.DerivedEnabled {
					addWorkerComponentKeys(
						add, "41-factory-derived-verification",
						cfg.Verification.DerivedWorkerCount,
					)
				}
				if cfg.Verification.DerivedForwardEnabled {
					addWorkerComponentKeys(
						add, "42-factory-derived-forward",
						cfg.Verification.DerivedWorkerCount,
					)
				}
			}
		case components.RoleSync:
			if wakeEnabled {
				add("05-new-head-wake")
			}
			add("10-core-sync")
			add("12-genesis-state")
			if cfg.Features.Mempool {
				add("15-pending-mempool")
			}
		case components.RoleEnrich:
			add("30-enrichment-outbox")
			addWorkerComponentKeys(add, "35-core-enrichment", cfg.Runtime.WorkerCount)
		case components.RoleTrace:
			if cfg.Features.Trace {
				addWorkerComponentKeys(add, "37-trace-enrichment", cfg.Runtime.WorkerCount)
			} else {
				add("50-role-trace")
			}
		case components.RoleMetadata:
			if cfg.Features.NFTMetadata {
				add("41-nft-metadata-updates")
				add("42-nft-metadata-discovery")
				addWorkerComponentKeys(add, "45-nft-metadata", cfg.Runtime.WorkerCount)
			} else {
				add("50-role-metadata")
			}
		case components.RoleMaintenance:
			add("44-historical-analytics-rollup")
			addWorkerComponentKeys(add, "45-maintenance", cfg.Runtime.WorkerCount)
			add("46-search-catalog-maintenance")
			if cfg.Features.UserAuth {
				add("47-user-auth-cleanup")
			}
			if cfg.Features.APIBilling {
				add("48-x402-billing-expiry")
			}
			if cfg.Features.APIBilling {
				add("49-prepaid-billing-expiry")
			}
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validateProductionComponentGraph(cfg config.Config, roles []components.Role, wakeEnabled bool, registeredKeys []string) error {
	expectedKeys := productionComponentKeys(cfg, roles, wakeEnabled)
	if !slices.Equal(registeredKeys, expectedKeys) {
		return fmt.Errorf("production component graph mismatch: registered=%v expected=%v", registeredKeys, expectedKeys)
	}
	return nil
}
