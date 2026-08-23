package app

import (
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/events"
)

func (assembly runtimeAssembly) registerSharedComponents() error {
	cfg := assembly.cfg
	roles := assembly.roles
	roleSet := assembly.roleSet
	logger := assembly.logger
	registry := assembly.registry
	metricCollector := assembly.metricCollector
	telemetry := assembly.telemetry
	runtimeEvents := assembly.runtimeEvents
	broker := assembly.broker
	eventWake := assembly.eventWake
	natsWake := assembly.natsWake
	lifecycle := assembly.lifecycle
	componentRegistry := assembly.componentRegistry
	databaseHealth := assembly.databaseHealth
	for _, role := range roles {
		if err := componentRegistry.Register(role, "00-operations-http", func() (components.Service, error) {
			return &operationalService{
				address: cfg.Server.MetricsAddress, shutdownTimeout: cfg.Server.ShutdownTimeout,
				db: databaseHealth, registry: registry, lifecycle: lifecycle, logger: logger, telemetry: telemetry,
			}, nil
		}); err != nil {
			return err
		}
		if err := componentRegistry.Register(role, "02-durable-metrics", func() (components.Service, error) {
			return metricCollector, nil
		}); err != nil {
			return err
		}
		if telemetry != nil {
			if err := componentRegistry.Register(role, "03-opentelemetry-traces", func() (components.Service, error) {
				return telemetry, nil
			}); err != nil {
				return err
			}
		}
	}
	if natsWake != nil {
		for _, role := range roles {
			if !roleUsesNATSWake(role, cfg) {
				continue
			}

			if err := componentRegistry.Register(role, "04-optional-nats-wake", func() (components.Service, error) {
				return natsWake, nil
			}); err != nil {
				return err
			}
		}
	}
	if roleSet[components.RoleAPI] {
		relay, err := events.NewRelay(runtimeEvents, broker, events.RelayOptions{
			PollInterval: cfg.Runtime.PollInterval, Wake: eventWake, Logger: logger,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleAPI, "08-runtime-event-relay", func() (components.Service, error) {
			return relay, nil
		}); err != nil {
			return err
		}
	}
	return nil
}
