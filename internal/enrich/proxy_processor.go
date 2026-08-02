package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

var ProxyStage = StageID{Name: "proxy", Version: 2}

var errProxyDependencyPending = errors.New("proxy stage dependency is not complete")

const (
	proxySourceTransaction  = "transaction_target"
	proxySourceLog          = "log_target"
	proxySourceTrace        = "trace_target"
	proxySourceStateDiff    = "state_diff_target"
	proxySourceReceipt      = "creation_receipt"
	proxySourceTraceCreate  = "trace_create"
	proxySourceUpgrade      = "upgrade_event"
	proxySourceBeaconReplay = "exact_beacon_replay"
	proxySourceVerification = "verification_publication"
	proxySourceGenesis      = "genesis_allocation"
)

var (
	proxyUpgradedTopic       = SignatureHash("Upgraded(address)")
	proxyBeaconUpgradedTopic = SignatureHash("BeaconUpgraded(address)")
	proxyInitializedTopic    = SignatureHash("Initialized(uint64)")
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
		limits.MaxDetailsBytes = 2048
	}
}

func (limits ProxyLimits) validate() error {
	if limits.MaxCandidates <= 0 || limits.MaxCandidates > 1_000_000 {
		return errors.New("proxy candidate limit is invalid")
	}
	if limits.MaxCodeBytes <= 0 || limits.MaxCodeBytes > 32<<20 {
		return errors.New("proxy code limit is invalid")
	}
	if limits.MaxDetailsBytes < 128 || limits.MaxDetailsBytes > 64<<10 {
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
	rejected        int
}

// PostgresProxyProcessor discovers block-scoped code and proxy facts. One
// state endpoint is acquired for the whole immutable block and every state
// request uses the same EIP-1898 block-hash selector.
type PostgresProxyProcessor struct {
	db     *sql.DB
	pool   *ethrpc.Pool
	limits ProxyLimits
}

func NewPostgresProxyProcessor(db *sql.DB, pool *ethrpc.Pool, limits ProxyLimits) (*PostgresProxyProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("proxy processor requires a database and RPC pool")
	}
	limits.defaults()
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &PostgresProxyProcessor{db: db, pool: pool, limits: limits}, nil
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

func (processor *PostgresProxyProcessor) loadCandidates(
	ctx context.Context,
	job Job,
) ([]proxyCandidate, []uupsImplementationProbeTarget, proxyBlockEvents, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, proxyCanonicalSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&canonical); err != nil {
		return nil, nil, proxyBlockEvents{}, false, fmt.Errorf("check proxy block canonicality: %w", err)
	}
	if !canonical {
		return nil, nil, proxyBlockEvents{}, false, nil
	}
	candidates := make(map[common.Address]proxyCandidate)
	add := func(address common.Address, source string, force bool) error {
		if address == (common.Address{}) {
			// The zero address is a legal CALL/transaction target. It cannot be a
			// deployable proxy identity, so ignore it without poisoning every other
			// candidate in the block.
			return nil
		}
		candidate := candidates[address]
		candidate.address = address
		candidate.add(source, force)
		candidates[address] = candidate
		if len(candidates) > processor.limits.MaxCandidates {
			return Permanent(errors.New("proxy candidate count exceeds configured limit"))
		}
		return nil
	}
	if err := processor.loadTransactionCandidates(ctx, job, add); err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	if err := processor.loadReceiptCandidates(ctx, job, add); err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	events, err := processor.loadLogCandidates(ctx, job, add)
	if err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	if err := processor.loadTraceCandidates(ctx, job, add); err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	if err := processor.loadStateDiffCandidates(ctx, job, add); err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	if err := processor.loadGenesisCandidates(ctx, job, add); err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	uupsTargets, err := processor.loadReplayCandidates(ctx, job, add)
	if err != nil {
		return nil, nil, proxyBlockEvents{}, false, err
	}
	if len(candidates)+len(uupsTargets) > processor.limits.MaxCandidates {
		return nil, nil, proxyBlockEvents{}, false,
			Permanent(errors.New("proxy candidate count exceeds configured limit"))
	}

	result := make([]proxyCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.knownBeacon = candidate.knownBeacon || candidate.hasSource(proxySourceBeaconReplay)
		if !candidate.force {
			hasHistory, err := processor.hasCanonicalCodeHistory(ctx, job, candidate.address)
			if err != nil {
				return nil, nil, proxyBlockEvents{}, false, err
			}
			if hasHistory {
				knownProxy, knownBeacon, err := processor.proxyOrBeaconHistory(ctx, job, candidate.address)
				if err != nil {
					return nil, nil, proxyBlockEvents{}, false, err
				}
				if !knownProxy && !knownBeacon {
					continue
				}
				candidate.force = true
				candidate.knownBeacon = knownBeacon
			}
		}
		result = append(result, candidate)
	}
	slices.SortFunc(result, func(left, right proxyCandidate) int {
		return bytes.Compare(left.address[:], right.address[:])
	})
	return result, uupsTargets, events, true, nil
}

type proxyCandidateAdder func(common.Address, string, bool) error

