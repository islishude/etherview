package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/islishude/etherview/internal/ethrpc"
)

var ProxyStage = StageID{Name: "proxy", Version: 1}

var errProxyDependencyPending = errors.New("proxy stage dependency is not complete")

const (
	proxySourceTransaction = "transaction_target"
	proxySourceLog         = "log_target"
	proxySourceTrace       = "trace_target"
	proxySourceReceipt     = "creation_receipt"
	proxySourceTraceCreate = "trace_create"
	proxySourceUpgrade     = "upgrade_event"
	proxySourceReplay      = "exact_replay"
)

var (
	proxyUpgradedTopic       = SignatureHash("Upgraded(address)")
	proxyBeaconUpgradedTopic = SignatureHash("BeaconUpgraded(address)")
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
	address Address
	force   bool
	sources map[string]struct{}
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

type proxyResolution struct {
	kind               ProxyKind
	implementation     Address
	implementationCode []byte
	implementationHash Word
	beacon             *Address
	minimalExact       bool
	immutableArgsBytes int
}

type proxyDetection struct {
	candidate proxyCandidate
	code      []byte
	codeHash  Word
	proxy     *proxyResolution
	rejected  string
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
	candidates, canonical, err := processor.loadCandidates(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return processor.persist(ctx, job, nil, "stale_canonical_skipped")
	}
	if len(candidates) == 0 {
		return processor.persist(ctx, job, nil, "complete")
	}
	endpoint, err := processor.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return StageResult{}, Unavailable(errors.New("state RPC endpoint is unavailable"))
	}
	detector := rpcProxyDetector{caller: endpoint.Client, limits: processor.limits}
	detections, err := detector.detectBlock(ctx, job, candidates)
	if err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		return StageResult{}, err
	}
	processor.pool.ReportSuccess(endpoint.Name)
	return processor.persist(ctx, job, detections, "complete")
}

func (processor *PostgresProxyProcessor) loadCandidates(ctx context.Context, job Job) ([]proxyCandidate, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, proxyCanonicalSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&canonical); err != nil {
		return nil, false, fmt.Errorf("check proxy block canonicality: %w", err)
	}
	if !canonical {
		return nil, false, nil
	}
	candidates := make(map[Address]proxyCandidate)
	add := func(address Address, source string, force bool) error {
		if address == (Address{}) {
			return Permanent(errors.New("proxy discovery produced the zero address"))
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
		return nil, false, err
	}
	if err := processor.loadReceiptCandidates(ctx, job, add); err != nil {
		return nil, false, err
	}
	if err := processor.loadLogCandidates(ctx, job, add); err != nil {
		return nil, false, err
	}
	if err := processor.loadTraceCandidates(ctx, job, add); err != nil {
		return nil, false, err
	}
	if err := processor.loadReplayCandidates(ctx, job, add); err != nil {
		return nil, false, err
	}

	result := make([]proxyCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.force {
			hasHistory, err := processor.hasCanonicalCodeHistory(ctx, job, candidate.address)
			if err != nil {
				return nil, false, err
			}
			if hasHistory {
				continue
			}
		}
		result = append(result, candidate)
	}
	slices.SortFunc(result, func(left, right proxyCandidate) int {
		return bytes.Compare(left.address[:], right.address[:])
	})
	return result, true, nil
}

type proxyCandidateAdder func(Address, string, bool) error

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
		var wire ethrpc.Transaction
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Permanent(errors.New("stored proxy transaction is invalid"))
		}
		if err := validateABITransactionIdentity(wire, job, hash); err != nil {
			return Permanent(fmt.Errorf("proxy transaction identity: %w", err))
		}
		if wire.To != nil {
			address, err := ParseAddress(wire.To.String())
			if err != nil {
				return Permanent(errors.New("stored proxy transaction target is invalid"))
			}
			if err := add(address, proxySourceTransaction, false); err != nil {
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
		var wire ethrpc.Receipt
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Permanent(errors.New("stored proxy receipt is invalid"))
		}
		if err := validateProxyReceipt(wire, job, uint64(index), hash); err != nil {
			return Permanent(err)
		}
		if wire.ContractAddress != nil {
			address, err := ParseAddress(wire.ContractAddress.String())
			if err != nil {
				return Permanent(errors.New("stored creation address is invalid"))
			}
			if err := add(address, proxySourceReceipt, true); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy creation receipts: %w", err)
	}
	return nil
}

