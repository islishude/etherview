package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/ethrpc"
)

var ABIStage = StageID{Name: "abi", Version: 3}

const (
	abiObjectTransactionCalldata = "transaction_calldata"
	abiObjectLog                 = "log"
	abiObjectTraceCalldata       = "trace_calldata"
	abiObjectTraceConstructor    = "trace_constructor"
	abiObjectTraceRevert         = "trace_revert"
	abiSignatureCandidatesPerID  = 64
)

// PostgresABIProcessor materializes block-bound ABI provenance and decoded
// core/trace observations. It only consumes previously persisted code and
// proxy observations; discovering either fact belongs to later stages.
type PostgresABIProcessor struct {
	db                     *sql.DB
	limits                 DecodeLimits
	requireProxyDependency bool
}

func NewPostgresABIProcessor(db *sql.DB) (*PostgresABIProcessor, error) {
	if db == nil {
		return nil, errors.New("ABI processor requires a database")
	}
	return &PostgresABIProcessor{db: db, limits: DefaultDecodeLimits()}, nil
}

// NewPostgresABIProcessorWithProxyDependency is the production constructor.
// The dependency prevents ABI guesses or unbound results from becoming
// terminal before the same-version proxy stage has either completed or reported explicit
// unavailability for the same immutable block.
func NewPostgresABIProcessorWithProxyDependency(db *sql.DB) (*PostgresABIProcessor, error) {
	processor, err := NewPostgresABIProcessor(db)
	if err != nil {
		return nil, err
	}
	processor.requireProxyDependency = true
	return processor, nil
}

func NewPostgresABIProcessorWithLimits(db *sql.DB, limits DecodeLimits) (*PostgresABIProcessor, error) {
	if db == nil {
		return nil, errors.New("ABI processor requires a database")
	}
	if err := limits.validate(); err != nil {
		return nil, fmt.Errorf("ABI processor limits: %w", err)
	}
	return &PostgresABIProcessor{db: db, limits: limits}, nil
}

func (*PostgresABIProcessor) Stage() StageID { return ABIStage }

func (processor *PostgresABIProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresABIProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil {
		return StageResult{}, errors.New("process ABI stage using nil database")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != ABIStage {
		return StageResult{}, Permanent(fmt.Errorf("ABI processor received stage %s", job.Stage))
	}
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.processTx(ctx, tx, job)
	})
}