func (processor *PostgresProxyProcessor) loadGenesisCandidates(
	ctx context.Context,
	job Job,
	add proxyCandidateAdder,
) error {
	if job.BlockNumber != 0 {
		return nil
	}
	rows, err := processor.db.QueryContext(ctx, `
		SELECT account.address
		FROM genesis_account_observations AS account
		JOIN genesis_state_imports AS imported
		  ON imported.chain_id = account.chain_id
		 AND imported.block_hash = account.block_hash
		 AND imported.state = 'complete'
		WHERE account.chain_id = $1::numeric
		  AND account.block_hash = $2
		  AND octet_length(account.code) > 0
		ORDER BY account.address`,
		job.ChainID, job.BlockHash[:],
	)
	if err != nil {
		return fmt.Errorf("query genesis proxy candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var addressBytes []byte
		if err := rows.Scan(&addressBytes); err != nil {
			return fmt.Errorf("scan genesis proxy candidate: %w", err)
		}
		if len(addressBytes) != common.AddressLength {
			return Permanent(errors.New("stored genesis address is invalid"))
		}
		address := common.BytesToAddress(addressBytes)
		if err := add(address, proxySourceGenesis, true); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate genesis proxy candidates: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadTransactionCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT tx_hash, raw
		FROM transaction_inclusions
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY tx_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return fmt.Errorf("query proxy transaction targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var hashBytes, raw []byte
		if err := rows.Scan(&hashBytes, &raw); err != nil {
			return fmt.Errorf("scan proxy transaction target: %w", err)
		}
		hash, err := WordFromBytes(hashBytes)
		if err != nil {
			return Permanent(errors.New("stored proxy transaction hash is invalid"))
		}
		var wire types.Transaction
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Permanent(errors.New("stored proxy transaction is invalid"))
		}
		if err := validateABITransactionIdentity(&wire, raw, job, hash); err != nil {
			return Permanent(fmt.Errorf("proxy transaction identity: %w", err))
		}
		if address := wire.To(); address != nil {
			if err := add(*address, proxySourceTransaction, false); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy transaction targets: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadReceiptCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT tx_index, tx_hash, raw
		FROM receipts
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY tx_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return fmt.Errorf("query proxy creation receipts: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var index int64
		var hashBytes, raw []byte
		if err := rows.Scan(&index, &hashBytes, &raw); err != nil {
			return fmt.Errorf("scan proxy creation receipt: %w", err)
		}
		if index < 0 {
			return Permanent(errors.New("stored proxy receipt index is invalid"))
		}
		hash, err := WordFromBytes(hashBytes)
		if err != nil {
			return Permanent(errors.New("stored proxy receipt hash is invalid"))
		}
		var wire types.Receipt
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Permanent(errors.New("stored proxy receipt is invalid"))
		}
		if err := validateProxyReceipt(wire, job, uint64(index), hash); err != nil {
			return Permanent(err)
		}
		if wire.ContractAddress != (common.Address{}) {
			if err := add(wire.ContractAddress, proxySourceReceipt, true); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy creation receipts: %w", err)
	}
	return nil
}

func validateProxyReceipt(
	wire types.Receipt,
	job Job,
	index uint64,
	hash common.Hash,
) error {
	if uint64(wire.TransactionIndex) != index || wire.TxHash != hash {
		return errors.New("stored proxy receipt transaction identity mismatch")
	}
	if wire.BlockNumber == nil || !wire.BlockNumber.IsUint64() ||
		wire.BlockNumber.Uint64() != job.BlockNumber ||
		wire.BlockHash != job.BlockHash {
		return errors.New("stored proxy receipt block identity mismatch")
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadLogCandidates(
	ctx context.Context,
	job Job,
	add proxyCandidateAdder,
) (proxyBlockEvents, error) {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT log_index, tx_hash, address, topic0, raw
		FROM logs
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY log_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return proxyBlockEvents{}, fmt.Errorf("query proxy log targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var events proxyBlockEvents
	for rows.Next() {
		var index int64
		var hashBytes, addressBytes, topicBytes, raw []byte
		if err := rows.Scan(&index, &hashBytes, &addressBytes, &topicBytes, &raw); err != nil {
			return proxyBlockEvents{}, fmt.Errorf("scan proxy log target: %w", err)
		}
		if index < 0 || len(addressBytes) != common.AddressLength {
			return proxyBlockEvents{}, Permanent(errors.New("stored proxy log identity is invalid"))
		}
		hash, err := WordFromBytes(hashBytes)
		if err != nil {
			return proxyBlockEvents{}, Permanent(errors.New("stored proxy log transaction hash is invalid"))
		}
		address := common.BytesToAddress(addressBytes)
		var wire types.Log
		if err := json.Unmarshal(raw, &wire); err != nil {
			return proxyBlockEvents{}, Permanent(errors.New("stored proxy log is invalid"))
		}
		if err := validateABILogIdentity(wire, job, uint64(index), hash, address); err != nil {
			return proxyBlockEvents{}, Permanent(fmt.Errorf("proxy log identity: %w", err))
		}
		var topic common.Hash
		if len(topicBytes) != 0 {
			topic, err = WordFromBytes(topicBytes)
			if err != nil || len(wire.Topics) == 0 || wire.Topics[0] != topic {
				return proxyBlockEvents{}, Permanent(errors.New("stored proxy log topic is invalid"))
			}
		} else if len(wire.Topics) != 0 {
			return proxyBlockEvents{}, Permanent(errors.New("stored proxy log topic is missing"))
		}
		if err := add(address, proxySourceLog, false); err != nil {
			return proxyBlockEvents{}, err
		}
		if topic == proxyUpgradedTopic || topic == proxyBeaconUpgradedTopic {
			if err := add(address, proxySourceUpgrade, true); err != nil {
				return proxyBlockEvents{}, err
			}
			target, valid := parseStrictIndexedAddressEvent(wire)
			if !valid {
				events.rejected++
				continue
			}
			kind := "implementation"
			if topic == proxyBeaconUpgradedTopic {
				kind = "beacon"
			}
			events.upgrades = append(events.upgrades, proxyUpgradeEvent{
				index: uint64(index), hash: hash, emitter: address, kind: kind, target: target,
			})
			continue
		}
		if topic == proxyInitializedTopic {
			version, valid := parseStrictInitializedEvent(wire)
			if !valid {
				events.rejected++
				continue
			}
			events.initializations = append(events.initializations, proxyInitializationEvent{
				index: uint64(index), hash: hash, address: address, version: version,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return proxyBlockEvents{}, fmt.Errorf("iterate proxy log targets: %w", err)
	}
	return events, nil
}

func parseStrictIndexedAddressEvent(log types.Log) (common.Address, bool) {
	if len(log.Topics) != 2 || len(log.Data) != 0 {
		return common.Address{}, false
	}
	address, err := AddressFromWord(log.Topics[1])
	return address, err == nil && address != (common.Address{})
}

func parseStrictInitializedEvent(log types.Log) (uint64, bool) {
	if len(log.Topics) != 1 || len(log.Data) != common.HashLength {
		return 0, false
	}
	word, _ := WordFromBytes(log.Data)
	if !word.Big().IsUint64() {
		return 0, false
	}
	return word.Big().Uint64(), true
}

func (processor *PostgresProxyProcessor) loadTraceCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT call_type, to_address, created_address, reverted
		FROM normalized_traces AS trace
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = trace.chain_id
		        AND published.block_hash = trace.block_hash
		        AND published.stage = $4
		        AND published.stage_version = $5
		        AND published.state = 'complete'
		  )
		ORDER BY transaction_index, trace_path`, job.ChainID,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		TraceStage.Name, TraceStage.Version)
	if err != nil {
		return fmt.Errorf("query proxy trace targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var callType string
		var toBytes, createdBytes []byte
		var reverted bool
		if err := rows.Scan(&callType, &toBytes, &createdBytes, &reverted); err != nil {
			return fmt.Errorf("scan proxy trace target: %w", err)
		}
		if len(toBytes) != 0 {
			if len(toBytes) != common.AddressLength {
				return Permanent(errors.New("stored proxy trace target is invalid"))
			}
			address := common.BytesToAddress(toBytes)
			if err := add(address, proxySourceTrace, false); err != nil {
				return err
			}
		}
		if !reverted && (callType == "CREATE" || callType == "CREATE2") && len(createdBytes) != 0 {
			if len(createdBytes) != common.AddressLength {
				return Permanent(errors.New("stored proxy trace creation address is invalid"))
			}
			address := common.BytesToAddress(createdBytes)
			if err := add(address, proxySourceTraceCreate, true); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy trace targets: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadStateDiffCandidates(
	ctx context.Context,
	job Job,
	add proxyCandidateAdder,
) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT DISTINCT address
		FROM transaction_state_changes AS change
		WHERE change.chain_id = $1::numeric
		  AND change.block_number = $2::numeric
		  AND change.block_hash = $3
		  AND change.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = change.chain_id
		        AND published.block_hash = change.block_hash
		        AND published.stage = $7
		        AND published.stage_version = $8
		        AND published.state = 'complete'
		  )
		  AND (
		      change.field_kind = 'code'
		      OR (change.field_kind = 'storage' AND change.storage_key IN ($4, $5, $6))
		  )
		ORDER BY change.address`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		EIP1967ImplementationSlot[:], EIP1967BeaconSlot[:], EIP1967AdminSlot[:],
		StateDiffStage.Name, StateDiffStage.Version,
	)
	if err != nil {
		return fmt.Errorf("query proxy state-difference targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var addressBytes []byte
		if err := rows.Scan(&addressBytes); err != nil {
			return fmt.Errorf("scan proxy state-difference target: %w", err)
		}
		if len(addressBytes) != common.AddressLength {
			return Permanent(errors.New("stored proxy state-difference target is invalid"))
		}
		if err := add(common.BytesToAddress(addressBytes), proxySourceStateDiff, true); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy state-difference targets: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadReplayCandidates(
	ctx context.Context,
	job Job,
	add proxyCandidateAdder,
) ([]uupsImplementationProbeTarget, error) {
	if job.Generation == 0 {
		// Direct processors have no durable lease or generation-fenced replay
		// provenance. Their ordinary block candidates are still loaded above.
		return nil, nil
	}
	rows, err := processor.db.QueryContext(ctx, proxyReplayCandidatesSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], job.Stage.Version, proxySourceVerification,
		job.ID, strconv.FormatUint(job.Generation, 10),
	)
	if err != nil {
		return nil, fmt.Errorf("query exact proxy replay targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	uupsTargets := make([]uupsImplementationProbeTarget, 0)
	for rows.Next() {
		var addressBytes []byte
		var targetKind, source string
		var artifactCodeHash []byte
		var artifactVerificationJob sql.NullString
		if err := rows.Scan(
			&addressBytes, &targetKind, &source,
			&artifactCodeHash, &artifactVerificationJob,
		); err != nil {
			return nil, fmt.Errorf("scan exact proxy replay target: %w", err)
		}
		if len(addressBytes) != common.AddressLength {
			return nil, Permanent(errors.New("stored exact proxy address is invalid"))
		}
		address := common.BytesToAddress(addressBytes)
		switch targetKind {
		case "uups":
			if len(artifactCodeHash) != common.HashLength || !artifactVerificationJob.Valid {
				return nil, Permanent(errors.New("stored UUPS replay target lacks an exact verified artifact"))
			}
			target := uupsImplementationProbeTarget{
				address: address, codeHash: common.BytesToHash(artifactCodeHash),
				verificationJobID: artifactVerificationJob.String,
			}
			if err := target.validate(); err != nil {
				return nil, Permanent(err)
			}
			uupsTargets = append(uupsTargets, target)
			continue
		case "beacon":
			source = proxySourceBeaconReplay
		case "proxy":
		default:
			return nil, Permanent(errors.New("stored exact proxy replay target kind is invalid"))
		}
		if err := add(address, source, true); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact proxy replay targets: %w", err)
	}
	return uupsTargets, nil
}

const proxyReplayCandidatesSQL = `
		SELECT target.address, target.target_kind, $5::text AS source,
		       verified.code_hash, verified.verification_job_id::text
		FROM proxy_replay_targets AS target
		JOIN durable_job_replay_requests AS replay_request
		  ON replay_request.job_id = $6::bigint
		 AND replay_request.source_kind = 'verification-publication'
		 AND target.source_verification_job_id::text = replay_request.source_key
		JOIN durable_jobs AS replay_job
		  ON replay_job.id = replay_request.job_id
		 AND replay_job.chain_id = target.chain_id
		 AND replay_job.kind = 'enrichment'
		 AND replay_job.stage = 'proxy'
		 AND replay_job.stage_version = $4
		 AND replay_job.payload->>'block_hash' = '0x' || encode(target.block_hash, 'hex')
		 AND replay_job.payload->>'block_number' = target.block_number::text
		 AND replay_job.status = 'leased'
		 AND replay_job.claimed_generation = $7::bigint
		 AND replay_job.leased_generation = $7::bigint
		LEFT JOIN verified_contract_proxy_artifacts AS artifact
		  ON target.target_kind = 'uups'
		 AND artifact.verification_job_id = target.source_verification_job_id
		 AND artifact.chain_id = target.chain_id
		 AND artifact.address = target.address
		 AND artifact.artifact_kind = 'uups_implementation'
		 AND artifact.standard_version = '5.6.1'
		 AND artifact.runtime_immutable_address = target.address
		 AND artifact.valid_from_block <= target.block_number
		LEFT JOIN verified_contracts AS verified
		  ON verified.chain_id = artifact.chain_id
		 AND verified.address = artifact.address
		 AND verified.code_hash = artifact.code_hash
		 AND verified.valid_from_block = artifact.valid_from_block
		 AND verified.verification_job_id = artifact.verification_job_id
		 AND verified.request_digest = artifact.request_digest
		 AND (verified.valid_to_block IS NULL OR
		      verified.valid_to_block >= target.block_number)
		WHERE target.chain_id = $1::numeric
		  AND target.block_number = $2::numeric
		  AND target.block_hash = $3
		  AND target.source_kind = 'verification_publication'
		  AND replay_request.requested_generation > replay_job.completed_generation
		  AND replay_request.requested_generation <= $7::bigint
		ORDER BY target.address, target.target_kind, source,
		         verified.verification_job_id`

// probeUUPSReplayTargets keeps the authenticated artifact identity on every
// persisted witness while issuing the fixed-block RPC probe only once for one
// implementation address and code epoch. Multiple verification publications
// for identical runtime code therefore share RPC evidence without conflating
// their append-only artifact identities.
func probeUUPSReplayTargets(
	ctx context.Context,
	caller rpcCaller,
	job Job,
	targets []uupsImplementationProbeTarget,
	maxCodeBytes int,
) ([]uupsImplementationProbeResult, error) {
	type probeKey struct {
		address  common.Address
		codeHash common.Hash
	}
	cache := make(map[probeKey]uupsImplementationProbeResult)
	results := make([]uupsImplementationProbeResult, 0, len(targets))
	for _, target := range targets {
		if err := target.validate(); err != nil {
			return nil, Permanent(err)
		}
		key := probeKey{address: target.address, codeHash: target.codeHash}
		if cached, exists := cache[key]; exists {
			cached.target = target
			results = append(results, cached)
			continue
		}
		result, err := probeUUPSImplementationAtBlock(
			ctx, caller, job, target, maxCodeBytes,
		)
		if err != nil {
			return nil, err
		}
		cache[key] = result
		results = append(results, result)
	}
	return results, nil
}

func (processor *PostgresProxyProcessor) loadProxyArtifact(
	ctx context.Context,
	job Job,
	address common.Address,
	hash common.Hash,
) (proxyArtifactEvidence, bool, error) {
	var artifact proxyArtifactEvidence
	var immutable []byte
	err := processor.db.QueryRowContext(ctx, `
		SELECT artifact.artifact_kind, artifact.standard_version,
		       artifact.runtime_immutable_address,
		       artifact.verification_job_id::text
		FROM verified_contract_proxy_artifacts AS artifact
		JOIN verified_contracts AS verified
		  ON verified.chain_id = artifact.chain_id
		 AND verified.address = artifact.address
		 AND verified.code_hash = artifact.code_hash
		 AND verified.valid_from_block = artifact.valid_from_block
		 AND verified.verification_job_id = artifact.verification_job_id
		 AND verified.request_digest = artifact.request_digest
		WHERE artifact.chain_id = $1::numeric
		  AND artifact.address = $2
		  AND artifact.code_hash = $3
		  AND artifact.valid_from_block <= $4::numeric
		  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)
		ORDER BY artifact.valid_from_block DESC, artifact.verification_job_id
		LIMIT 1`, job.ChainID, address[:], hash[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(
		&artifact.kind, &artifact.standardVersion, &immutable, &artifact.verificationJob,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return proxyArtifactEvidence{}, false, nil
	}
	if err != nil {
		return proxyArtifactEvidence{}, false, fmt.Errorf("query authenticated proxy artifact: %w", err)
	}
	if artifact.standardVersion != OpenZeppelin561Standard || artifact.verificationJob == "" {
		return proxyArtifactEvidence{}, false, Permanent(errors.New("stored proxy artifact identity is invalid"))
	}
	if len(immutable) != 0 {
		if len(immutable) != common.AddressLength {
			return proxyArtifactEvidence{}, false, Permanent(errors.New("stored proxy artifact immutable is invalid"))
		}
		value := common.BytesToAddress(immutable)
		if value == (common.Address{}) {
			return proxyArtifactEvidence{}, false, Permanent(errors.New("stored proxy artifact immutable is zero"))
		}
		artifact.runtimeImmutable = &value
	}
	return artifact, true, nil
}

func (processor *PostgresProxyProcessor) authenticateCloneCreation(
	ctx context.Context,
	job Job,
	address common.Address,
	runtime []byte,
) (bool, error) {
	var input, output []byte
	err := processor.db.QueryRowContext(ctx, `
		SELECT trace.input, trace.output
		FROM normalized_traces AS trace
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = trace.chain_id
		 AND canonical.number = trace.block_number
		 AND canonical.block_hash = trace.block_hash
		WHERE trace.chain_id = $1::numeric
		  AND trace.created_address = $2
		  AND trace.call_type IN ('CREATE', 'CREATE2')
		  AND NOT trace.reverted
		  AND trace.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = trace.chain_id
		        AND published.block_hash = trace.block_hash
		        AND published.stage = $4
		        AND published.stage_version = $5
		        AND published.state = 'complete'
		  )
		  AND trace.block_number <= $3::numeric
		ORDER BY trace.block_number DESC, trace.transaction_index DESC,
		         trace.trace_path DESC
		LIMIT 1`, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10),
		TraceStage.Name, TraceStage.Version).Scan(&input, &output)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query clone creation evidence: %w", err)
	}
	if len(input) > processor.limits.MaxCodeBytes+MaxCloneImmutableArgs+64 ||
		len(output) > processor.limits.MaxCodeBytes {
		return false, Permanent(errors.New("clone creation evidence exceeds configured bounds"))
	}
	return bytes.Equal(output, runtime) && AuthenticateOpenZeppelinImmutableClone(input, runtime), nil
}

func (processor *PostgresProxyProcessor) proxyOrBeaconHistory(
	ctx context.Context,
	job Job,
	address common.Address,
) (bool, bool, error) {
	var proxy, beacon bool
	err := processor.db.QueryRowContext(ctx, `
		SELECT
		    EXISTS (
		        SELECT 1
		        FROM proxy_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
		          AND observation.proxy_address = $2
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		    ),
		    EXISTS (
		        SELECT 1
		        FROM proxy_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
		          AND observation.beacon_address = $2
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		        UNION ALL
		        SELECT 1
		        FROM beacon_implementation_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
		          AND observation.beacon_address = $2
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		    )`, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&proxy, &beacon)
	if err != nil {
		return false, false, fmt.Errorf("query canonical proxy or beacon history: %w", err)
	}
	return proxy, beacon, nil
}

func (processor *PostgresProxyProcessor) hasCanonicalCodeHistory(ctx context.Context, job Job, address common.Address) (bool, error) {
	var exists bool
	if err := processor.db.QueryRowContext(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM contract_code_observations AS code
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = code.chain_id
		     AND canonical.number = code.block_number
		     AND canonical.block_hash = code.block_hash
		    WHERE code.chain_id = $1::numeric AND code.address = $2
		      AND code.block_number <= $3::numeric AND code.canonical
		)`, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&exists); err != nil {
		return false, fmt.Errorf("query canonical code history: %w", err)
	}
	return exists, nil
}

type rpcProxyDetector struct {
	caller                    rpcCaller
	limits                    ProxyLimits
	artifact                  func(context.Context, common.Address, common.Hash) (proxyArtifactEvidence, bool, error)
	cloneCreation             func(context.Context, common.Address, []byte) (bool, error)
	codeCache                 map[common.Address][]byte
	beaconImplementationCache map[common.Address]cachedBeaconImplementation
}

type cachedBeaconImplementation struct {
	address common.Address
	valid   bool
}

func (detector rpcProxyDetector) detectBlock(ctx context.Context, job Job, candidates []proxyCandidate) ([]proxyDetection, error) {
	if detector.caller == nil {
		return nil, errors.New("proxy RPC detector is not configured")
	}
	blockReference := rpc.BlockNumberOrHashWithHash(job.BlockHash, true)
	if detector.codeCache == nil {
		detector.codeCache = make(map[common.Address][]byte)
	}
	if detector.beaconImplementationCache == nil {
		detector.beaconImplementationCache = make(map[common.Address]cachedBeaconImplementation)
	}
	result := make([]proxyDetection, 0, len(candidates))
	for _, candidate := range candidates {
		detection, err := detector.detect(ctx, candidate, blockReference)
		if err != nil {
			return nil, err
		}
		result = append(result, detection)
	}
	return result, nil
}

func (detector rpcProxyDetector) detect(
	ctx context.Context,
	candidate proxyCandidate,
	blockReference rpc.BlockNumberOrHash,
) (proxyDetection, error) {
	code, err := detector.getCode(ctx, candidate.address, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	detection := proxyDetection{candidate: candidate, code: code, codeHash: codeHash(code)}
	if len(code) == 0 {
		return detection, nil
	}
	if minimal, ok := DetectEIP1167(code); ok {
		if minimal.Implementation == (common.Address{}) {
			detection.rejected = "minimal_zero_implementation"
			return detection, nil
		}
		if minimal.ImmutableArgsTooLarge {
			detection.rejected = "immutable_args_too_large"
			return detection, nil
		}
		if minimal.Implementation == candidate.address {
			detection.rejected = "self_implementation"
			return detection, nil
		}
		immutableArgsExact := false
		if len(minimal.TrailingData) != 0 {
			if detector.cloneCreation != nil {
				immutableArgsExact, err = detector.cloneCreation(ctx, candidate.address, code)
				if err != nil {
					return proxyDetection{}, err
				}
			}
			if !immutableArgsExact {
				// A canonical EIP-1167 prefix with trailing bytes is only a
				// candidate until a published CREATE/CREATE2 trace proves that
				// the exact OpenZeppelin initcode returned this runtime. Keep the
				// negative generation so a later Trace publication can promote it
				// without mutating an earlier proxy observation.
				detection.rejected = "immutable_args_creation_unverified"
				return detection, nil
			}
		}
		implementationCode, err := detector.getCode(ctx, minimal.Implementation, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		detection.proxy = &proxyResolution{
			kind: ProxyMinimal1167, pattern: ProxyPatternClone, evidenceState: "exact",
			implementation:     minimal.Implementation,
			implementationCode: implementationCode, implementationHash: codeHash(implementationCode),
			minimalExact: minimal.Exact, immutableArgsExact: immutableArgsExact,
			immutableArgs: common.CopyBytes(minimal.TrailingData),
		}
		return detection, nil
	}
	implementationWord, err := detector.getStorage(ctx, candidate.address, EIP1967ImplementationSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	beaconWord, err := detector.getStorage(ctx, candidate.address, EIP1967BeaconSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	adminWord, err := detector.getStorage(ctx, candidate.address, EIP1967AdminSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	implementationSlot, implementationSlotValid := strictStorageAddress(implementationWord)
	beaconSlot, beaconSlotValid := strictStorageAddress(beaconWord)
	adminSlot, adminSlotValid := strictStorageAddress(adminWord)

	var proxyArtifact proxyArtifactEvidence
	var artifactFound bool
	if detector.artifact != nil {
		proxyArtifact, artifactFound, err = detector.artifact(ctx, candidate.address, detection.codeHash)
		if err != nil {
			return proxyDetection{}, err
		}
	}
	if artifactFound {
		detection.exact, err = detector.resolveAuthenticatedArtifact(
			ctx, candidate, detection.codeHash, proxyArtifact,
			implementationSlot, implementationSlotValid,
			beaconSlot, beaconSlotValid, adminSlot, adminSlotValid,
			blockReference,
		)
		if err != nil {
			return proxyDetection{}, err
		}
	}

	references, slotsErr := ParseEIP1967Storage(implementationWord, beaconWord)
	if slotsErr != nil || len(references) > 1 {
		if detection.exact == nil {
			if slotsErr != nil {
				detection.rejected = "invalid_slot_address"
			} else {
				detection.rejected = "ambiguous_slots"
			}
			return detection, nil
		}
		detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
		return detection, nil
	}
	if len(references) == 0 {
		if detection.exact != nil {
			detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
		}
		return detection, nil
	}
	reference := references[0]
	resolution := &proxyResolution{kind: reference.Kind, pattern: ProxyPatternUnknown, evidenceState: "generic"}
	implementation := reference.Target
	if reference.Kind == ProxyBeacon {
		beaconAddress := reference.Target
		beaconCode, codeErr := detector.getCode(ctx, beaconAddress, blockReference)
		if codeErr != nil {
			return proxyDetection{}, codeErr
		}
		if len(beaconCode) == 0 {
			if detection.exact != nil {
				detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
				return detection, nil
			}
			detection.rejected = "beacon_has_no_code"
			return detection, nil
		}
		var valid bool
		implementation, valid, err = detector.beaconImplementation(ctx, beaconAddress, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		if !valid {
			if detection.exact != nil {
				detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
				return detection, nil
			}
			detection.rejected = "invalid_beacon_implementation"
			return detection, nil
		}
		resolution.evidenceState = "partial"
		resolution.beacon = &beaconAddress
		resolution.beaconCode = beaconCode
		resolution.beaconHash = codeHash(beaconCode)
	}
	if implementation == candidate.address {
		detection.rejected = "self_implementation"
		return detection, nil
	}
	implementationCode, err := detector.getCode(ctx, implementation, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	if len(implementationCode) == 0 {
		if detection.exact != nil {
			detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
			return detection, nil
		}
		detection.rejected = "implementation_has_no_code"
		return detection, nil
	}
	resolution.implementation = implementation
	resolution.implementationCode = implementationCode
	resolution.implementationHash = codeHash(implementationCode)
	if reference.Kind == ProxyEIP1967 && adminSlotValid && adminSlot != (common.Address{}) {
		adminCode, codeErr := detector.getCode(ctx, adminSlot, blockReference)
		if codeErr != nil {
			return proxyDetection{}, codeErr
		}
		resolution.admin = &adminSlot
		resolution.adminCode = adminCode
		resolution.adminHash = codeHash(adminCode)
		resolution.evidenceState = "partial"
	}
	detection.proxy = resolution
	return detection, nil
}

func strictStorageAddress(word common.Hash) (common.Address, bool) {
	if word == (common.Hash{}) {
		return common.Address{}, true
	}
	address, err := AddressFromWord(word)
	return address, err == nil
}

func genericResolutionFromArtifact(
	exact *proxyArtifactResolution,
	adminSlot common.Address,
	adminSlotValid bool,
	beaconSlot common.Address,
	beaconSlotValid bool,
) *proxyResolution {
	_ = adminSlot
	_ = adminSlotValid
	_ = beaconSlot
	_ = beaconSlotValid
	resolution := exact.proxyResolution
	resolution.pattern = ProxyPatternUnknown
	resolution.standardVersion = ""
	resolution.evidenceState = "generic"
	resolution.admin = nil
	resolution.adminCode = nil
	resolution.adminHash = common.Hash{}
	resolution.beacon = nil
	resolution.beaconCode = nil
	resolution.beaconHash = common.Hash{}
	return &resolution
}

func (detector rpcProxyDetector) resolveAuthenticatedArtifact(
	ctx context.Context,
	candidate proxyCandidate,
	proxyCodeHash common.Hash,
	artifact proxyArtifactEvidence,
	implementationSlot common.Address,
	implementationSlotValid bool,
	beaconSlot common.Address,
	beaconSlotValid bool,
	adminSlot common.Address,
	adminSlotValid bool,
	blockReference rpc.BlockNumberOrHash,
) (*proxyArtifactResolution, error) {
	if artifact.standardVersion != OpenZeppelin561Standard {
		return nil, Permanent(errors.New("authenticated proxy artifact has an unsupported version"))
	}
	exact := &proxyArtifactResolution{
		proxyArtifactJob: artifact.verificationJob,
		evidence:         map[string]any{"official_sources": true},
	}
	loadImplementation := func(address common.Address) (bool, error) {
		if address == (common.Address{}) || address == candidate.address {
			return false, nil
		}
		implementationCode, err := detector.getCode(ctx, address, blockReference)
		if err != nil {
			return false, err
		}
		if len(implementationCode) == 0 {
			return false, nil
		}
		exact.implementation = address
		exact.implementationCode = implementationCode
		exact.implementationHash = codeHash(implementationCode)
		return true, nil
	}
	switch artifact.kind {
	case "transparent_proxy":
		if artifact.runtimeImmutable == nil || !implementationSlotValid {
			return nil, nil
		}
		ok, err := loadImplementation(implementationSlot)
		if err != nil || !ok {
			return nil, err
		}
		adminCode, err := detector.getCode(ctx, *artifact.runtimeImmutable, blockReference)
		if err != nil {
			return nil, err
		}
		if len(adminCode) == 0 {
			return nil, nil
		}
		admin := *artifact.runtimeImmutable
		exact.kind, exact.pattern = ProxyEIP1967, ProxyPatternTransparent
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
		exact.admin = &admin
		exact.adminCode, exact.adminHash = adminCode, codeHash(adminCode)
		exact.evidence["admin_authority"] = "runtime_immutable"
		exact.evidence["admin_slot_matches"] = adminSlotValid && adminSlot == admin
	case "beacon_proxy":
		if artifact.runtimeImmutable == nil {
			return nil, nil
		}
		beacon := *artifact.runtimeImmutable
		beaconCode, err := detector.getCode(ctx, beacon, blockReference)
		if err != nil {
			return nil, err
		}
		if len(beaconCode) == 0 {
			return nil, nil
		}
		implementation, valid, err := detector.beaconImplementation(ctx, beacon, blockReference)
		if err != nil || !valid {
			return nil, err
		}
		ok, err := loadImplementation(implementation)
		if err != nil || !ok {
			return nil, err
		}
		exact.kind, exact.pattern = ProxyBeacon, ProxyPatternBeacon
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
		exact.beacon = &beacon
		exact.beaconCode, exact.beaconHash = beaconCode, codeHash(beaconCode)
		exact.evidence["beacon_authority"] = "runtime_immutable"
		exact.evidence["beacon_slot_matches"] = beaconSlotValid && beaconSlot == beacon
	case "erc1967_proxy":
		if !implementationSlotValid {
			return nil, nil
		}
		ok, err := loadImplementation(implementationSlot)
		if err != nil || !ok {
			return nil, err
		}
		exact.kind, exact.pattern = ProxyEIP1967, ProxyPatternERC1967
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
	default:
		return nil, nil
	}
	exact.evidence["proxy_code_hash"] = proxyCodeHash.Hex()
	return exact, nil
}

func (detector rpcProxyDetector) detectBeaconBlock(
	ctx context.Context,
	job Job,
	candidates []proxyCandidate,
) ([]beaconDetection, error) {
	if detector.caller == nil {
		return nil, errors.New("proxy RPC detector is not configured")
	}
	blockReference := rpc.BlockNumberOrHashWithHash(job.BlockHash, true)
	if detector.codeCache == nil {
		detector.codeCache = make(map[common.Address][]byte)
	}
	if detector.beaconImplementationCache == nil {
		detector.beaconImplementationCache = make(map[common.Address]cachedBeaconImplementation)
	}
	result := make([]beaconDetection, 0, len(candidates))
	for _, candidate := range candidates {
		detection, err := detector.detectBeacon(ctx, candidate, blockReference)
		if err != nil {
			return nil, err
		}
		result = append(result, detection)
	}
	return result, nil
}

func (detector rpcProxyDetector) detectBeacon(
	ctx context.Context,
	candidate proxyCandidate,
	blockReference rpc.BlockNumberOrHash,
) (beaconDetection, error) {
	code, err := detector.getCode(ctx, candidate.address, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	detection := beaconDetection{candidate: candidate, code: code, codeHash: codeHash(code)}
	if len(code) == 0 {
		return detection, nil
	}
	implementation, valid, err := detector.beaconImplementation(ctx, candidate.address, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	if !valid {
		detection.rejected = "invalid_beacon_implementation"
		return detection, nil
	}
	if implementation == candidate.address {
		detection.rejected = "self_implementation"
		return detection, nil
	}
	implementationCode, err := detector.getCode(ctx, implementation, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	if len(implementationCode) == 0 {
		detection.rejected = "implementation_has_no_code"
		return detection, nil
	}
	detection.implementation = implementation
	detection.implementationCode = implementationCode
	detection.implementationHash = codeHash(implementationCode)
	return detection, nil
}

func (detector rpcProxyDetector) getCode(
	ctx context.Context,
	address common.Address,
	blockReference rpc.BlockNumberOrHash,
) ([]byte, error) {
	if cached, exists := detector.codeCache[address]; exists {
		return common.CopyBytes(cached), nil
	}
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_getCode", address, blockReference,
	); err != nil {
		return nil, exactStateRPCError(ctx, "eth_getCode", err)
	}
	code := []byte(encoded)
	if len(code) > detector.limits.MaxCodeBytes {
		return nil, Permanent(errors.New("contract bytecode exceeds proxy detection limit"))
	}
	if code == nil {
		// An exact empty-code observation is different from SQL NULL (code bytes
		// deliberately omitted). Keep an allocated zero-length slice so the
		// Keccak(empty) fact can be audited and reused.
		code = make([]byte, 0)
	}
	if detector.codeCache != nil {
		detector.codeCache[address] = common.CopyBytes(code)
	}
	return code, nil
}

func (detector rpcProxyDetector) getStorage(
	ctx context.Context,
	address common.Address,
	slot common.Hash,
	blockReference rpc.BlockNumberOrHash,
) (common.Hash, error) {
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_getStorageAt", address, slot, blockReference,
	); err != nil {
		return common.Hash{}, exactStateRPCError(ctx, "eth_getStorageAt", err)
	}
	value := []byte(encoded)
	if len(value) != common.HashLength {
		return common.Hash{}, Permanent(errors.New("eth_getStorageAt returned a non-word value"))
	}
	return WordFromBytes(value)
}

func (detector rpcProxyDetector) beaconImplementation(
	ctx context.Context,
	beacon common.Address,
	blockReference rpc.BlockNumberOrHash,
) (common.Address, bool, error) {
	if cached, exists := detector.beaconImplementationCache[beacon]; exists {
		return cached.address, cached.valid, nil
	}
	input, err := packStateProbe("implementation")
	if err != nil {
		return common.Address{}, false, Permanent(err)
	}
	request := map[string]any{"to": beacon, "data": hexutil.Bytes(input)}
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_call", request, blockReference,
	); err != nil {
		if executionReverted(err) {
			if detector.beaconImplementationCache != nil {
				detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{}
			}
			return common.Address{}, false, nil
		}
		return common.Address{}, false, exactStateRPCError(ctx, "eth_call", err)
	}
	value := []byte(encoded)
	implementation, err := ParseBeaconImplementation(value)
	if err != nil {
		if detector.beaconImplementationCache != nil {
			detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{}
		}
		return common.Address{}, false, nil
	}
	if detector.beaconImplementationCache != nil {
		detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{
			address: implementation, valid: true,
		}
	}
	return implementation, true, nil
}

func (processor *PostgresProxyProcessor) persist(
	ctx context.Context,
	job Job,
	detections []proxyDetection,
	beacons []beaconDetection,
	uupsProbes []uupsImplementationProbeResult,
	events proxyBlockEvents,
	outcome string,
) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistTx(ctx, tx, job, detections, beacons, uupsProbes, events, outcome)
	})
}

type proxyCarryForwardCounts struct {
	proxies          int64
	beacons          int64
	uups             int64
	resolutions      int64
	negativeEvidence int64
}

func (processor *PostgresProxyProcessor) persistTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	detections []proxyDetection,
	beacons []beaconDetection,
	uupsProbes []uupsImplementationProbeResult,
	events proxyBlockEvents,
	outcome string,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		outcome = "stale_canonical_skipped"
		detections = nil
		beacons = nil
		uupsProbes = nil
		events = proxyBlockEvents{}
	}
	codeObservations := make(map[common.Address]proxyCodeObservation)
	beaconObservations := make(map[common.Address]proxyBeaconObservation)
	proxyCount := 0
	rejectedCount := 0
	uupsCompatibleCount := 0
	uupsRejectedCount := 0
	for _, result := range uupsProbes {
		if err := mergeProxyCodeObservation(
			codeObservations, result.target.address, result.target.codeHash, result.code,
		); err != nil {
			return StageResult{}, Permanent(err)
		}
		if result.compatible() {
			uupsCompatibleCount++
		} else {
			uupsRejectedCount++
		}
	}
	for _, detection := range detections {
		if err := mergeProxyCodeObservation(codeObservations, detection.candidate.address, detection.codeHash, detection.code); err != nil {
			return StageResult{}, Permanent(err)
		}
		if detection.proxy == nil {
			if detection.rejected != "" {
				rejectedCount++
			}
			continue
		}
		proxyCount++
		if detection.exact != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, detection.exact.implementation,
				detection.exact.implementationHash, detection.exact.implementationCode,
			); err != nil {
				return StageResult{}, Permanent(err)
			}
			if detection.exact.admin != nil {
				if err := mergeProxyCodeObservation(
					codeObservations, *detection.exact.admin,
					detection.exact.adminHash, detection.exact.adminCode,
				); err != nil {
					return StageResult{}, Permanent(err)
				}
			}
			if detection.exact.beacon != nil {
				if err := mergeProxyCodeObservation(
					codeObservations, *detection.exact.beacon,
					detection.exact.beaconHash, detection.exact.beaconCode,
				); err != nil {
					return StageResult{}, Permanent(err)
				}
				if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
					address: *detection.exact.beacon, codeHash: detection.exact.beaconHash,
					implementation:     detection.exact.implementation,
					implementationHash: detection.exact.implementationHash,
					sources:            map[string]struct{}{"runtime_immutable": {}},
				}); err != nil {
					return StageResult{}, Permanent(err)
				}
			}
		}
		if err := mergeProxyCodeObservation(
			codeObservations, detection.proxy.implementation,
			detection.proxy.implementationHash, detection.proxy.implementationCode,
		); err != nil {
			return StageResult{}, Permanent(err)
		}
		if detection.proxy.admin != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, *detection.proxy.admin,
				detection.proxy.adminHash, detection.proxy.adminCode,
			); err != nil {
				return StageResult{}, Permanent(err)
			}
		}
		if detection.proxy.beacon != nil {
			if err := mergeProxyCodeObservation(
				codeObservations, *detection.proxy.beacon,
				detection.proxy.beaconHash, detection.proxy.beaconCode,
			); err != nil {
				return StageResult{}, Permanent(err)
			}
			if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
				address: *detection.proxy.beacon, codeHash: detection.proxy.beaconHash,
				implementation:     detection.proxy.implementation,
				implementationHash: detection.proxy.implementationHash,
				sources:            map[string]struct{}{"proxy_slot": {}},
			}); err != nil {
				return StageResult{}, Permanent(err)
			}
		}
	}
	for _, detection := range beacons {
		if err := mergeProxyCodeObservation(
			codeObservations, detection.candidate.address, detection.codeHash, detection.code,
		); err != nil {
			return StageResult{}, Permanent(err)
		}
		if detection.implementation == (common.Address{}) {
			if detection.rejected != "" {
				rejectedCount++
			}
			continue
		}
		if err := mergeProxyCodeObservation(
			codeObservations, detection.implementation,
			detection.implementationHash, detection.implementationCode,
		); err != nil {
			return StageResult{}, Permanent(err)
		}
		if err := mergeProxyBeaconObservation(beaconObservations, proxyBeaconObservation{
			address: detection.candidate.address, codeHash: detection.codeHash,
			implementation:     detection.implementation,
			implementationHash: detection.implementationHash,
			sources:            map[string]struct{}{"standalone_probe": {}},
		}); err != nil {
			return StageResult{}, Permanent(err)
		}
	}
	addresses := make([]common.Address, 0, len(codeObservations))
	for address := range codeObservations {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(left, right common.Address) int { return bytes.Compare(left[:], right[:]) })
	for _, address := range addresses {
		if err := persistProxyCodeObservation(ctx, tx, job, codeObservations[address]); err != nil {
			return StageResult{}, err
		}
	}
	for _, result := range uupsProbes {
		if err := persistUUPSImplementationProbe(ctx, tx, job, result); err != nil {
			return StageResult{}, err
		}
	}
	for _, detection := range detections {
		if detection.proxy == nil {
			if err := processor.persistProxyDetectionEvidence(
				ctx, tx, job, detection.candidate, detection.codeHash,
				"proxy", detection.rejected, len(detection.code) == 0,
			); err != nil {
				return StageResult{}, err
			}
		}
	}
	for _, detection := range beacons {
		if detection.implementation == (common.Address{}) {
			if err := processor.persistProxyDetectionEvidence(
				ctx, tx, job, detection.candidate, detection.codeHash,
				"beacon", detection.rejected, len(detection.code) == 0,
			); err != nil {
				return StageResult{}, err
			}
		}
	}
	for _, detection := range detections {
		if detection.proxy != nil {
			if err := processor.persistProxyObservation(ctx, tx, job, detection); err != nil {
				return StageResult{}, err
			}
			if err := persistProxyObservationGeneration(ctx, tx, job, detection.candidate.address); err != nil {
				return StageResult{}, err
			}
			if detection.exact != nil {
				if err := processor.persistProxyArtifactResolution(ctx, tx, job, detection); err != nil {
					return StageResult{}, err
				}
			}
		}
	}
	beaconAddresses := make([]common.Address, 0, len(beaconObservations))
	for address := range beaconObservations {
		beaconAddresses = append(beaconAddresses, address)
	}
	slices.SortFunc(beaconAddresses, func(left, right common.Address) int {
		return bytes.Compare(left[:], right[:])
	})
	for _, address := range beaconAddresses {
		if err := processor.persistBeaconObservation(ctx, tx, job, beaconObservations[address]); err != nil {
			return StageResult{}, err
		}
		if err := persistBeaconObservationGeneration(ctx, tx, job, address); err != nil {
			return StageResult{}, err
		}
	}
	for _, event := range events.upgrades {
		if err := persistProxyUpgradeEvent(ctx, tx, job, event); err != nil {
			return StageResult{}, err
		}
	}
	for _, event := range events.initializations {
		if err := persistProxyInitializationEvent(ctx, tx, job, event); err != nil {
			return StageResult{}, err
		}
	}
	carried := proxyCarryForwardCounts{}
	if canonical && job.Generation > 1 {
		carried, err = carryForwardProxyGeneration(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
	}
	// The first proxy generation is the ABI claim dependency itself. Publishing
	// it unlocks the already queued ABI generation, so requesting another
	// generation here would make durable history depend on whether a late trace
	// replay superseded this transaction. Only later proxy generations carry
	// facts that can be newer than ABI's initial view.
	abiRequeued := false
	if job.Generation > 1 {
		abiRequeued, err = resetTerminalDependentStageTx(ctx, tx, job, ABIStage)
		if err != nil {
			return StageResult{}, err
		}
	}
	coverageDetails, err := loadProxyCoverageDetails(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	details := map[string]string{
		"outcome": outcome, "candidates": strconv.Itoa(len(detections) + len(beacons) + len(uupsProbes)),
		"code_observations": strconv.Itoa(len(codeObservations)), "proxies": strconv.Itoa(proxyCount),
		"beacons":               strconv.Itoa(len(beaconObservations)),
		"uups_probes":           strconv.Itoa(len(uupsProbes)),
		"uups_compatible":       strconv.Itoa(uupsCompatibleCount),
		"uups_rejected":         strconv.Itoa(uupsRejectedCount),
		"upgrade_events":        strconv.Itoa(len(events.upgrades)),
		"initialization_events": strconv.Itoa(len(events.initializations)),
		"rejected_events":       strconv.Itoa(events.rejected),
		"rejected_candidates":   strconv.Itoa(rejectedCount),
		"carried_proxies":       strconv.FormatInt(carried.proxies, 10),
		"carried_beacons":       strconv.FormatInt(carried.beacons, 10),
		"carried_uups":          strconv.FormatInt(carried.uups, 10),
		"carried_resolutions":   strconv.FormatInt(carried.resolutions, 10),
		"carried_negative_evidence": strconv.FormatInt(
			carried.negativeEvidence, 10,
		),
		"abi_requeued": strconv.FormatBool(abiRequeued),
	}
	maps.Copy(details, coverageDetails)
	return StageResult{State: ResultComplete, Details: details}, nil
}

func carryForwardProxyGeneration(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
) (proxyCarryForwardCounts, error) {
	if job.Generation <= 1 {
		return proxyCarryForwardCounts{}, nil
	}
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return proxyCarryForwardCounts{}, Permanent(err)
	}
	var carried proxyCarryForwardCounts
	err = tx.QueryRowContext(ctx, carryForwardProxyGenerationSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		job.Stage.Version, jobID, generation,
	).Scan(
		&carried.proxies, &carried.beacons, &carried.uups, &carried.resolutions,
		&carried.negativeEvidence,
	)
	if err != nil {
		return proxyCarryForwardCounts{}, fmt.Errorf("carry forward proxy generation evidence: %w", err)
	}
	return carried, nil
}

func loadProxyCoverageDetails(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
) (map[string]string, error) {
	details := map[string]string{
		"history_coverage":    "event_only",
		"trace_coverage":      "missing",
		"state_diff_coverage": "missing",
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT stage, stage_version, state, durable_job_id, job_generation
		FROM published_block_stage_results
		WHERE chain_id = $1::numeric
		  AND block_hash = $2
		  AND ((stage = $3 AND stage_version = $4) OR
		       (stage = $5 AND stage_version = $6))
		ORDER BY stage, stage_version`,
		job.ChainID, job.BlockHash[:], TraceStage.Name, TraceStage.Version,
		StateDiffStage.Name, StateDiffStage.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("query proxy coverage witnesses: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var stage, state string
		var version int
		var durableJobID, generation sql.NullInt64
		if err := rows.Scan(&stage, &version, &state, &durableJobID, &generation); err != nil {
			return nil, fmt.Errorf("scan proxy coverage witness: %w", err)
		}
		key := stage + "_coverage"
		details[key] = state
		if state == "complete" && durableJobID.Valid && durableJobID.Int64 > 0 &&
			generation.Valid && generation.Int64 > 0 {
			details[stage+"_job_id"] = strconv.FormatInt(durableJobID.Int64, 10)
			details[stage+"_job_generation"] = strconv.FormatInt(generation.Int64, 10)
		} else if state == "complete" {
			details[key] = "unfenced"
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy coverage witnesses: %w", err)
	}
	if details["trace_coverage"] == "complete" &&
		details["state_diff_coverage"] == "complete" {
		details["history_coverage"] = "complete"
	}
	return details, nil
}

type proxyCodeObservation struct {
	address  common.Address
	codeHash common.Hash
	code     []byte
}

func (processor *PostgresProxyProcessor) persistProxyDetectionEvidence(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	candidate proxyCandidate,
	codeHash common.Hash,
	candidateKind string,
	reason string,
	emptyCode bool,
) error {
	state := "rejected"
	if reason == "" {
		state = "not_detected"
		reason = "not_proxy"
		if emptyCode {
			reason = "empty_code"
		}
	}
	details, err := json.Marshal(map[string]any{"discovery_sources": candidate.sourceList()})
	if err != nil {
		return fmt.Errorf("encode proxy detection evidence: %w", err)
	}
	if len(details) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("proxy detection evidence exceeds configured limit"))
	}
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	result, err := tx.ExecContext(ctx, upsertProxyDetectionEvidenceSQL,
		job.ChainID, candidate.address[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], job.Stage.Version, codeHash[:], candidateKind, state, reason,
		jobID, generation, string(details),
	)
	if err != nil {
		return fmt.Errorf("persist proxy detection evidence: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read proxy detection evidence result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing proxy detection evidence conflicts with RPC state"))
	}
	return nil
}

type proxyBeaconObservation struct {
	address            common.Address
	codeHash           common.Hash
	implementation     common.Address
	implementationHash common.Hash
	sources            map[string]struct{}
}

func mergeProxyBeaconObservation(
	observations map[common.Address]proxyBeaconObservation,
	observation proxyBeaconObservation,
) error {
	if observation.address == (common.Address{}) || observation.codeHash == (common.Hash{}) ||
		observation.implementation == (common.Address{}) ||
		observation.implementationHash == (common.Hash{}) {
		return errors.New("beacon implementation observation identity is invalid")
	}
	if existing, exists := observations[observation.address]; exists {
		if existing.codeHash != observation.codeHash ||
			existing.implementation != observation.implementation ||
			existing.implementationHash != observation.implementationHash {
			return errors.New("one block produced conflicting beacon implementation observations")
		}
		for source := range observation.sources {
			existing.sources[source] = struct{}{}
		}
		observations[observation.address] = existing
		return nil
	}
	clonedSources := make(map[string]struct{}, len(observation.sources))
	for source := range observation.sources {
		clonedSources[source] = struct{}{}
	}
	observation.sources = clonedSources
	observations[observation.address] = observation
	return nil
}

func mergeProxyCodeObservation(observations map[common.Address]proxyCodeObservation, address common.Address, hash common.Hash, code []byte) error {
	if address == (common.Address{}) || hash == (common.Hash{}) {
		return errors.New("proxy code observation identity is invalid")
	}
	if existing, exists := observations[address]; exists {
		if existing.codeHash != hash || !bytes.Equal(existing.code, code) {
			return errors.New("one block produced conflicting code observations")
		}
		return nil
	}
	cloned := make([]byte, len(code))
	copy(cloned, code)
	observations[address] = proxyCodeObservation{address: address, codeHash: hash, code: cloned}
	return nil
}

func persistProxyCodeObservation(ctx context.Context, tx *sql.Tx, job Job, observation proxyCodeObservation) error {
	result, err := tx.ExecContext(ctx, upsertProxyCodeObservationSQL,
		job.ChainID, observation.address[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], observation.codeHash[:], observation.code,
	)
	if err != nil {
		return fmt.Errorf("persist exact contract code observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read exact contract code observation result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing exact contract code observation conflicts with RPC state"))
	}
	return nil
}

func (processor *PostgresProxyProcessor) persistProxyObservation(ctx context.Context, tx *sql.Tx, job Job, detection proxyDetection) error {
	resolved := detection.proxy
	details := map[string]any{"discovery_sources": detection.candidate.sourceList()}
	if resolved.kind == ProxyMinimal1167 {
		runtimeKind := "immutable_args_candidate"
		if resolved.minimalExact {
			runtimeKind = "canonical"
		} else if resolved.immutableArgsExact {
			runtimeKind = "openzeppelin_immutable_args"
		}
		details["minimal_runtime"] = runtimeKind
		details["immutable_args_bytes"] = len(resolved.immutableArgs)
		details["immutable_args_creation_authenticated"] = resolved.immutableArgsExact
	}
	if resolved.admin != nil {
		// The ERC-1967 admin slot is compatibility evidence only. In particular,
		// it is not the immutable _admin authority of an OZ 5.x transparent proxy.
		details["admin_evidence"] = "erc1967_compatibility_slot"
	}
	if resolved.beacon != nil {
		// OZ 5.x BeaconProxy resolves its immutable beacon independently from the
		// compatibility slot. Verification must bind that immutable before writes.
		details["beacon_evidence"] = "erc1967_compatibility_slot"
	}
	if resolved.pattern == ProxyPatternUUPS {
		details["uups_evidence"] = "fixed_block_proxiable_uuid"
		if resolved.standardVersion != "" {
			details["upgrade_interface_version"] = "5.0.0"
		}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode proxy observation details: %w", err)
	}
	if len(encoded) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("proxy observation details exceed configured limit"))
	}
	var admin, adminHash, beacon, beaconHash, immutableArgs, standardVersion any
	if resolved.admin != nil {
		admin = resolved.admin[:]
		adminHash = resolved.adminHash[:]
	}
	if resolved.beacon != nil {
		beacon = resolved.beacon[:]
		beaconHash = resolved.beaconHash[:]
	}
	if len(resolved.immutableArgs) != 0 {
		immutableArgs = resolved.immutableArgs
	}
	if resolved.standardVersion != "" {
		standardVersion = resolved.standardVersion
	}
	result, err := tx.ExecContext(ctx, upsertProxyObservationSQL,
		job.ChainID, detection.candidate.address[:], strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		job.Stage.Version, detection.codeHash[:], resolved.kind, resolved.pattern, standardVersion,
		resolved.implementation[:], admin, adminHash, beacon, beaconHash, immutableArgs,
		resolved.implementationHash[:], ConfidenceHigh, resolved.evidenceState, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("persist exact proxy observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read exact proxy observation result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing exact proxy observation conflicts with RPC state"))
	}
	return nil
}

func proxyGenerationSQLIdentity(job Job) (any, any, error) {
	if job.Generation == 0 {
		return nil, nil, nil
	}
	jobID, err := strconv.ParseInt(job.ID, 10, 64)
	if err != nil || jobID <= 0 || strconv.FormatInt(jobID, 10) != job.ID {
		return nil, nil, errors.New("proxy generation job ID is not a canonical positive BIGINT")
	}
	return jobID, strconv.FormatUint(job.Generation, 10), nil
}

func persistProxyObservationGeneration(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	address common.Address,
) error {
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	result, err := tx.ExecContext(ctx, insertProxyObservationGenerationSQL,
		job.ChainID, address[:], job.BlockHash[:], job.Stage.Version, jobID, generation,
	)
	if err != nil {
		return fmt.Errorf("persist proxy observation generation: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected > 1 {
		if rowsErr != nil {
			return fmt.Errorf("read proxy observation generation result: %w", rowsErr)
		}
		return Permanent(errors.New("proxy observation generation affected multiple rows"))
	}
	return nil
}

func (processor *PostgresProxyProcessor) persistProxyArtifactResolution(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	detection proxyDetection,
) error {
	exact := detection.exact
	if exact == nil {
		return nil
	}
	encoded, err := json.Marshal(exact.evidence)
	if err != nil {
		return fmt.Errorf("encode proxy artifact resolution evidence: %w", err)
	}
	if len(encoded) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("proxy artifact resolution evidence exceeds configured limit"))
	}
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	var admin, adminHash, beacon, beaconHash, implementationArtifact any
	if exact.admin != nil {
		admin, adminHash = exact.admin[:], exact.adminHash[:]
	}
	if exact.beacon != nil {
		beacon, beaconHash = exact.beacon[:], exact.beaconHash[:]
	}
	if exact.implementationArtifactJob != "" {
		implementationArtifact = exact.implementationArtifactJob
	}
	var resolutionID int64
	err = tx.QueryRowContext(ctx, insertProxyArtifactResolutionSQL,
		job.ChainID, detection.candidate.address[:], job.BlockHash[:], job.Stage.Version,
		detection.codeHash[:], exact.kind, exact.pattern, exact.standardVersion,
		exact.implementation[:], exact.implementationHash[:], admin, adminHash,
		beacon, beaconHash, exact.proxyArtifactJob, implementationArtifact,
		jobID, generation, string(encoded),
	).Scan(&resolutionID)
	if err != nil {
		return fmt.Errorf("persist authenticated proxy artifact resolution: %w", err)
	}
	if resolutionID <= 0 {
		return Permanent(errors.New("proxy artifact resolution returned an invalid identity"))
	}
	return nil
}

func (processor *PostgresProxyProcessor) persistBeaconObservation(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	observation proxyBeaconObservation,
) error {
	sources := make([]string, 0, len(observation.sources))
	for source := range observation.sources {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	details, err := json.Marshal(map[string]any{"detection_sources": sources})
	if err != nil {
		return fmt.Errorf("encode beacon implementation observation details: %w", err)
	}
	if len(details) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("beacon implementation observation details exceed configured limit"))
	}
	result, err := tx.ExecContext(ctx, upsertBeaconImplementationObservationSQL,
		job.ChainID, observation.address[:], strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		observation.codeHash[:], observation.implementation[:], observation.implementationHash[:],
		job.Stage.Version, ConfidenceHigh, string(details),
	)
	if err != nil {
		return fmt.Errorf("persist exact beacon implementation observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read exact beacon implementation observation result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing exact beacon implementation observation conflicts with RPC state"))
	}
	return nil
}

func persistBeaconObservationGeneration(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	address common.Address,
) error {
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	result, err := tx.ExecContext(ctx, insertBeaconObservationGenerationSQL,
		job.ChainID, address[:], job.BlockHash[:], job.Stage.Version, jobID, generation,
	)
	if err != nil {
		return fmt.Errorf("persist beacon observation generation: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected > 1 {
		if rowsErr != nil {
			return fmt.Errorf("read beacon observation generation result: %w", rowsErr)
		}
		return Permanent(errors.New("beacon observation generation affected multiple rows"))
	}
	return nil
}

func persistProxyUpgradeEvent(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	event proxyUpgradeEvent,
) error {
	result, err := tx.ExecContext(ctx, upsertProxyUpgradeEventSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		strconv.FormatUint(event.index, 10), event.hash[:], event.emitter[:], event.kind,
		event.target[:], job.Stage.Version,
	)
	if err != nil {
		return fmt.Errorf("persist strict proxy upgrade event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read strict proxy upgrade event result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing strict proxy upgrade event conflicts with indexed log"))
	}
	return nil
}

func persistProxyInitializationEvent(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	event proxyInitializationEvent,
) error {
	result, err := tx.ExecContext(ctx, upsertProxyInitializationEventSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		strconv.FormatUint(event.index, 10), event.hash[:], event.address[:],
		strconv.FormatUint(event.version, 10), job.Stage.Version,
	)
	if err != nil {
		return fmt.Errorf("persist strict proxy initialization event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read strict proxy initialization event result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing strict proxy initialization event conflicts with indexed log"))
	}
	return nil
}