func validateProxyReceipt(wire ethrpc.Receipt, job Job, index uint64, hash Word) error {
	wireIndex, err := wire.TransactionIndex.Uint64()
	if err != nil || wireIndex != index || wire.TransactionHash.String() != hash.String() {
		return errors.New("stored proxy receipt transaction identity mismatch")
	}
	wireNumber, err := wire.BlockNumber.Uint64()
	if err != nil || wireNumber != job.BlockNumber || wire.BlockHash.String() != job.BlockHash.String() {
		return errors.New("stored proxy receipt block identity mismatch")
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadLogCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT log_index, tx_hash, address, topic0, raw
		FROM logs
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY log_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return fmt.Errorf("query proxy log targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var index int64
		var hashBytes, addressBytes, topicBytes, raw []byte
		if err := rows.Scan(&index, &hashBytes, &addressBytes, &topicBytes, &raw); err != nil {
			return fmt.Errorf("scan proxy log target: %w", err)
		}
		if index < 0 || len(addressBytes) != 20 {
			return Permanent(errors.New("stored proxy log identity is invalid"))
		}
		hash, err := WordFromBytes(hashBytes)
		if err != nil {
			return Permanent(errors.New("stored proxy log transaction hash is invalid"))
		}
		var address Address
		copy(address[:], addressBytes)
		var wire ethrpc.Log
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Permanent(errors.New("stored proxy log is invalid"))
		}
		if err := validateABILogIdentity(wire, job, uint64(index), hash, address); err != nil {
			return Permanent(fmt.Errorf("proxy log identity: %w", err))
		}
		force := false
		if len(topicBytes) != 0 {
			topic, err := WordFromBytes(topicBytes)
			if err != nil || len(wire.Topics) == 0 || wire.Topics[0].String() != topic.String() {
				return Permanent(errors.New("stored proxy log topic is invalid"))
			}
			force = topic == proxyUpgradedTopic || topic == proxyBeaconUpgradedTopic
		}
		if err := add(address, proxySourceLog, false); err != nil {
			return err
		}
		if force {
			if err := add(address, proxySourceUpgrade, true); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proxy log targets: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) loadTraceCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT call_type, to_address, created_address, reverted
		FROM normalized_traces
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		  AND canonical
		ORDER BY transaction_index, trace_path`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
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
			if len(toBytes) != 20 {
				return Permanent(errors.New("stored proxy trace target is invalid"))
			}
			var address Address
			copy(address[:], toBytes)
			if err := add(address, proxySourceTrace, false); err != nil {
				return err
			}
		}
		if !reverted && (callType == "CREATE" || callType == "CREATE2") && len(createdBytes) != 0 {
			if len(createdBytes) != 20 {
				return Permanent(errors.New("stored proxy trace creation address is invalid"))
			}
			var address Address
			copy(address[:], createdBytes)
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

func (processor *PostgresProxyProcessor) loadReplayCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := processor.db.QueryContext(ctx, `
		SELECT proxy_address
		FROM proxy_observations
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY proxy_address`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return fmt.Errorf("query exact proxy replay targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var addressBytes []byte
		if err := rows.Scan(&addressBytes); err != nil {
			return fmt.Errorf("scan exact proxy replay target: %w", err)
		}
		if len(addressBytes) != 20 {
			return Permanent(errors.New("stored exact proxy address is invalid"))
		}
		var address Address
		copy(address[:], addressBytes)
		if err := add(address, proxySourceReplay, true); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate exact proxy replay targets: %w", err)
	}
	return nil
}

func (processor *PostgresProxyProcessor) hasCanonicalCodeHistory(ctx context.Context, job Job, address Address) (bool, error) {
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
	caller ethrpc.Caller
	limits ProxyLimits
}

func (detector rpcProxyDetector) detectBlock(ctx context.Context, job Job, candidates []proxyCandidate) ([]proxyDetection, error) {
	if detector.caller == nil {
		return nil, errors.New("proxy RPC detector is not configured")
	}
	blockReference := map[string]any{"blockHash": job.BlockHash.String(), "requireCanonical": true}
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

func (detector rpcProxyDetector) detect(ctx context.Context, candidate proxyCandidate, blockReference map[string]any) (proxyDetection, error) {
	code, err := detector.getCode(ctx, candidate.address, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	detection := proxyDetection{candidate: candidate, code: code, codeHash: codeHash(code)}
	if len(code) == 0 {
		return detection, nil
	}
	if minimal, ok := DetectEIP1167(code); ok {
		if minimal.Implementation == (Address{}) {
			detection.rejected = "minimal_zero_implementation"
			return detection, nil
		}
		if minimal.Implementation == candidate.address {
			detection.rejected = "self_implementation"
			return detection, nil
		}
		implementationCode, err := detector.getCode(ctx, minimal.Implementation, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		if len(implementationCode) == 0 {
			detection.rejected = "implementation_has_no_code"
			return detection, nil
		}
		detection.proxy = &proxyResolution{
			kind: ProxyMinimal1167, implementation: minimal.Implementation,
			implementationCode: implementationCode, implementationHash: codeHash(implementationCode),
			minimalExact: minimal.Exact, immutableArgsBytes: len(minimal.TrailingData),
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
	references, err := ParseEIP1967Storage(implementationWord, beaconWord)
	if err != nil {
		detection.rejected = "invalid_slot_address"
		return detection, nil
	}
	if len(references) == 0 {
		return detection, nil
	}
	if len(references) != 1 {
		detection.rejected = "ambiguous_slots"
		return detection, nil
	}
	reference := references[0]
	implementation := reference.Target
	var beacon *Address
	if reference.Kind == ProxyBeacon {
		beaconAddress := reference.Target
		beacon = &beaconAddress
		var valid bool
		implementation, valid, err = detector.beaconImplementation(ctx, beaconAddress, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		if !valid {
			detection.rejected = "invalid_beacon_implementation"
			return detection, nil
		}
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
		detection.rejected = "implementation_has_no_code"
		return detection, nil
	}
	detection.proxy = &proxyResolution{
		kind: reference.Kind, implementation: implementation,
		implementationCode: implementationCode, implementationHash: codeHash(implementationCode), beacon: beacon,
	}
	return detection, nil
}

func (detector rpcProxyDetector) getCode(ctx context.Context, address Address, blockReference map[string]any) ([]byte, error) {
	var encoded ethrpc.Data
	if err := detector.caller.Call(ctx, "eth_getCode", []any{address.String(), blockReference}, &encoded); err != nil {
		return nil, exactStateRPCError(ctx, "eth_getCode", err)
	}
	code, err := encoded.Bytes()
	if err != nil {
		return nil, Permanent(errors.New("eth_getCode returned invalid bytecode"))
	}
	if len(code) > detector.limits.MaxCodeBytes {
		return nil, Permanent(errors.New("contract bytecode exceeds proxy detection limit"))
	}
	if code == nil {
		// An exact empty-code observation is different from SQL NULL (code bytes
		// deliberately omitted). Keep an allocated zero-length slice so the
		// Keccak(empty) fact can be audited and reused.
		code = make([]byte, 0)
	}
	return code, nil
}

func (detector rpcProxyDetector) getStorage(ctx context.Context, address Address, slot Word, blockReference map[string]any) (Word, error) {
	var encoded ethrpc.Data
	if err := detector.caller.Call(ctx, "eth_getStorageAt", []any{address.String(), slot.String(), blockReference}, &encoded); err != nil {
		return Word{}, exactStateRPCError(ctx, "eth_getStorageAt", err)
	}
	value, err := encoded.Bytes()
	if err != nil || len(value) != 32 {
		return Word{}, Permanent(errors.New("eth_getStorageAt returned a non-word value"))
	}
	return WordFromBytes(value)
}

func (detector rpcProxyDetector) beaconImplementation(ctx context.Context, beacon Address, blockReference map[string]any) (Address, bool, error) {
	selector := SignatureSelector("implementation()")
	request := map[string]any{"to": beacon.String(), "data": ethrpc.DataFromBytes(selector[:]).String()}
	var encoded ethrpc.Data
	if err := detector.caller.Call(ctx, "eth_call", []any{request, blockReference}, &encoded); err != nil {
		if executionReverted(err) {
			return Address{}, false, nil
		}
		return Address{}, false, exactStateRPCError(ctx, "eth_call", err)
	}
	value, err := encoded.Bytes()
	if err != nil {
		return Address{}, false, Permanent(errors.New("beacon implementation RPC returned invalid data"))
	}
	implementation, err := ParseBeaconImplementation(value)
	if err != nil {
		return Address{}, false, nil
	}
	return implementation, true, nil
}

func (processor *PostgresProxyProcessor) persist(ctx context.Context, job Job, detections []proxyDetection, outcome string) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistTx(ctx, tx, job, detections, outcome)
	})
}

func (processor *PostgresProxyProcessor) persistTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	detections []proxyDetection,
	outcome string,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		outcome = "stale_canonical_skipped"
		detections = nil
	}
	codeObservations := make(map[Address]proxyCodeObservation)
	proxyCount := 0
	rejectedCount := 0
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
		if err := mergeProxyCodeObservation(
			codeObservations, detection.proxy.implementation,
			detection.proxy.implementationHash, detection.proxy.implementationCode,
		); err != nil {
			return StageResult{}, Permanent(err)
		}
	}
	addresses := make([]Address, 0, len(codeObservations))
	for address := range codeObservations {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(left, right Address) int { return bytes.Compare(left[:], right[:]) })
	for _, address := range addresses {
		if err := persistProxyCodeObservation(ctx, tx, job, codeObservations[address]); err != nil {
			return StageResult{}, err
		}
	}
	for _, detection := range detections {
		if detection.proxy != nil {
			if err := processor.persistProxyObservation(ctx, tx, job, detection); err != nil {
				return StageResult{}, err
			}
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
	details := map[string]string{
		"outcome": outcome, "candidates": strconv.Itoa(len(detections)),
		"code_observations": strconv.Itoa(len(codeObservations)), "proxies": strconv.Itoa(proxyCount),
		"rejected_candidates": strconv.Itoa(rejectedCount),
		"abi_requeued":        strconv.FormatBool(abiRequeued),
	}
	return StageResult{State: ResultComplete, Details: details}, nil
}

type proxyCodeObservation struct {
	address  Address
	codeHash Word
	code     []byte
}

func mergeProxyCodeObservation(observations map[Address]proxyCodeObservation, address Address, hash Word, code []byte) error {
	if address == (Address{}) || hash.IsZero() {
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
		details["minimal_runtime"] = map[bool]string{true: "canonical", false: "immutable_args"}[resolved.minimalExact]
		details["immutable_args_bytes"] = resolved.immutableArgsBytes
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode proxy observation details: %w", err)
	}
	if len(encoded) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("proxy observation details exceed configured limit"))
	}
	var beacon any
	if resolved.beacon != nil {
		beacon = resolved.beacon[:]
	}
	result, err := tx.ExecContext(ctx, upsertProxyObservationSQL,
		job.ChainID, detection.candidate.address[:], strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		detection.codeHash[:], resolved.kind, resolved.implementation[:], beacon,
		resolved.implementationHash[:], ConfidenceHigh, string(encoded),
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
    chain_id, proxy_address, block_number, block_hash, proxy_code_hash,
    proxy_kind, implementation_address, beacon_address,
    implementation_code_hash, confidence, canonical, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5,
    $6, $7, $8, $9, $10, TRUE, $11::jsonb
)
ON CONFLICT (chain_id, proxy_address, block_hash) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.proxy_code_hash = EXCLUDED.proxy_code_hash
  AND current.proxy_kind = EXCLUDED.proxy_kind
  AND current.implementation_address IS NOT DISTINCT FROM EXCLUDED.implementation_address
  AND current.beacon_address IS NOT DISTINCT FROM EXCLUDED.beacon_address
  AND current.implementation_code_hash IS NOT DISTINCT FROM EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence`
