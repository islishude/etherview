package app

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
	"github.com/islishude/etherview/internal/verify"
)

type runtimeAssembly struct {
	backend                   *Backend
	ctx                       context.Context
	cfg                       config.Config
	roles                     []components.Role
	roleSet                   map[components.Role]bool
	db                        *sql.DB
	readDB                    *sql.DB
	logger                    *slog.Logger
	registry                  *observability.Registry
	businessObserver          *observability.BusinessObserver
	tracker                   *syncer.Tracker
	metricCollector           *observability.DurableCollector
	telemetry                 *observability.Telemetry
	rpcBuild                  *RPCBuild
	repository                *store.PostgresRepository
	pendingRepository         *mempool.Postgres
	runtimeEvents             *events.PostgresStore
	redisAccelerator          *accelerator.RedisAccelerator
	broker                    *events.Broker
	eventWake                 chan struct{}
	natsWake                  *accelerator.NATSWake
	outboxWake                <-chan struct{}
	enrichJobWake             <-chan struct{}
	traceJobWake              <-chan struct{}
	signalEvents              func()
	coreRPCSource             *syncer.RPCSource
	coreCanonicalizer         *indexer.Canonicalizer
	verificationRepository    *verify.PostgresRepository
	compilerCatalog           *verify.CompilerCatalog
	verificationReader        httpapi.VerificationReader
	verificationSubmitter     httpapi.VerificationSubmitter
	compatibilityVerification etherscan.VerificationService
	sourcify                  *verify.SourcifyClient
	lifecycle                 *components.Lifecycle
	componentRegistry         *components.Registry
	databaseHealth            databasePinger
	chainID                   string
}

func (assembly runtimeAssembly) registerComponents() error {
	if err := assembly.registerSharedComponents(); err != nil {
		return err
	}

	if err := assembly.registerSyncComponents(); err != nil {
		return err
	}

	if err := assembly.registerAPIComponents(); err != nil {
		return err
	}

	if err := assembly.registerVerificationComponents(); err != nil {
		return err
	}

	if err := assembly.registerEnrichComponents(); err != nil {
		return err
	}

	if err := assembly.registerTraceComponents(); err != nil {
		return err
	}

	if err := assembly.registerMetadataComponents(); err != nil {
		return err
	}

	if err := assembly.registerMaintenanceComponents(); err != nil {
		return err
	}

	if err := assembly.registerIdleRoleComponents(); err != nil {
		return err
	}

	return nil
}