// resetTerminalDependentStageTx is the safe-replay half of stage dependency.
// Despite the historical name, it now records a durable source generation for
// queued, leased, or terminal work. A leased target retains ownership and its
// completion transaction consumes the pending replay before it can become
// terminal; unowned output is cleared immediately.
func resetTerminalDependentStageTx(ctx context.Context, tx *sql.Tx, job Job, dependent StageID) (bool, error) {
	requested, err := requestDependentStageReplayTx(ctx, tx, job, dependent)
	if err != nil {
		return false, fmt.Errorf("request dependent stage replay %s: %w", dependent, err)
	}
	return requested, nil
}

const proxyCanonicalSQL = `
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
)`

const upsertProxyCodeObservationSQL = `
INSERT INTO contract_code_observations AS current (
    chain_id, address, block_number, block_hash, code_hash, code, canonical
) VALUES ($1::numeric, $2, $3::numeric, $4, $5, $6, TRUE)
ON CONFLICT (chain_id, address, block_hash) DO UPDATE SET
    code = COALESCE(current.code, EXCLUDED.code),
    canonical = EXCLUDED.canonical
WHERE current.code_hash = EXCLUDED.code_hash
  AND (current.code IS NULL OR current.code = EXCLUDED.code)`

const upsertProxyObservationSQL = `
INSERT INTO proxy_observations AS current (
    chain_id, proxy_address, block_number, block_hash, stage_version,
    proxy_code_hash, proxy_kind, proxy_pattern, standard_version,
    implementation_address, admin_address, admin_code_hash,
    beacon_address, beacon_code_hash, immutable_args,
    implementation_code_hash, confidence, evidence_state, canonical, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5,
    $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, TRUE, $19::jsonb
)
ON CONFLICT (chain_id, proxy_address, block_hash, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.proxy_code_hash = EXCLUDED.proxy_code_hash
  AND current.proxy_kind = EXCLUDED.proxy_kind
  AND current.proxy_pattern = EXCLUDED.proxy_pattern
  AND current.standard_version IS NOT DISTINCT FROM EXCLUDED.standard_version
  AND current.implementation_address IS NOT DISTINCT FROM EXCLUDED.implementation_address
  AND current.admin_address IS NOT DISTINCT FROM EXCLUDED.admin_address
  AND current.admin_code_hash IS NOT DISTINCT FROM EXCLUDED.admin_code_hash
  AND current.beacon_address IS NOT DISTINCT FROM EXCLUDED.beacon_address
  AND current.beacon_code_hash IS NOT DISTINCT FROM EXCLUDED.beacon_code_hash
  AND current.immutable_args IS NOT DISTINCT FROM EXCLUDED.immutable_args
  AND current.implementation_code_hash IS NOT DISTINCT FROM EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence
  AND current.evidence_state = EXCLUDED.evidence_state`

