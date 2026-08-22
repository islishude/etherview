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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (processor *PostgresProxyProcessor) loadCandidates(
	ctx context.Context,
	job Job,
) ([]proxyCandidate, []uupsImplementationProbeTarget, proxyBlockEvents, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, dbgen.EnrichLegacyProxyCanonical, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]).Scan(&canonical); err != nil {
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
		if candidate.hasSource(proxySourceVerification) {
			verifiedLoupe, err := processor.hasVerifiedDiamondLoupeABI(ctx, job, candidate.address)
			if err != nil {
				return nil, nil, proxyBlockEvents{}, false, err
			}
			if verifiedLoupe {
				candidate.add(proxySourceVerifiedDiamondLoupe, true)
			}
		}
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadGenesisCandidatesStatement1, job.ChainID, job.BlockHash[:])
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadTransactionCandidatesStatement1, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadReceiptCandidatesStatement1, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadLogCandidatesStatement1, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
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
		if topic == proxyDiamondCutTopic && processor.options.DiamondEnabled {
			if err := add(address, proxySourceDiamondCut, true); err != nil {
				return proxyBlockEvents{}, err
			}
			record, valid := parseStrictDiamondCutEvent(wire)
			if !valid {
				events.rejected++
				continue
			}
			record.index = uint64(index)
			record.hash = hash
			record.diamond = address
			events.diamondCuts = append(events.diamondCuts, record)
			continue
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadTraceCandidatesStatement1, job.ChainID,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		TraceStage.Name, TraceStage.Version)
	if err != nil {
		return fmt.Errorf("query proxy trace targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var callType string
		var fromBytes, toBytes, createdBytes []byte
		var reverted bool
		if err := rows.Scan(&callType, &fromBytes, &toBytes, &createdBytes, &reverted); err != nil {
			return fmt.Errorf("scan proxy trace target: %w", err)
		}
		if processor.options.DiamondEnabled && !reverted && callType == "DELEGATECALL" && len(fromBytes) != 0 {
			if len(fromBytes) != common.AddressLength {
				return Permanent(errors.New("stored proxy trace caller is invalid"))
			}
			if err := add(common.BytesToAddress(fromBytes), proxySourceDelegatecallRouter, true); err != nil {
				return err
			}
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichInlineLoadStateDiffCandidatesStatement1, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
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
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichLegacyProxyReplayCandidates, job.ChainID, strconv.FormatUint(job.BlockNumber, 10),
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
	err := processor.db.QueryRowContext(ctx, dbgen.EnrichInlineLoadProxyArtifactStatement1, job.ChainID, address[:], hash[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(
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

func (processor *PostgresProxyProcessor) hasVerifiedDiamondLoupeABI(
	ctx context.Context,
	job Job,
	address common.Address,
) (bool, error) {
	var found bool
	err := processor.db.QueryRowContext(ctx, dbgen.EnrichInlineHasVerifiedDiamondLoupeABIStatement1, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("query verified Diamond Loupe ABI: %w", err)
	}
	return found, nil
}

func (processor *PostgresProxyProcessor) authenticateCloneCreation(
	ctx context.Context,
	job Job,
	address common.Address,
	runtime []byte,
) (bool, error) {
	var input, output []byte
	err := processor.db.QueryRowContext(ctx, dbgen.EnrichInlineAuthenticateCloneCreationStatement1, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10),
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
	err := processor.db.QueryRowContext(ctx, dbgen.EnrichInlineProxyOrBeaconHistoryStatement1, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&proxy, &beacon)
	if err != nil {
		return false, false, fmt.Errorf("query canonical proxy or beacon history: %w", err)
	}
	return proxy, beacon, nil
}

func (processor *PostgresProxyProcessor) hasCanonicalCodeHistory(ctx context.Context, job Job, address common.Address) (bool, error) {
	var exists bool
	if err := processor.db.QueryRowContext(ctx, dbgen.EnrichInlineHasCanonicalCodeHistoryStatement1, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&exists); err != nil {
		return false, fmt.Errorf("query canonical code history: %w", err)
	}
	return exists, nil
}