func (processor *PostgresABIProcessor) processTx(ctx context.Context, tx *sql.Tx, job Job) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{
			State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"},
		}, nil
	}
	proxyDependency := "not_required"
	if processor.requireProxyDependency {
		state, err := proxyDependencyState(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
		switch state {
		case ResultComplete:
			proxyDependency = string(ResultComplete)
		case ResultUnavailable:
			return StageResult{}, Unavailable(errors.New("proxy stage is unavailable for this block"))
		case ResultFailed:
			return StageResult{}, errProxyDependencyPending
		default:
			return StageResult{}, errProxyDependencyPending
		}
	}

	observations, err := loadABIObservations(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM abi_decodings
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
		return StageResult{}, fmt.Errorf("clear ABI decodings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM contract_abis
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
		return StageResult{}, fmt.Errorf("clear ABI bindings: %w", err)
	}

	registry, err := NewABIRegistryWithLimits(processor.limits)
	if err != nil {
		return StageResult{}, Permanent(err)
	}
	identities := make(map[common.Address]ABIIdentity)
	bindingsCount, invalidSignatures := 0, 0
	for _, address := range uniqueABIAddresses(observations) {
		identity, codeRange, found, err := resolveABICodeIdentity(ctx, tx, job, address)
		if err != nil {
			return StageResult{}, err
		}
		if !found {
			continue
		}
		identities[address] = identity
		bindings, invalid, err := loadABIBindings(ctx, tx, identity, codeRange, observationsForAddress(observations, address), processor.limits)
		if err != nil {
			return StageResult{}, err
		}
		invalidSignatures += invalid
		for _, candidate := range bindings {
			if err := registry.RegisterJSON(candidate.binding, candidate.abi); err != nil {
				return StageResult{}, Permanent(fmt.Errorf("register persisted ABI binding: %w", err))
			}
			if err := persistABIBinding(ctx, tx, candidate); err != nil {
				return StageResult{}, err
			}
			bindingsCount++
		}
	}

	counts := map[DecodeStatus]int{}
	unbound := 0
	for _, observation := range observations {
		identity, found := identities[observation.target]
		if !found {
			unbound++
			continue
		}
		result := decodeABIObservation(registry, identity, observation)
		if err := persistABIDecoding(ctx, tx, job, identity, observation, result); err != nil {
			return StageResult{}, err
		}
		counts[result.result.Status]++
	}
	return StageResult{
		State: ResultComplete,
		Details: map[string]string{
			"proxy_dependency":   proxyDependency,
			"bindings":           strconv.Itoa(bindingsCount),
			"decoded":            strconv.Itoa(counts[DecodeDecoded]),
			"ambiguous":          strconv.Itoa(counts[DecodeAmbiguous]),
			"unknown":            strconv.Itoa(counts[DecodeUnknown]),
			"malformed":          strconv.Itoa(counts[DecodeMalformed]),
			"unbound":            strconv.Itoa(unbound),
			"invalid_signatures": strconv.Itoa(invalidSignatures),
		},
	}, nil
}

func proxyDependencyState(ctx context.Context, tx *sql.Tx, job Job) (ResultState, error) {
	var state string
	err := tx.QueryRowContext(ctx, `
		SELECT state
		FROM published_block_stage_results
		WHERE chain_id = $1::numeric AND block_hash = $2
		  AND stage = $3 AND stage_version = $4`,
		job.ChainID, job.BlockHash[:], ProxyStage.Name, ProxyStage.Version,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query ABI proxy dependency: %w", err)
	}
	result := ResultState(state)
	switch result {
	case ResultComplete, ResultUnavailable, ResultFailed:
		return result, nil
	default:
		return "", Permanent(errors.New("stored proxy stage state is invalid"))
	}
}

type abiObservation struct {
	objectKind      string
	transactionHash common.Hash
	objectIndex     string
	target          common.Address
	input           []byte
	topics          []common.Hash
	data            []byte
	output          []byte
	directReverted  bool
	malformed       string
}

func loadABIObservations(ctx context.Context, tx *sql.Tx, job Job) ([]abiObservation, error) {
	transactions, err := loadABITransactions(ctx, tx, job)
	if err != nil {
		return nil, err
	}
	logs, err := loadABILogs(ctx, tx, job)
	if err != nil {
		return nil, err
	}
	traces, err := loadABITraces(ctx, tx, job)
	if err != nil {
		return nil, err
	}
	constructors, err := loadABIConstructors(ctx, tx, job)
	if err != nil {
		return nil, err
	}
	result := make([]abiObservation, 0, len(transactions)+len(logs)+len(traces)+len(constructors))
	result = append(result, transactions...)
	result = append(result, logs...)
	result = append(result, traces...)
	result = append(result, constructors...)
	return result, nil
}

func loadABIConstructors(ctx context.Context, tx *sql.Tx, job Job) ([]abiObservation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT ON (trace.transaction_hash, trace.trace_path)
		       trace.transaction_hash, trace.trace_path, trace.created_address,
		       trace.input, verified.constructor_arguments, verified.abi,
		       verified.code_hash
		FROM normalized_traces AS trace
		JOIN contract_code_observations AS code
		  ON code.chain_id = trace.chain_id
		 AND code.block_number = trace.block_number
		 AND code.block_hash = trace.block_hash
		 AND code.address = trace.created_address
		 AND code.canonical
		JOIN verified_contracts AS verified
		  ON verified.chain_id = trace.chain_id
		 AND verified.address = trace.created_address
		 AND verified.code_hash = code.code_hash
		 AND verified.valid_from_block <= trace.block_number
		 AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= trace.block_number)
		 AND verified.abi IS NOT NULL
		JOIN verification_results AS result
		  ON result.job_id = verified.verification_job_id
		 AND result.request_digest = verified.request_digest
		 AND result.outcome_kind = 'verification_success'
		 AND result.outcome->'creation_match'->>'match_type' = 'full'
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.call_type IN ('CREATE', 'CREATE2')
		  AND trace.created_address IS NOT NULL
		  AND NOT trace.reverted
		  AND trace.canonical
		ORDER BY trace.transaction_hash, trace.trace_path, verified.valid_from_block DESC`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, fmt.Errorf("query exact constructor observations: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var observations []abiObservation
	for rows.Next() {
		var transactionHashBytes, targetBytes, initcode, arguments, abiJSON, codeHashBytes []byte
		var tracePath string
		if err := rows.Scan(
			&transactionHashBytes, &tracePath, &targetBytes, &initcode,
			&arguments, &abiJSON, &codeHashBytes,
		); err != nil {
			return nil, fmt.Errorf("scan exact constructor observation: %w", err)
		}
		transactionHash, err := WordFromBytes(transactionHashBytes)
		if err != nil || len(targetBytes) != common.AddressLength || len(codeHashBytes) != common.HashLength {
			return nil, Permanent(errors.New("exact constructor identity is invalid"))
		}
		observation := abiObservation{
			objectKind: abiObjectTraceConstructor, transactionHash: transactionHash,
			objectIndex: tracePath, target: common.BytesToAddress(targetBytes), input: common.CopyBytes(arguments),
		}
		if !bytes.HasSuffix(initcode, arguments) {
			observation.malformed = "verified constructor arguments are not an exact initcode suffix"
		} else if err := validateConstructorArguments(abiJSON, arguments); err != nil {
			observation.malformed = "verified constructor arguments do not re-encode exactly"
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact constructor observations: %w", err)
	}
	return observations, nil
}

func validateConstructorArguments(document, arguments []byte) error {
	parsed, err := gethabi.JSON(strings.NewReader(string(document)))
	if err != nil {
		return err
	}
	values, err := parsed.Constructor.Inputs.Unpack(arguments)
	if err != nil {
		return err
	}
	reencoded, err := parsed.Constructor.Inputs.Pack(values...)
	if err != nil {
		return err
	}
	if !bytes.Equal(reencoded, arguments) {
		return errors.New("constructor argument round trip mismatch")
	}
	return nil
}

func loadABITransactions(ctx context.Context, tx *sql.Tx, job Job) ([]abiObservation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT inclusion.tx_hash, inclusion.raw, resolution.execution_address,
		       resolution.execution_code_hash
		FROM transaction_inclusions AS inclusion
		LEFT JOIN transaction_execution_code_resolutions AS resolution
		  ON resolution.chain_id = inclusion.chain_id
		 AND resolution.block_number = inclusion.block_number
		 AND resolution.block_hash = inclusion.block_hash
		 AND resolution.transaction_hash = inclusion.tx_hash
		 AND resolution.context_address = decode(substring(inclusion.raw->>'to' from 3), 'hex')
		 AND resolution.resolution IN ('direct', 'eip7702_delegate')
		 AND resolution.canonical
		 AND EXISTS (
		     SELECT 1 FROM published_block_stage_results AS published
		     WHERE published.chain_id = resolution.chain_id
		       AND published.block_number = resolution.block_number
		       AND published.block_hash = resolution.block_hash
		       AND published.stage = $4
		       AND published.stage_version = $5
		       AND published.state = 'complete'
		 )
		WHERE inclusion.chain_id = $1::numeric
		  AND inclusion.block_number = $2::numeric
		  AND inclusion.block_hash = $3
		ORDER BY inclusion.tx_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		StateDiffStage.Name, StateDiffStage.Version)
	if err != nil {
		return nil, fmt.Errorf("query ABI transaction inputs: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []abiObservation
	for rows.Next() {
		var transactionHashBytes, raw, executionAddress, executionCodeHash []byte
		if err := rows.Scan(&transactionHashBytes, &raw, &executionAddress, &executionCodeHash); err != nil {
			return nil, fmt.Errorf("scan ABI transaction input: %w", err)
		}
		transactionHash, err := WordFromBytes(transactionHashBytes)
		if err != nil {
			return nil, Permanent(fmt.Errorf("stored transaction hash: %w", err))
		}
		var wire types.Transaction
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, Permanent(fmt.Errorf("decode stored transaction: %w", err))
		}
		if err := validateABITransactionIdentity(&wire, raw, job, transactionHash); err != nil {
			return nil, Permanent(err)
		}
		to := wire.To()
		if to == nil || len(executionAddress) == 0 {
			continue
		}
		if len(executionAddress) != common.AddressLength || len(executionCodeHash) != common.HashLength {
			return nil, Permanent(errors.New("transaction execution-code identity is invalid"))
		}
		target := common.BytesToAddress(executionAddress)
		input := wire.Data()
		result = append(result, abiObservation{
			objectKind: abiObjectTransactionCalldata, transactionHash: transactionHash,
			target: target, input: input,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ABI transaction inputs: %w", err)
	}
	return result, nil
}

func validateABITransactionIdentity(
	wire *types.Transaction,
	raw json.RawMessage,
	job Job,
	transactionHash common.Hash,
) error {
	if wire == nil || wire.Hash() != transactionHash {
		return errors.New("stored transaction raw identity is incomplete")
	}
	blockHash, blockNumber, err := storedTransactionInclusion(raw)
	if err != nil || blockHash != job.BlockHash {
		return errors.New("stored transaction block hash mismatch")
	}
	if blockNumber != job.BlockNumber {
		return errors.New("stored transaction block number mismatch")
	}
	return nil
}

func storedTransactionInclusion(
	raw json.RawMessage,
) (common.Hash, uint64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return common.Hash{}, 0, err
	}
	var hashText, numberText string
	if err := json.Unmarshal(fields["blockHash"], &hashText); err != nil {
		return common.Hash{}, 0, err
	}
	if err := json.Unmarshal(fields["blockNumber"], &numberText); err != nil {
		return common.Hash{}, 0, err
	}
	hash, err := ethrpc.ParseHash(hashText)
	if err != nil {
		return common.Hash{}, 0, err
	}
	number, err := ethrpc.ParseQuantity(numberText)
	if err != nil || !number.IsUint64() {
		return common.Hash{}, 0, errors.New(
			"stored transaction block number is invalid",
		)
	}
	return hash, number.Uint64(), nil
}

func loadABILogs(ctx context.Context, tx *sql.Tx, job Job) ([]abiObservation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT log.log_index, log.tx_hash, log.address, log.raw,
		       attribution.execution_address
		FROM logs AS log
		LEFT JOIN trace_log_attributions AS attribution
		  ON attribution.chain_id = log.chain_id
		 AND attribution.block_number = log.block_number
		 AND attribution.block_hash = log.block_hash
		 AND attribution.transaction_hash = log.tx_hash
		 AND attribution.log_index = log.log_index
		 AND attribution.canonical
		 AND EXISTS (
		     SELECT 1
		     FROM published_block_stage_results AS published
		     WHERE published.chain_id = attribution.chain_id
		       AND published.block_hash = attribution.block_hash
		       AND published.stage = $4
		       AND published.stage_version = $5
		       AND published.state = 'complete'
		 )
		WHERE log.chain_id = $1::numeric AND log.block_number = $2::numeric AND log.block_hash = $3
		ORDER BY log.log_index`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		TraceStage.Name, TraceStage.Version)
	if err != nil {
		return nil, fmt.Errorf("query ABI logs: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []abiObservation
	for rows.Next() {
		var logIndex int64
		var transactionHashBytes, addressBytes, raw, executionAddressBytes []byte
		if err := rows.Scan(&logIndex, &transactionHashBytes, &addressBytes, &raw, &executionAddressBytes); err != nil {
			return nil, fmt.Errorf("scan ABI log: %w", err)
		}
		if logIndex < 0 {
			return nil, Permanent(errors.New("stored ABI log index is negative"))
		}
		transactionHash, err := WordFromBytes(transactionHashBytes)
		if err != nil {
			return nil, Permanent(fmt.Errorf("stored ABI log transaction hash: %w", err))
		}
		if len(addressBytes) != common.AddressLength {
			return nil, Permanent(errors.New("stored ABI log address is not 20 bytes"))
		}
		emitter := common.BytesToAddress(addressBytes)
		target := emitter
		if len(executionAddressBytes) != 0 {
			if len(executionAddressBytes) != common.AddressLength {
				return nil, Permanent(errors.New("stored trace log execution address is not 20 bytes"))
			}
			target = common.BytesToAddress(executionAddressBytes)
		}
		var wire types.Log
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, Permanent(fmt.Errorf("decode stored ABI log: %w", err))
		}
		if err := validateABILogIdentity(wire, job, uint64(logIndex), transactionHash, emitter); err != nil {
			return nil, Permanent(err)
		}
		data := common.CopyBytes(wire.Data)
		topics := append([]common.Hash(nil), wire.Topics...)
		result = append(result, abiObservation{
			objectKind: abiObjectLog, transactionHash: transactionHash,
			objectIndex: strconv.FormatInt(logIndex, 10), target: target, topics: topics, data: data,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ABI logs: %w", err)
	}
	return result, nil
}

func validateABILogIdentity(
	wire types.Log,
	job Job,
	logIndex uint64,
	transactionHash common.Hash,
	target common.Address,
) error {
	if uint64(wire.Index) != logIndex || wire.TxHash != transactionHash ||
		wire.Address != target {
		return errors.New("stored ABI log identity mismatch")
	}
	if wire.BlockHash != job.BlockHash {
		return errors.New("stored ABI log block hash mismatch")
	}
	if wire.BlockNumber != job.BlockNumber {
		return errors.New("stored ABI log block number mismatch")
	}
	return nil
}

func loadABITraces(ctx context.Context, tx *sql.Tx, job Job) ([]abiObservation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT transaction_hash, trace_path, execution_address, input, output, direct_reverted
		FROM normalized_traces AS trace
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.canonical
		  AND trace.execution_address IS NOT NULL
		  AND trace.execution_resolution IN ('direct', 'eip7702_delegate', 'unavailable')
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
		return nil, fmt.Errorf("query ABI traces: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []abiObservation
	for rows.Next() {
		var transactionHashBytes, targetBytes, input, output []byte
		var tracePath string
		var directReverted bool
		if err := rows.Scan(&transactionHashBytes, &tracePath, &targetBytes, &input, &output, &directReverted); err != nil {
			return nil, fmt.Errorf("scan ABI trace: %w", err)
		}
		if len(targetBytes) == 0 {
			continue
		}
		// The normalized transaction root deliberately uses the empty trace
		// path. Child paths are non-empty, but emptiness alone is not an invalid
		// identity.
		if len(targetBytes) != common.AddressLength {
			return nil, Permanent(errors.New("stored ABI trace identity is invalid"))
		}
		transactionHash, err := WordFromBytes(transactionHashBytes)
		if err != nil {
			return nil, Permanent(fmt.Errorf("stored ABI trace transaction hash: %w", err))
		}
		target := common.BytesToAddress(targetBytes)
		result = append(result, abiObservation{
			objectKind: abiObjectTraceCalldata, transactionHash: transactionHash,
			objectIndex: tracePath, target: target, input: append([]byte(nil), input...),
			output: append([]byte(nil), output...), directReverted: directReverted,
		})
		if directReverted && len(output) > 0 {
			result = append(result, abiObservation{
				objectKind: abiObjectTraceRevert, transactionHash: transactionHash,
				objectIndex: tracePath, target: target, input: append([]byte(nil), output...),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ABI traces: %w", err)
	}
	return result, nil
}

func uniqueABIAddresses(observations []abiObservation) []common.Address {
	seen := make(map[common.Address]struct{})
	result := make([]common.Address, 0)
	for _, observation := range observations {
		if _, exists := seen[observation.target]; exists {
			continue
		}
		seen[observation.target] = struct{}{}
		result = append(result, observation.target)
	}
	return result
}

func observationsForAddress(observations []abiObservation, address common.Address) []abiObservation {
	result := make([]abiObservation, 0)
	for _, observation := range observations {
		if observation.target == address {
			result = append(result, observation)
		}
	}
	return result
}

type abiBlockRange struct {
	from uint64
	to   *uint64
}

func resolveABICodeIdentity(ctx context.Context, tx *sql.Tx, job Job, address common.Address) (ABIIdentity, abiBlockRange, bool, error) {
	var blockNumberText string
	var codeHashBytes []byte
	err := tx.QueryRowContext(ctx, `
		SELECT observation.block_number::text, observation.code_hash
		FROM contract_code_observations AS observation
		WHERE observation.chain_id = $1::numeric AND observation.address = $2 AND observation.canonical
		  AND observation.block_number <= $3::numeric
		ORDER BY observation.block_number DESC, observation.observed_at DESC
		LIMIT 1`, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&blockNumberText, &codeHashBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return ABIIdentity{}, abiBlockRange{}, false, nil
	}
	if err != nil {
		return ABIIdentity{}, abiBlockRange{}, false, fmt.Errorf("query ABI code identity: %w", err)
	}
	from, err := strconv.ParseUint(blockNumberText, 10, 64)
	if err != nil {
		return ABIIdentity{}, abiBlockRange{}, false, Permanent(fmt.Errorf("decode ABI code range start: %w", err))
	}
	codeHash, err := WordFromBytes(codeHashBytes)
	if err != nil || codeHash == (common.Hash{}) {
		return ABIIdentity{}, abiBlockRange{}, false, Permanent(errors.New("stored ABI code hash is invalid"))
	}
	var next sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT min(block_number)::text
		FROM contract_code_observations
		WHERE chain_id = $1::numeric AND address = $2 AND canonical
		  AND block_number > $3::numeric AND code_hash <> $4`,
		job.ChainID, address[:], blockNumberText, codeHash[:]).Scan(&next); err != nil {
		return ABIIdentity{}, abiBlockRange{}, false, fmt.Errorf("query ABI code range end: %w", err)
	}
	codeRange := abiBlockRange{from: from}
	if next.Valid {
		value, err := strconv.ParseUint(next.String, 10, 64)
		if err != nil || value == 0 {
			return ABIIdentity{}, abiBlockRange{}, false, Permanent(errors.New("stored ABI code range end is invalid"))
		}
		end := value - 1
		codeRange.to = &end
	}
	identity := ABIIdentity{
		ChainID: job.ChainID, Address: address, CodeHash: codeHash,
		BlockNumber: job.BlockNumber, BlockHash: job.BlockHash,
	}
	if err := identity.validate(); err != nil {
		return ABIIdentity{}, abiBlockRange{}, false, Permanent(err)
	}
	return identity, codeRange, true, nil
}

type persistedABIBinding struct {
	binding ABIBinding
	abi     []byte
}

func loadABIBindings(
	ctx context.Context,
	tx *sql.Tx,
	identity ABIIdentity,
	codeRange abiBlockRange,
	observations []abiObservation,
	limits DecodeLimits,
) ([]persistedABIBinding, int, error) {
	result := make([]persistedABIBinding, 0, 3)
	direct, found, err := loadVerifiedABIBinding(ctx, tx, identity, identity.Address, identity.CodeHash, codeRange, ABISourceVerified)
	if err != nil {
		return nil, 0, err
	}
	if found {
		result = append(result, direct)
	}
	sameCode, found, err := loadSameCodeABIBinding(ctx, tx, identity, codeRange)
	if err != nil {
		return nil, 0, err
	}
	if found {
		result = append(result, sameCode)
	}
	proxy, found, err := loadProxyABIBinding(ctx, tx, identity, codeRange)
	if err != nil {
		return nil, 0, err
	}
	if found {
		result = append(result, proxy)
	}
	guesses, invalid, err := loadSignatureABIBinding(ctx, tx, identity, codeRange, observations, limits)
	if err != nil {
		return nil, 0, err
	}
	if len(guesses.abi) > 0 {
		result = append(result, guesses)
	}
	return result, invalid, nil
}

func loadSameCodeABIBinding(
	ctx context.Context,
	tx *sql.Tx,
	target ABIIdentity,
	codeRange abiBlockRange,
) (persistedABIBinding, bool, error) {
	var sourceAddressBytes, abi []byte
	err := tx.QueryRowContext(ctx, `
		SELECT address, abi
		FROM verified_contracts
		WHERE chain_id = $1::numeric
		  AND code_hash = $2
		  AND abi IS NOT NULL
		  AND (address <> $3 OR valid_from_block > $4::numeric OR
		       (valid_to_block IS NOT NULL AND valid_to_block < $4::numeric))
		ORDER BY (match_type = 'full') DESC, (address = $3) DESC, created_at DESC,
		         request_digest ASC, verification_job_id ASC, address
		LIMIT 1`, target.ChainID, target.CodeHash[:], target.Address[:],
		strconv.FormatUint(target.BlockNumber, 10)).Scan(&sourceAddressBytes, &abi)
	if errors.Is(err, sql.ErrNoRows) {
		return persistedABIBinding{}, false, nil
	}
	if err != nil {
		return persistedABIBinding{}, false, fmt.Errorf("query same-code verified ABI: %w", err)
	}
	if len(sourceAddressBytes) != common.AddressLength {
		return persistedABIBinding{}, false, Permanent(errors.New("same-code ABI source address is invalid"))
	}
	sourceAddress := common.BytesToAddress(sourceAddressBytes)
	binding := ABIBinding{
		Identity: target, Source: ABISourceCodeHash,
		SourceAddress: sourceAddress, SourceCodeHash: target.CodeHash,
		ValidFromBlock: codeRange.from, ValidToBlock: codeRange.to,
	}
	return persistedABIBinding{binding: binding, abi: append([]byte(nil), abi...)}, true, nil
}

func loadVerifiedABIBinding(
	ctx context.Context,
	tx *sql.Tx,
	target ABIIdentity,
	sourceAddress common.Address,
	sourceCodeHash common.Hash,
	baseRange abiBlockRange,
	source ABISource,
) (persistedABIBinding, bool, error) {
	var abi []byte
	var fromText string
	var to sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT abi, valid_from_block::text, valid_to_block::text
		FROM verified_contracts
		WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3
		  AND abi IS NOT NULL
		  AND valid_from_block <= $4::numeric
		  AND (valid_to_block IS NULL OR valid_to_block >= $4::numeric)
		ORDER BY (match_type = 'full') DESC, valid_from_block DESC,
		         request_digest ASC, verification_job_id ASC
		LIMIT 1`, target.ChainID, sourceAddress[:], sourceCodeHash[:], strconv.FormatUint(target.BlockNumber, 10)).Scan(&abi, &fromText, &to)
	if errors.Is(err, sql.ErrNoRows) {
		return persistedABIBinding{}, false, nil
	}
	if err != nil {
		return persistedABIBinding{}, false, fmt.Errorf("query verified ABI: %w", err)
	}
	verifiedRange, err := scanABIRange(fromText, to)
	if err != nil {
		return persistedABIBinding{}, false, Permanent(fmt.Errorf("decode verified ABI range: %w", err))
	}
	validity, ok := intersectABIRanges(baseRange, verifiedRange)
	if !ok || !rangeContains(validity, target.BlockNumber) {
		return persistedABIBinding{}, false, Permanent(errors.New("verified ABI range does not cover target identity"))
	}
	binding := ABIBinding{
		Identity: target, Source: source, SourceAddress: sourceAddress,
		SourceCodeHash: sourceCodeHash, ValidFromBlock: validity.from, ValidToBlock: validity.to,
	}
	return persistedABIBinding{binding: binding, abi: append([]byte(nil), abi...)}, true, nil
}

func loadProxyABIBinding(ctx context.Context, tx *sql.Tx, target ABIIdentity, codeRange abiBlockRange) (persistedABIBinding, bool, error) {
	var implementationAddressBytes, implementationCodeHashBytes []byte
	err := tx.QueryRowContext(ctx, `
		WITH published_proxy_candidates AS (
		    SELECT observation.*, generation.id AS observation_generation_id,
		           generation.durable_job_id, generation.job_generation
		    FROM proxy_observations AS observation
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = observation.chain_id
		     AND canonical.number = observation.block_number
		     AND canonical.block_hash = observation.block_hash
		    JOIN proxy_observation_generations AS generation
		      ON generation.chain_id = observation.chain_id
		     AND generation.proxy_address = observation.proxy_address
		     AND generation.observation_block_hash = observation.block_hash
		     AND generation.observation_stage_version = observation.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = generation.chain_id
		     AND published.block_hash = generation.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = generation.observation_stage_version
		     AND published.durable_job_id = generation.durable_job_id
		     AND published.job_generation = generation.job_generation
		     AND published.state = 'complete'
		    WHERE observation.chain_id = $1::numeric
		      AND observation.proxy_address = $2
		      AND observation.proxy_code_hash = $3
		      AND observation.stage_version = $6
		      AND observation.canonical
		      AND observation.confidence IN ('verified', 'high')
		      AND observation.block_number <= $4::numeric
		), resolved_candidates AS (
		    SELECT raw.*, resolution.id AS artifact_resolution_id,
		           resolution.proxy_kind AS resolved_kind,
		           resolution.proxy_pattern AS resolved_pattern,
		           resolution.implementation_address AS resolved_implementation,
		           resolution.implementation_code_hash AS resolved_implementation_hash,
		           resolution.beacon_address AS resolved_beacon,
		           resolution.beacon_code_hash AS resolved_beacon_hash
		    FROM published_proxy_candidates AS raw
		    LEFT JOIN LATERAL (
		        SELECT candidate.*
		        FROM proxy_artifact_resolutions AS candidate
		        JOIN published_block_stage_results AS published
		          ON published.chain_id = candidate.chain_id
		         AND published.block_hash = candidate.observation_block_hash
		         AND published.stage = 'proxy'
		         AND published.stage_version = candidate.observation_stage_version
		         AND published.durable_job_id = candidate.durable_job_id
		         AND published.job_generation = candidate.job_generation
		         AND published.state = 'complete'
		        WHERE candidate.chain_id = raw.chain_id
		          AND candidate.proxy_address = raw.proxy_address
		          AND candidate.observation_block_hash = raw.block_hash
		          AND candidate.observation_stage_version = raw.stage_version
		          AND candidate.proxy_code_hash = raw.proxy_code_hash
		          AND candidate.durable_job_id = raw.durable_job_id
		          AND candidate.job_generation = raw.job_generation
		        ORDER BY candidate.id DESC
		        LIMIT 1
		    ) AS resolution ON raw.proxy_pattern <> 'clone'
		    WHERE (
		        raw.proxy_pattern = 'clone'
		        AND raw.evidence_state = 'exact'
		        AND raw.implementation_address IS NOT NULL
		        AND raw.implementation_code_hash IS NOT NULL
		    ) OR (
		        resolution.id IS NOT NULL
		        AND (
		            resolution.proxy_pattern = 'beacon'
		            OR (raw.block_number = $4::numeric AND raw.block_hash = $5)
		        )
		    ) OR (
		        resolution.id IS NULL
		        AND raw.proxy_kind = 'eip1967'
		        AND raw.proxy_pattern = 'unknown'
		        AND raw.evidence_state = 'generic'
		        AND raw.beacon_address IS NULL
		        AND raw.beacon_code_hash IS NULL
		        AND raw.block_number = $4::numeric
		        AND raw.block_hash = $5
		        AND raw.implementation_address IS NOT NULL
		        AND raw.implementation_code_hash IS NOT NULL
		    )
		), selected_proxy AS (
		    SELECT candidate.*,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.proxy_pattern
		                ELSE candidate.resolved_pattern END AS effective_pattern,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.implementation_address
		                ELSE candidate.resolved_implementation END AS effective_implementation,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.implementation_code_hash
		                ELSE candidate.resolved_implementation_hash END AS effective_implementation_hash
		    FROM resolved_candidates AS candidate
		    ORDER BY (candidate.artifact_resolution_id IS NOT NULL) DESC,
		             candidate.block_number DESC,
		             candidate.observation_generation_id DESC
		    LIMIT 1
		), published_beacon AS (
		    SELECT observation.implementation_address,
		           observation.implementation_code_hash
		    FROM selected_proxy AS proxy
		    JOIN beacon_implementation_observations AS observation
		      ON observation.chain_id = proxy.chain_id
		     AND observation.beacon_address = proxy.resolved_beacon
		     AND observation.beacon_code_hash = proxy.resolved_beacon_hash
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = observation.chain_id
		     AND canonical.number = observation.block_number
		     AND canonical.block_hash = observation.block_hash
		    JOIN beacon_observation_generations AS generation
		      ON generation.chain_id = observation.chain_id
		     AND generation.beacon_address = observation.beacon_address
		     AND generation.observation_block_hash = observation.block_hash
		     AND generation.observation_stage_version = observation.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = generation.chain_id
		     AND published.block_hash = generation.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = generation.observation_stage_version
		     AND published.durable_job_id = generation.durable_job_id
		     AND published.job_generation = generation.job_generation
		     AND published.state = 'complete'
		    WHERE proxy.effective_pattern = 'beacon'
		      AND observation.stage_version = $6
		      AND observation.canonical
		      AND observation.confidence IN ('verified', 'high')
		      AND observation.block_number <= $4::numeric
		    ORDER BY observation.block_number DESC, generation.id DESC
		    LIMIT 1
		)
		SELECT CASE WHEN proxy.effective_pattern = 'beacon'
		                   THEN beacon.implementation_address
		                   ELSE proxy.effective_implementation END,
		       CASE WHEN proxy.effective_pattern = 'beacon'
		                   THEN beacon.implementation_code_hash
		                   ELSE proxy.effective_implementation_hash END
		FROM selected_proxy AS proxy
		LEFT JOIN published_beacon AS beacon
		  ON proxy.effective_pattern = 'beacon'
		WHERE proxy.effective_pattern <> 'beacon'
		   OR beacon.implementation_address IS NOT NULL`,
		target.ChainID, target.Address[:], target.CodeHash[:],
		strconv.FormatUint(target.BlockNumber, 10), target.BlockHash[:], ProxyStage.Version,
	).Scan(
		&implementationAddressBytes, &implementationCodeHashBytes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return persistedABIBinding{}, false, nil
	}
	if err != nil {
		return persistedABIBinding{}, false, fmt.Errorf("query historical proxy implementation: %w", err)
	}
	if len(implementationAddressBytes) != common.AddressLength {
		return persistedABIBinding{}, false, Permanent(errors.New("stored proxy implementation address is invalid"))
	}
	implementationAddress := common.BytesToAddress(implementationAddressBytes)
	implementationCodeHash, err := WordFromBytes(implementationCodeHashBytes)
	if err != nil || implementationCodeHash == (common.Hash{}) {
		return persistedABIBinding{}, false, Permanent(errors.New("stored proxy implementation code hash is invalid"))
	}
	// A proxy implementation is a state observation, not a property of the
	// proxy bytecode. Keep the ABI binding block-exact so a gap in proxy@2
	// coverage can never make it span an implementation change that was not
	// observed. Beacon implementations are likewise selected as of this block.
	to := target.BlockNumber
	proxyRange := abiBlockRange{from: target.BlockNumber, to: &to}
	baseRange, ok := intersectABIRanges(codeRange, proxyRange)
	if !ok {
		return persistedABIBinding{}, false, Permanent(errors.New("proxy and code ABI ranges do not intersect"))
	}
	return loadVerifiedABIBinding(
		ctx, tx, target, implementationAddress, implementationCodeHash, baseRange, ABISourceProxyImplementation,
	)
}

type abiIdentifier struct {
	kind       ABIKind
	identifier string
	bytes      []byte
}

func loadSignatureABIBinding(
	ctx context.Context,
	tx *sql.Tx,
	identity ABIIdentity,
	codeRange abiBlockRange,
	observations []abiObservation,
	limits DecodeLimits,
) (persistedABIBinding, int, error) {
	identifiers := observedABIIdentifiers(observations)
	entries := make([]json.RawMessage, 0)
	seen := make(map[string]struct{})
	invalid := 0
	totalBytes := 2
	for _, identifier := range identifiers {
		if len(entries) >= limits.MaxEntries {
			break
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT signature, abi_entry
			FROM abi_signature_candidates
			WHERE kind = $1 AND identifier = $2
			  AND octet_length(signature) <= $3
			  AND octet_length(abi_entry::text) <= $4
			ORDER BY signature
			LIMIT $5`, string(identifier.kind), identifier.bytes,
			limits.MaxSignatureBytes, limits.MaxDocumentBytes-2, abiSignatureCandidatesPerID)
		if err != nil {
			return persistedABIBinding{}, invalid, fmt.Errorf("query ABI signature candidates: %w", err)
		}
		for rows.Next() {
			var signature string
			var entry []byte
			if err := rows.Scan(&signature, &entry); err != nil {
				_ = rows.Close()
				return persistedABIBinding{}, invalid, fmt.Errorf("scan ABI signature candidate: %w", err)
			}
			if _, duplicate := seen[string(identifier.kind)+"\x00"+signature]; duplicate {
				continue
			}
			if !validSignatureCandidate(identifier, signature, entry, limits) {
				invalid++
				continue
			}
			if len(entries) >= limits.MaxEntries || totalBytes+len(entry)+1 > limits.MaxDocumentBytes {
				invalid++
				continue
			}
			seen[string(identifier.kind)+"\x00"+signature] = struct{}{}
			entries = append(entries, append(json.RawMessage(nil), entry...))
			totalBytes += len(entry) + 1
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return persistedABIBinding{}, invalid, fmt.Errorf("iterate ABI signature candidates: %w", err)
		}
		if err := rows.Close(); err != nil {
			return persistedABIBinding{}, invalid, fmt.Errorf("close ABI signature candidates: %w", err)
		}
	}
	if len(entries) == 0 {
		return persistedABIBinding{}, invalid, nil
	}
	abi, err := json.Marshal(entries)
	if err != nil {
		return persistedABIBinding{}, invalid, fmt.Errorf("encode ABI signature candidates: %w", err)
	}
	binding := ABIBinding{
		Identity: identity, Source: ABISourceSignatureDatabase,
		SourceAddress: identity.Address, SourceCodeHash: identity.CodeHash,
		ValidFromBlock: codeRange.from, ValidToBlock: codeRange.to,
	}
	return persistedABIBinding{binding: binding, abi: abi}, invalid, nil
}

func validSignatureCandidate(identifier abiIdentifier, signature string, entry []byte, limits DecodeLimits) bool {
	if identifier.kind == ABIKindError && len(identifier.bytes) == 4 {
		var selector [4]byte
		copy(selector[:], identifier.bytes)
		if isBuiltinErrorSelector(selector) {
			return false
		}
	}
	wrapper := make([]byte, 0, len(entry)+2)
	wrapper = append(wrapper, '[')
	wrapper = append(wrapper, entry...)
	wrapper = append(wrapper, ']')
	parsed, err := parseABIEntries(wrapper, ABISourceSignatureDatabase, limits)
	if err != nil || len(parsed) != 1 || parsed[0].kind != identifier.kind || parsed[0].signature != signature {
		return false
	}
	if identifier.kind == ABIKindEvent {
		return parsed[0].topic.String() == identifier.identifier
	}
	return "0x"+fmt.Sprintf("%x", parsed[0].selector[:]) == identifier.identifier
}

func observedABIIdentifiers(observations []abiObservation) []abiIdentifier {
	seen := make(map[string]struct{})
	result := make([]abiIdentifier, 0)
	for _, observation := range observations {
		var kind ABIKind
		var value []byte
		switch observation.objectKind {
		case abiObjectTransactionCalldata, abiObjectTraceCalldata:
			kind, value = ABIKindFunction, observation.input
		case abiObjectTraceRevert:
			kind, value = ABIKindError, observation.input
		case abiObjectLog:
			kind = ABIKindEvent
			if len(observation.topics) > 0 {
				value = observation.topics[0][:]
			}
		}
		want := 4
		if kind == ABIKindEvent {
			want = 32
		}
		if len(value) < want {
			continue
		}
		value = append([]byte(nil), value[:want]...)
		if kind == ABIKindError {
			var selector [4]byte
			copy(selector[:], value)
			if isBuiltinErrorSelector(selector) {
				continue
			}
		}
		identifier := "0x" + fmt.Sprintf("%x", value)
		key := string(kind) + "\x00" + identifier
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, abiIdentifier{kind: kind, identifier: identifier, bytes: value})
	}
	return result
}

func scanABIRange(fromText string, to sql.NullString) (abiBlockRange, error) {
	from, err := strconv.ParseUint(fromText, 10, 64)
	if err != nil {
		return abiBlockRange{}, err
	}
	result := abiBlockRange{from: from}
	if to.Valid {
		value, err := strconv.ParseUint(to.String, 10, 64)
		if err != nil || value < from {
			return abiBlockRange{}, errors.New("invalid ABI range end")
		}
		result.to = &value
	}
	return result, nil
}

func intersectABIRanges(left, right abiBlockRange) (abiBlockRange, bool) {
	result := abiBlockRange{from: left.from}
	if right.from > result.from {
		result.from = right.from
	}
	result.to = minOptionalUint64(left.to, right.to)
	return result, result.to == nil || result.from <= *result.to
}

func minOptionalUint64(left, right *uint64) *uint64 {
	if left == nil && right == nil {
		return nil
	}
	value := uint64(math.MaxUint64)
	if left != nil && *left < value {
		value = *left
	}
	if right != nil && *right < value {
		value = *right
	}
	return &value
}

func rangeContains(value abiBlockRange, block uint64) bool {
	return block >= value.from && (value.to == nil || block <= *value.to)
}

func persistABIBinding(ctx context.Context, tx *sql.Tx, candidate persistedABIBinding) error {
	if err := candidate.binding.validate(); err != nil {
		return Permanent(err)
	}
	var validTo any
	if candidate.binding.ValidToBlock != nil {
		validTo = strconv.FormatUint(*candidate.binding.ValidToBlock, 10)
	}
	identity := candidate.binding.Identity
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO contract_abis (
			chain_id, address, code_hash, source, confidence, abi,
			valid_from_block, valid_to_block, block_number, block_hash,
			source_address, source_code_hash, canonical
		) VALUES (
			$1::numeric, $2, $3, $4, $5, $6::jsonb,
			$7::numeric, $8::numeric, $9::numeric, $10, $11, $12, TRUE
		)
		ON CONFLICT (chain_id, address, code_hash, source, valid_from_block, block_hash)
		DO UPDATE SET
			confidence = EXCLUDED.confidence,
			abi = EXCLUDED.abi,
			valid_to_block = EXCLUDED.valid_to_block,
			block_number = EXCLUDED.block_number,
			source_address = EXCLUDED.source_address,
			source_code_hash = EXCLUDED.source_code_hash,
			canonical = TRUE`,
		identity.ChainID, identity.Address[:], identity.CodeHash[:], candidate.binding.Source,
		candidate.binding.Source.confidence(), candidate.abi,
		strconv.FormatUint(candidate.binding.ValidFromBlock, 10), validTo,
		strconv.FormatUint(identity.BlockNumber, 10), identity.BlockHash[:],
		candidate.binding.SourceAddress[:], candidate.binding.SourceCodeHash[:],
	); err != nil {
		return fmt.Errorf("persist ABI binding: %w", err)
	}
	return nil
}

type decodedABIObservation struct {
	result       DecodeResult
	returnStatus ReturnStatus
	returns      []DecodedArgument
}

func decodeABIObservation(registry *ABIRegistry, identity ABIIdentity, observation abiObservation) decodedABIObservation {
	switch observation.objectKind {
	case abiObjectTransactionCalldata:
		return decodedABIObservation{
			result: registry.DecodeCalldata(identity, observation.input), returnStatus: ReturnNotApplicable,
		}
	case abiObjectTraceCalldata:
		decoded := registry.DecodeCall(identity, observation.input, observation.output, observation.directReverted)
		if decoded.Warning != "" {
			if decoded.Input.Warning == "" {
				decoded.Input.Warning = decoded.Warning
			} else {
				decoded.Input.Warning += "; " + decoded.Warning
			}
		}
		return decodedABIObservation{result: decoded.Input, returnStatus: decoded.ReturnStatus, returns: decoded.Returns}
	case abiObjectTraceConstructor:
		if observation.malformed != "" {
			return decodedABIObservation{result: DecodeResult{
				Status: DecodeMalformed, Kind: ABIKindConstructor, Warning: observation.malformed,
			}, returnStatus: ReturnNotApplicable}
		}
		return decodedABIObservation{
			result: registry.DecodeConstructor(identity, observation.input), returnStatus: ReturnNotApplicable,
		}
	case abiObjectTraceRevert:
		return decodedABIObservation{result: registry.DecodeRevert(identity, observation.input), returnStatus: ReturnNotApplicable}
	case abiObjectLog:
		return decodedABIObservation{result: registry.DecodeLog(identity, observation.topics, observation.data), returnStatus: ReturnNotApplicable}
	default:
		return decodedABIObservation{
			result:       DecodeResult{Status: DecodeUnknown, Warning: "unsupported ABI observation kind"},
			returnStatus: ReturnNotApplicable,
		}
	}
}

func persistABIDecoding(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	identity ABIIdentity,
	observation abiObservation,
	decoded decodedABIObservation,
) error {
	result := decoded.result
	arguments, err := json.Marshal(result.Arguments)
	if err != nil {
		return Permanent(fmt.Errorf("encode decoded ABI arguments: %w", err))
	}
	candidates, err := json.Marshal(result.Candidates)
	if err != nil {
		return Permanent(fmt.Errorf("encode decoded ABI candidates: %w", err))
	}
	returnValues := decoded.returns
	if returnValues == nil {
		returnValues = []DecodedArgument{}
	}
	returnArguments, err := json.Marshal(returnValues)
	if err != nil {
		return Permanent(fmt.Errorf("encode decoded ABI return arguments: %w", err))
	}
	var signature, source, confidence any
	if result.Signature != "" {
		signature = result.Signature
	}
	if result.Source != "" {
		source = result.Source
		confidence = result.Confidence
	}
	var sourceAddress, sourceCodeHash any
	if result.SourceAddress != (common.Address{}) && result.SourceCodeHash != (common.Hash{}) {
		sourceAddress = result.SourceAddress[:]
		sourceCodeHash = result.SourceCodeHash[:]
	}
	result.Warning = truncateUTF8Bytes(result.Warning, 4096)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO abi_decodings (
			chain_id, block_number, block_hash, object_kind, transaction_hash,
			object_index, target_address, target_code_hash, abi_kind, status,
			signature, source, confidence, source_address, source_code_hash,
			arguments, candidates, warning, return_status, return_arguments,
			decoding_kind, canonical
		) VALUES (
			$1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16::jsonb, $17::jsonb, $18,
			$19, $20::jsonb, $21, TRUE
		)`, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		observation.objectKind, observation.transactionHash[:], observation.objectIndex,
		identity.Address[:], identity.CodeHash[:], result.Kind, result.Status,
		signature, source, confidence, sourceAddress, sourceCodeHash,
		arguments, candidates, result.Warning, decoded.returnStatus, returnArguments, result.Kind,
	); err != nil {
		return fmt.Errorf("persist ABI decoding: %w", err)
	}
	return nil
}

// truncateUTF8Bytes bounds text for PostgreSQL without cutting a multi-byte
// rune or forwarding invalid UTF-8 to a TEXT column. The replacement is
// applied before measuring because it can expand an invalid input byte.
func truncateUTF8Bytes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maximum {
		return value
	}
	cut := maximum
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