const insertProxyObservationGenerationSQL = `
INSERT INTO proxy_observation_generations (
    chain_id, proxy_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES ($1::numeric, $2, $3, $4, $5::bigint, $6::bigint)
ON CONFLICT DO NOTHING`

const upsertProxyDetectionEvidenceSQL = `
INSERT INTO proxy_detection_evidence AS current (
    chain_id, address, block_number, block_hash, stage_version, code_hash,
    candidate_kind, detection_state, reason, canonical,
    durable_job_id, job_generation, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5, $6,
    $7, $8, $9, TRUE, $10::bigint, $11::bigint, $12::jsonb
)
ON CONFLICT (
    chain_id, address, block_hash, stage_version, candidate_kind,
    durable_job_id, job_generation
) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.code_hash = EXCLUDED.code_hash
  AND current.detection_state = EXCLUDED.detection_state
  AND current.reason = EXCLUDED.reason`

const insertProxyArtifactResolutionSQL = `
WITH inserted AS (
    INSERT INTO proxy_artifact_resolutions (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, proxy_code_hash, proxy_kind,
        proxy_pattern, standard_version, implementation_address,
        implementation_code_hash, admin_address, admin_code_hash,
        beacon_address, beacon_code_hash, proxy_artifact_job_id,
        implementation_artifact_job_id, durable_job_id, job_generation,
        evidence
    ) VALUES (
        $1::numeric, $2, $3, $4, $5, $6,
        $7, $8, $9, $10, $11, $12,
        $13, $14, $15::uuid, $16::uuid, $17::bigint, $18::bigint,
        $19::jsonb
    )
    ON CONFLICT DO NOTHING
    RETURNING id
)
SELECT id FROM inserted
UNION ALL
SELECT existing.id
FROM proxy_artifact_resolutions AS existing
WHERE existing.chain_id = $1::numeric
  AND existing.proxy_address = $2
  AND existing.observation_block_hash = $3
  AND existing.observation_stage_version = $4
  AND existing.durable_job_id IS NOT DISTINCT FROM $17::bigint
  AND existing.job_generation IS NOT DISTINCT FROM $18::bigint
  AND existing.proxy_code_hash = $5
  AND existing.proxy_kind = $6
  AND existing.proxy_pattern = $7
  AND existing.standard_version = $8
  AND existing.implementation_address = $9
  AND existing.implementation_code_hash = $10
  AND existing.admin_address IS NOT DISTINCT FROM $11::bytea
  AND existing.admin_code_hash IS NOT DISTINCT FROM $12::bytea
  AND existing.beacon_address IS NOT DISTINCT FROM $13::bytea
  AND existing.beacon_code_hash IS NOT DISTINCT FROM $14::bytea
  AND existing.proxy_artifact_job_id = $15::uuid
  AND existing.implementation_artifact_job_id IS NOT DISTINCT FROM $16::uuid
  AND existing.evidence = $19::jsonb
LIMIT 1`

