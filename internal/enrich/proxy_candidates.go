package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"uuid"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (processor *PostgresProxyProcessor) loadCandidates(
	ctx context.Context,
	job Job,
) ([]proxyCandidate, []uupsImplementationProbeTarget, proxyBlockEvents, bool, error) {
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).EnrichLegacyProxyCanonical(ctx, queryValue0, queryValue1, job.BlockHash[:])
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
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
	return dbaccess.WithTransactionOptions(ctx, processor.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(queries *dbgen.Queries) error {
		cursor := dbgen.EnrichInlineLoadGenesisCandidatesStatement1Params{ChainID: job.ChainID, BlockHash: job.BlockHash[:], AfterAddress: make([]byte, common.AddressLength), PageLimit: 512}
		for {
			rows, err := queries.EnrichInlineLoadGenesisCandidatesStatement1(ctx, cursor)
			if err != nil {
				return fmt.Errorf("query genesis proxy candidates: %w", err)
			}
			for _, encoded := range rows {
				if len(encoded) != common.AddressLength {
					return Permanent(errors.New("stored genesis address is invalid"))
				}
				if err := add(common.BytesToAddress(encoded), proxySourceGenesis, true); err != nil {
					return err
				}
			}
			if len(rows) < int(cursor.PageLimit) {
				return nil
			}
			cursor.HasCursor = true
			cursor.AfterAddress = rows[len(rows)-1]
		}
	})
}

