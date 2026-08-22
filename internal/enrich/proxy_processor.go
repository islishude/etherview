package enrich

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/ethrpc"
)

var ProxyStage = StageID{Name: "proxy", Version: 2}

var errProxyDependencyPending = errors.New("proxy stage dependency is not complete")

const (
	proxySourceTransaction          = "transaction_target"
	proxySourceLog                  = "log_target"
	proxySourceTrace                = "trace_target"
	proxySourceStateDiff            = "state_diff_target"
	proxySourceReceipt              = "creation_receipt"
	proxySourceTraceCreate          = "trace_create"
	proxySourceUpgrade              = "upgrade_event"
	proxySourceBeaconReplay         = "exact_beacon_replay"
	proxySourceVerification         = "verification_publication"
	proxySourceGenesis              = "genesis_allocation"
	proxySourceDiamondCut           = "diamond_cut_event"
	proxySourceDelegatecallRouter   = "delegatecall_router"
	proxySourceVerifiedDiamondLoupe = "verified_diamond_loupe"
)

var (
	proxyUpgradedTopic       = SignatureHash("Upgraded(address)")
	proxyBeaconUpgradedTopic = SignatureHash("BeaconUpgraded(address)")
	proxyInitializedTopic    = SignatureHash("Initialized(uint64)")
	proxyDiamondCutTopic     = SignatureHash("DiamondCut((address,uint8,bytes4[])[],address,bytes)")
)

type ProxyLimits struct {
	MaxCandidates   int
	MaxCodeBytes    int
	MaxDetailsBytes int
}

func (limits *ProxyLimits) defaults() {
	if limits.MaxCandidates <= 0 {
		limits.MaxCandidates = 4096
	}
	if limits.MaxCodeBytes <= 0 {
		limits.MaxCodeBytes = 1 << 20
	}
	if limits.MaxDetailsBytes <= 0 {
		limits.MaxDetailsBytes = 4 << 20
	}
}

func (limits ProxyLimits) validate() error {
	if limits.MaxCandidates <= 0 || limits.MaxCandidates > 1_000_000 {
		return errors.New("proxy candidate limit is invalid")
	}
	if limits.MaxCodeBytes <= 0 || limits.MaxCodeBytes > 32<<20 {
		return errors.New("proxy code limit is invalid")
	}
	if limits.MaxDetailsBytes < 128 || limits.MaxDetailsBytes > 8<<20 {
		return errors.New("proxy details limit is invalid")
	}
	return nil
}

type proxyCandidate struct {
	address     common.Address
	force       bool
	knownBeacon bool
	sources     map[string]struct{}
}

func (candidate *proxyCandidate) add(source string, force bool) {
	if candidate.sources == nil {
		candidate.sources = make(map[string]struct{})
	}
	candidate.sources[source] = struct{}{}
	candidate.force = candidate.force || force
}

func (candidate proxyCandidate) sourceList() []string {
	result := make([]string, 0, len(candidate.sources))
	for source := range candidate.sources {
		result = append(result, source)
	}
	slices.Sort(result)
	return result
}

func (candidate proxyCandidate) hasSource(source string) bool {
	_, exists := candidate.sources[source]
	return exists
}

type proxyResolution struct {
	kind               ProxyKind
	pattern            ProxyPattern
	standardVersion    string
	evidenceState      string
	implementation     common.Address
	implementationCode []byte
	implementationHash common.Hash
	beacon             *common.Address
	beaconCode         []byte
	beaconHash         common.Hash
	admin              *common.Address
	adminCode          []byte
	adminHash          common.Hash
	minimalExact       bool
	immutableArgsExact bool
	immutableArgs      []byte
}

type proxyArtifactEvidence struct {
	kind             string
	standardVersion  string
	runtimeImmutable *common.Address
	verificationJob  string
}

type proxyArtifactResolution struct {
	proxyResolution
	proxyArtifactJob          string
	implementationArtifactJob string
	evidence                  map[string]any
}

type proxyDetection struct {
	candidate proxyCandidate
	code      []byte
	codeHash  common.Hash
	proxy     *proxyResolution
	exact     *proxyArtifactResolution
	rejected  string
	v2        ProxyDetectionResolution
	v2Active  bool
}

type beaconDetection struct {
	candidate          proxyCandidate
	code               []byte
	codeHash           common.Hash
	implementation     common.Address
	implementationCode []byte
	implementationHash common.Hash
	rejected           string
}

type proxyUpgradeEvent struct {
	index   uint64
	hash    common.Hash
	emitter common.Address
	kind    string
	target  common.Address
}

type proxyInitializationEvent struct {
	index   uint64
	hash    common.Hash
	address common.Address
	version uint64
}

type proxyBlockEvents struct {
	upgrades        []proxyUpgradeEvent
	initializations []proxyInitializationEvent
	diamondCuts     []diamondCutRecord
	rejected        int
}