const upsertBeaconImplementationObservationSQL = `
INSERT INTO beacon_implementation_observations AS current (
    chain_id, beacon_address, block_number, block_hash, beacon_code_hash,
    implementation_address, implementation_code_hash, stage_version,
    confidence, canonical, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5,
    $6, $7, $8, $9, TRUE, $10::jsonb
)
ON CONFLICT (chain_id, beacon_address, block_hash, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.beacon_code_hash = EXCLUDED.beacon_code_hash
  AND current.implementation_address = EXCLUDED.implementation_address
  AND current.implementation_code_hash = EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence`

const insertBeaconObservationGenerationSQL = `
INSERT INTO beacon_observation_generations (
    chain_id, beacon_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES ($1::numeric, $2, $3, $4, $5::bigint, $6::bigint)
ON CONFLICT DO NOTHING`

const carryForwardProxyGenerationSQL = `
WITH source_generation AS MATERIALIZED (
    SELECT publication.job_generation
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $5::bigint
      AND publication.job_generation < $6::bigint
      AND publication.chain_id = $1::numeric
      AND publication.block_number = $2::numeric
      AND publication.block_hash = $3
      AND publication.stage = 'proxy'
      AND publication.stage_version = $4
      AND publication.state = 'complete'
    ORDER BY publication.job_generation DESC
    LIMIT 1
), redetected AS MATERIALIZED (
    SELECT generation.proxy_address AS address
    FROM proxy_observation_generations AS generation
    WHERE generation.chain_id = $1::numeric
      AND generation.observation_block_hash = $3
      AND generation.observation_stage_version = $4
      AND generation.durable_job_id = $5::bigint
      AND generation.job_generation = $6::bigint
    UNION
    SELECT generation.beacon_address AS address
    FROM beacon_observation_generations AS generation
    WHERE generation.chain_id = $1::numeric
      AND generation.observation_block_hash = $3
      AND generation.observation_stage_version = $4
      AND generation.durable_job_id = $5::bigint
      AND generation.job_generation = $6::bigint
    UNION
	SELECT generation.implementation_address AS address
	FROM uups_implementation_observation_generations AS generation
	WHERE generation.chain_id = $1::numeric
	  AND generation.observation_block_hash = $3
	  AND generation.observation_stage_version = $4
	  AND generation.durable_job_id = $5::bigint
	  AND generation.job_generation = $6::bigint
	UNION
    SELECT evidence.address
    FROM proxy_detection_evidence AS evidence
    WHERE evidence.chain_id = $1::numeric
      AND evidence.block_number = $2::numeric
      AND evidence.block_hash = $3
      AND evidence.stage_version = $4
      AND evidence.durable_job_id = $5::bigint
      AND evidence.job_generation = $6::bigint
), carried_proxies AS (
    INSERT INTO proxy_observation_generations (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    )
    SELECT source.chain_id, source.proxy_address, source.observation_block_hash,
           source.observation_stage_version, $5::bigint, $6::bigint
    FROM proxy_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.proxy_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_beacons AS (
    INSERT INTO beacon_observation_generations (
        chain_id, beacon_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    )
    SELECT source.chain_id, source.beacon_address, source.observation_block_hash,
           source.observation_stage_version, $5::bigint, $6::bigint
    FROM beacon_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.beacon_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_uups AS (
	INSERT INTO uups_implementation_observation_generations (
		chain_id, implementation_address, observation_block_hash,
		observation_stage_version, verification_job_id,
		durable_job_id, job_generation
	)
	SELECT source.chain_id, source.implementation_address,
		   source.observation_block_hash, source.observation_stage_version,
		   source.verification_job_id, $5::bigint, $6::bigint
	FROM uups_implementation_observation_generations AS source
	JOIN source_generation
	  ON source.job_generation = source_generation.job_generation
	WHERE source.chain_id = $1::numeric
	  AND source.observation_block_hash = $3
	  AND source.observation_stage_version = $4
	  AND source.durable_job_id = $5::bigint
	  AND NOT EXISTS (
		  SELECT 1 FROM redetected
		  WHERE redetected.address = source.implementation_address
	  )
	ON CONFLICT DO NOTHING
	RETURNING 1
), carried_resolutions AS (
    INSERT INTO proxy_artifact_resolutions (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, proxy_code_hash, proxy_kind,
        proxy_pattern, standard_version, implementation_address,
        implementation_code_hash, admin_address, admin_code_hash,
        beacon_address, beacon_code_hash, proxy_artifact_job_id,
        implementation_artifact_job_id, durable_job_id, job_generation,
        evidence
    )
    SELECT source.chain_id, source.proxy_address, source.observation_block_hash,
           source.observation_stage_version, source.proxy_code_hash,
           source.proxy_kind, source.proxy_pattern, source.standard_version,
           source.implementation_address, source.implementation_code_hash,
           source.admin_address, source.admin_code_hash,
           source.beacon_address, source.beacon_code_hash,
           source.proxy_artifact_job_id, source.implementation_artifact_job_id,
           $5::bigint, $6::bigint, source.evidence
    FROM proxy_artifact_resolutions AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.proxy_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_negative_evidence AS (
    INSERT INTO proxy_detection_evidence (
        chain_id, address, block_number, block_hash, stage_version, code_hash,
        candidate_kind, detection_state, reason, canonical,
        durable_job_id, job_generation, details
    )
    SELECT source.chain_id, source.address, source.block_number,
           source.block_hash, source.stage_version, source.code_hash,
           source.candidate_kind, source.detection_state, source.reason, TRUE,
           $5::bigint, $6::bigint, source.details
    FROM proxy_detection_evidence AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.block_number = $2::numeric
      AND source.block_hash = $3
      AND source.stage_version = $4
      AND source.durable_job_id = $5::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
)
SELECT (SELECT count(*) FROM carried_proxies),
       (SELECT count(*) FROM carried_beacons),
	   (SELECT count(*) FROM carried_uups),
       (SELECT count(*) FROM carried_resolutions),
       (SELECT count(*) FROM carried_negative_evidence)`

const upsertProxyUpgradeEventSQL = `
INSERT INTO proxy_upgrade_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    emitter_address, event_kind, target_address, stage_version, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4::bigint, $5,
    $6, $7, $8, $9, TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.emitter_address = EXCLUDED.emitter_address
  AND current.event_kind = EXCLUDED.event_kind
  AND current.target_address = EXCLUDED.target_address`

const upsertProxyInitializationEventSQL = `
INSERT INTO proxy_initialization_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    contract_address, version, stage_version, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4::bigint, $5,
    $6, $7::numeric, $8, TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.contract_address = EXCLUDED.contract_address
  AND current.version = EXCLUDED.version`