func (processor *PostgresProxyProcessor) loadTransactionCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := func() ([]dbgen.EnrichInlineLoadTransactionCandidatesStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		return dbgen.New(processor.db).EnrichInlineLoadTransactionCandidatesStatement1(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}()
	if err != nil {
		return fmt.Errorf("query proxy transaction targets: %w", err)
	}

	for _, storedRow := range rows {
		var hashBytes, raw []byte
		{
			hashBytes = storedRow.TxHash
			raw = storedRow.Raw
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

	return nil
}

func (processor *PostgresProxyProcessor) loadReceiptCandidates(ctx context.Context, job Job, add proxyCandidateAdder) error {
	rows, err := func() ([]dbgen.EnrichInlineLoadReceiptCandidatesStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		return dbgen.New(processor.db).EnrichInlineLoadReceiptCandidatesStatement1(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}()
	if err != nil {
		return fmt.Errorf("query proxy creation receipts: %w", err)
	}

	for _, storedRow := range rows {
		var index int64
		var hashBytes, raw []byte
		{
			index = storedRow.TxIndex
			hashBytes = storedRow.TxHash
			raw = storedRow.Raw
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
	rows, err := func() ([]dbgen.EnrichInlineLoadLogCandidatesStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		return dbgen.New(processor.db).EnrichInlineLoadLogCandidatesStatement1(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}()
	if err != nil {
		return proxyBlockEvents{}, fmt.Errorf("query proxy log targets: %w", err)
	}

	var events proxyBlockEvents
	for _, storedRow := range rows {
		var index int64
		var hashBytes, addressBytes, topicBytes, raw []byte
		{
			index = storedRow.LogIndex
			hashBytes = storedRow.TxHash
			addressBytes = storedRow.Address
			topicBytes = storedRow.Topic0
			raw = storedRow.Raw
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
	rows, err := func() ([]dbgen.EnrichInlineLoadTraceCandidatesStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		if TraceStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(processor.db).EnrichInlineLoadTraceCandidatesStatement1(ctx, dbgen.EnrichInlineLoadTraceCandidatesStatement1Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], Stage: TraceStage.Name, StageVersion: int32(TraceStage.Version)})
	}()
	if err != nil {
		return fmt.Errorf("query proxy trace targets: %w", err)
	}

	for _, storedRow := range rows {
		var callType string
		var fromBytes, toBytes, createdBytes []byte
		var reverted bool
		{
			callType = storedRow.CallType
			fromBytes = storedRow.FromAddress
			toBytes = storedRow.ToAddress
			createdBytes = storedRow.CreatedAddress
			reverted = storedRow.Reverted
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

	return nil
}

func (processor *PostgresProxyProcessor) loadStateDiffCandidates(
	ctx context.Context,
	job Job,
	add proxyCandidateAdder,
) error {
	rows, err := func() ([][]byte, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		if StateDiffStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(processor.db).EnrichInlineLoadStateDiffCandidatesStatement1(ctx, dbgen.EnrichInlineLoadStateDiffCandidatesStatement1Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StorageKey: EIP1967ImplementationSlot[:], StorageKey2: EIP1967BeaconSlot[:], StorageKey3: EIP1967AdminSlot[:], Stage: StateDiffStage.Name, StageVersion: int32(StateDiffStage.Version)})
	}()
	if err != nil {
		return fmt.Errorf("query proxy state-difference targets: %w", err)
	}

	for _, storedRow := range rows {
		var addressBytes []byte
		{
			addressBytes = storedRow
		}
		if len(addressBytes) != common.AddressLength {
			return Permanent(errors.New("stored proxy state-difference target is invalid"))
		}
		if err := add(common.BytesToAddress(addressBytes), proxySourceStateDiff, true); err != nil {
			return err
		}
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
	rows, err := func() ([]dbgen.EnrichLegacyProxyReplayCandidatesRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		if job.Stage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		queryValue3, err := strconv.ParseInt(job.ID, 10, 64)
		if err != nil {
			return nil, err
		}
		queryValue4, err := strconv.ParseInt(strconv.FormatUint(job.Generation, 10), 10, 64)
		if err != nil {
			return nil, err
		}
		return dbgen.New(processor.db).EnrichLegacyProxyReplayCandidates(ctx, dbgen.EnrichLegacyProxyReplayCandidatesParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), Source: proxySourceVerification, JobID: int64(queryValue3), ClaimedGeneration: int64(queryValue4)})
	}()
	if err != nil {
		return nil, fmt.Errorf("query exact proxy replay targets: %w", err)
	}

	uupsTargets := make([]uupsImplementationProbeTarget, 0)
	for _, storedRow := range rows {
		var addressBytes []byte
		var targetKind, source string
		var artifactCodeHash []byte
		var artifactVerificationJob pgtype.UUID
		{
			addressBytes = storedRow.Address
			targetKind = storedRow.TargetKind
			source = storedRow.Source
			artifactCodeHash = storedRow.CodeHash
			artifactVerificationJob = storedRow.VerificationJobID
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
				verificationJobID: uuid.UUID(artifactVerificationJob.Bytes).String(),
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).EnrichInlineLoadProxyArtifactStatement1(ctx, dbgen.EnrichInlineLoadProxyArtifactStatement1Params{ChainID: queryValue0, Address: address[:], CodeHash: hash[:], MaxValidFromBlock: queryValue1})
		if err != nil {
			return err
		}
		artifact.kind = queryRow.ArtifactKind
		artifact.standardVersion = queryRow.StandardVersion
		immutable = queryRow.RuntimeImmutableAddress
		artifact.verificationJob = queryRow.ArtifactVerificationJobID
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).EnrichInlineHasVerifiedDiamondLoupeABIStatement1(ctx, queryValue0, address[:], queryValue1)
		if err != nil {
			return err
		}
		found = queryRow
		return nil
	}()
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		if TraceStage.Version > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(processor.db).EnrichInlineAuthenticateCloneCreationStatement1(ctx, dbgen.EnrichInlineAuthenticateCloneCreationStatement1Params{ChainID: queryValue0, CreatedAddress: address[:], MaxBlockNumber: queryValue1, Stage: TraceStage.Name, StageVersion: int32(TraceStage.Version)})
		if err != nil {
			return err
		}
		input = queryRow.Input
		output = queryRow.Output
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).EnrichInlineProxyOrBeaconHistoryStatement1(ctx, queryValue0, address[:], queryValue1)
		if err != nil {
			return err
		}
		proxy = queryRow.Exists
		beacon = queryRow.Exists_2
		return nil
	}()
	if err != nil {
		return false, false, fmt.Errorf("query canonical proxy or beacon history: %w", err)
	}
	return proxy, beacon, nil
}

func (processor *PostgresProxyProcessor) hasCanonicalCodeHistory(ctx context.Context, job Job, address common.Address) (bool, error) {
	var exists bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).EnrichInlineHasCanonicalCodeHistoryStatement1(ctx, queryValue0, address[:], queryValue1)
		if err != nil {
			return err
		}
		exists = queryRow
		return nil
	}(); err != nil {
		return false, fmt.Errorf("query canonical code history: %w", err)
	}
	return exists, nil
}