// PostgresProxyProcessor discovers block-scoped code and proxy facts. One
// state endpoint is acquired for the whole immutable block and every state
// request uses the same EIP-1898 block-hash selector.
type PostgresProxyProcessor struct {
	db      *sql.DB
	pool    *ethrpc.Pool
	limits  ProxyLimits
	options ProxyDetectionOptions
}

type ProxyDetectionOptions struct {
	Enabled        bool
	SafeEnabled    bool
	DiamondEnabled bool
	Observer       ProxyDetectionObserver
}

type ProxyDetectionObserver interface {
	ObserveProxyDetectionRun(
		duration time.Duration,
		getCodeCalls, storageCalls, callCalls,
		getCodeErrors, storageErrors, callErrors uint64,
		ambiguous bool,
	)
	RecordProxyDetectionResult(detector, family, status, confidence string)
}

func NewPostgresProxyProcessor(db *sql.DB, pool *ethrpc.Pool, limits ProxyLimits) (*PostgresProxyProcessor, error) {
	return NewPostgresProxyProcessorWithOptions(db, pool, limits, ProxyDetectionOptions{})
}

func NewPostgresProxyProcessorWithOptions(
	db *sql.DB,
	pool *ethrpc.Pool,
	limits ProxyLimits,
	options ProxyDetectionOptions,
) (*PostgresProxyProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("proxy processor requires a database and RPC pool")
	}
	limits.defaults()
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if options.SafeEnabled && !options.Enabled {
		return nil, errors.New("safe proxy detector requires proxy detection V2")
	}
	if options.DiamondEnabled && !options.Enabled {
		return nil, errors.New("diamond proxy detector requires proxy detection V2")
	}
	return &PostgresProxyProcessor{db: db, pool: pool, limits: limits, options: options}, nil
}

func (*PostgresProxyProcessor) Stage() StageID { return ProxyStage }

func (processor *PostgresProxyProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresProxyProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil || processor.pool == nil {
		return StageResult{}, errors.New("process proxy stage using unconfigured processor")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != ProxyStage {
		return StageResult{}, Permanent(fmt.Errorf("proxy processor received stage %s", job.Stage))
	}
	candidates, uupsTargets, events, canonical, err := processor.loadCandidates(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return processor.persist(ctx, job, nil, nil, nil, proxyBlockEvents{}, "stale_canonical_skipped")
	}
	if len(candidates) == 0 && len(uupsTargets) == 0 {
		return processor.persist(ctx, job, nil, nil, nil, events, "complete")
	}
	endpoint, err := processor.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return StageResult{}, Unavailable(errors.New("state RPC endpoint is unavailable"))
	}
	detector := rpcProxyDetector{
		caller: endpoint.Client, limits: processor.limits,
		v2Enabled: processor.options.Enabled, safeEnabled: processor.options.SafeEnabled,
		diamondEnabled:            processor.options.DiamondEnabled,
		observer:                  processor.options.Observer,
		codeCache:                 make(map[common.Address][]byte),
		beaconImplementationCache: make(map[common.Address]cachedBeaconImplementation),
		artifact: func(ctx context.Context, address common.Address, hash common.Hash) (proxyArtifactEvidence, bool, error) {
			return processor.loadProxyArtifact(ctx, job, address, hash)
		},
		cloneCreation: func(ctx context.Context, address common.Address, runtime []byte) (bool, error) {
			return processor.authenticateCloneCreation(ctx, job, address, runtime)
		},
	}
	uupsResults, err := probeUUPSReplayTargets(
		ctx, endpoint.Client, job, uupsTargets, processor.limits.MaxCodeBytes,
	)
	if err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		return StageResult{}, err
	}
	for _, result := range uupsResults {
		detector.codeCache[result.target.address] = common.CopyBytes(result.code)
	}
	proxyCandidates := make([]proxyCandidate, 0, len(candidates))
	beaconCandidates := make([]proxyCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.knownBeacon {
			beaconCandidates = append(beaconCandidates, candidate)
		} else {
			proxyCandidates = append(proxyCandidates, candidate)
		}
	}
	detections, err := detector.detectBlock(ctx, job, proxyCandidates)
	if err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		return StageResult{}, err
	}
	for _, detection := range detections {
		if detection.proxy == nil && detection.rejected == "" &&
			detection.candidate.hasSource(proxySourceUpgrade) {
			beaconCandidates = append(beaconCandidates, detection.candidate)
		}
	}
	beacons, err := detector.detectBeaconBlock(ctx, job, beaconCandidates)
	if err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		return StageResult{}, err
	}
	processor.pool.ReportSuccess(endpoint.Name)
	return processor.persist(ctx, job, detections, beacons, uupsResults, events, "complete")
}
