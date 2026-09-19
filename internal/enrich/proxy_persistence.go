package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (processor *PostgresProxyProcessor) persist(
	ctx context.Context,
	job Job,
	detections []proxyDetection,
	beacons []beaconDetection,
	uupsProbes []uupsImplementationProbeResult,
	events proxyBlockEvents,
	outcome string,
) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
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
	tx pgx.Tx,
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
	for _, event := range events.diamondCuts {
		if err := persistDiamondCutRecord(ctx, tx, job, event); err != nil {
			return StageResult{}, err
		}
	}
	for index := range detections {
		if err := processor.reconcileDiamondHistory(ctx, tx, job, &detections[index]); err != nil {
			return StageResult{}, err
		}
	}
	collected, err := collectProxyObservations(detections, beacons, uupsProbes)
	if err != nil {
		return StageResult{}, err
	}
	codeObservations := collected.code
	beaconObservations := collected.beacons
	proxyCount := collected.proxies
	rejectedCount := collected.rejected
	v2DifferenceCount := collected.v2Differences
	uupsCompatibleCount := collected.uupsCompatible
	uupsRejectedCount := collected.uupsRejected
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
		if detection.v2Active {
			if err := processor.persistProxyDetectionV2(ctx, tx, job, detection); err != nil {
				return StageResult{}, err
			}
			if err := persistDiamondDetectionSnapshots(ctx, tx, job, detection.v2); err != nil {
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
	holderRequeued := false
	if job.Generation > 1 {
		abiRequeued, err = resetTerminalDependentStageTx(ctx, tx, job, ABIStage)
		if err != nil {
			return StageResult{}, err
		}
	}
	if job.Generation > 1 && (len(events.upgrades) > 0 || len(codeObservations) > 0) {
		holderRequeued, err = resetTerminalDependentStageTx(ctx, tx, job, HolderStage)
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
		"beacons":                        strconv.Itoa(len(beaconObservations)),
		"uups_probes":                    strconv.Itoa(len(uupsProbes)),
		"uups_compatible":                strconv.Itoa(uupsCompatibleCount),
		"uups_rejected":                  strconv.Itoa(uupsRejectedCount),
		"upgrade_events":                 strconv.Itoa(len(events.upgrades)),
		"initialization_events":          strconv.Itoa(len(events.initializations)),
		"diamond_cut_events":             strconv.Itoa(len(events.diamondCuts)),
		"rejected_events":                strconv.Itoa(events.rejected),
		"rejected_candidates":            strconv.Itoa(rejectedCount),
		"proxy_detection_v2_differences": strconv.Itoa(v2DifferenceCount),
		"carried_proxies":                strconv.FormatInt(carried.proxies, 10),
		"carried_beacons":                strconv.FormatInt(carried.beacons, 10),
		"carried_uups":                   strconv.FormatInt(carried.uups, 10),
		"carried_resolutions":            strconv.FormatInt(carried.resolutions, 10),
		"carried_negative_evidence": strconv.FormatInt(
			carried.negativeEvidence, 10,
		),
		"abi_requeued":    strconv.FormatBool(abiRequeued),
		"holder_requeued": strconv.FormatBool(holderRequeued),
	}
	maps.Copy(details, coverageDetails)
	return StageResult{State: ResultComplete, Details: details}, nil
}

func carryForwardProxyGeneration(
	ctx context.Context,
	tx pgx.Tx,
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		if job.Stage.Version > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).EnrichLegacyCarryForwardProxyGeneration(ctx, dbgen.EnrichLegacyCarryForwardProxyGenerationParams{DurableJobID: jobID, JobGeneration: generation, ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version)})
		if err != nil {
			return err
		}
		carried.proxies = queryRow.Count
		carried.beacons = queryRow.Count_2
		carried.uups = queryRow.Count_3
		carried.resolutions = queryRow.Count_4
		carried.negativeEvidence = queryRow.Count_5
		return nil
	}()
	if err != nil {
		return proxyCarryForwardCounts{}, fmt.Errorf("carry forward proxy generation evidence: %w", err)
	}
	return carried, nil
}

func loadProxyCoverageDetails(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
) (map[string]string, error) {
	details := map[string]string{
		"history_coverage":    "event_only",
		"trace_coverage":      "missing",
		"state_diff_coverage": "missing",
	}
	rows, err := func() ([]dbgen.EnrichInlineLoadProxyCoverageDetailsStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		if TraceStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if StateDiffStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichInlineLoadProxyCoverageDetailsStatement1(ctx, dbgen.EnrichInlineLoadProxyCoverageDetailsStatement1Params{ChainID: queryValue0, BlockHash: job.BlockHash[:], Stage: TraceStage.Name, StageVersion: int32(TraceStage.Version), Stage2: StateDiffStage.Name, StageVersion2: int32(StateDiffStage.Version)})
	}()
	if err != nil {
		return nil, fmt.Errorf("query proxy coverage witnesses: %w", err)
	}

	for _, storedRow := range rows {
		var stage, state string
		var durableJobID, generation pgtype.Int8
		if err := func() error {
			stage = storedRow.Stage
			if !storedRow.State.Valid {
				return errors.New("invalid stored query value")
			}
			state = storedRow.State.String
			var queryValue4 pgtype.Int8
			if storedRow.DurableJobID != nil {
				queryValue4 = pgtype.Int8{Int64: *storedRow.DurableJobID, Valid: true}
			}
			durableJobID = queryValue4
			var queryValue6 pgtype.Int8
			if storedRow.JobGeneration != nil {
				queryValue6 = pgtype.Int8{Int64: *storedRow.JobGeneration, Valid: true}
			}
			generation = queryValue6
			return nil
		}(); err != nil {
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
	tx pgx.Tx,
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
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyDetectionEvidence(ctx, dbgen.EnrichLegacyUpsertProxyDetectionEvidenceParams{ChainID: queryValue0, Address: candidate.address[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), CodeHash: codeHash[:], CandidateKind: candidateKind, DetectionState: state, Reason: reason, DurableJobID: jobID, JobGeneration: generation, Details: []byte(string(details))})
	}()
	if err != nil {
		return fmt.Errorf("persist proxy detection evidence: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing proxy detection evidence conflicts with RPC state"))
	}
	return nil
}

func (processor *PostgresProxyProcessor) persistProxyDetectionV2(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	detection proxyDetection,
) error {
	details, err := marshalProxyDetectionResolution(detection.v2)
	if err != nil {
		return fmt.Errorf("encode proxy detection V2 resolution: %w", err)
	}
	if len(details) > processor.limits.MaxDetailsBytes {
		return Permanent(errors.New("proxy detection V2 resolution exceeds configured limit"))
	}
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	state := strings.ReplaceAll(string(detection.v2.Status), "-", "_")
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyDetectionEvidence(ctx, dbgen.EnrichLegacyUpsertProxyDetectionEvidenceParams{ChainID: queryValue0, Address: detection.candidate.address[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), CodeHash: detection.codeHash[:], CandidateKind: "proxy_v2", DetectionState: state, Reason: "resolver", DurableJobID: jobID, JobGeneration: generation, Details: []byte(string(details))})
	}()
	if err != nil {
		return fmt.Errorf("persist proxy detection V2 resolution: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing proxy detection V2 conflicts with RPC state"))
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

func persistProxyCodeObservation(ctx context.Context, tx pgx.Tx, job Job, observation proxyCodeObservation) error {
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyCodeObservation(ctx, dbgen.EnrichLegacyUpsertProxyCodeObservationParams{ChainID: queryValue0, Address: observation.address[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], CodeHash: observation.codeHash[:], Code: observation.code})
	}()
	if err != nil {
		return fmt.Errorf("persist exact contract code observation: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing exact contract code observation conflicts with RPC state"))
	}
	return nil
}

func (processor *PostgresProxyProcessor) persistProxyObservation(ctx context.Context, tx pgx.Tx, job Job, detection proxyDetection) error {
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
	if resolved.kind == ProxyCWIA {
		details["cwia_runtime"] = soladyLegacyCWIAVariant
		details["immutable_args_bytes"] = len(resolved.immutableArgs)
		details["immutable_args_runtime_authenticated"] = true
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
	var admin []byte
	var adminHash []byte
	var beacon []byte
	var beaconHash []byte
	var immutableArgs []byte
	var standardVersion *string
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
		standardVersion = new(resolved.standardVersion)
	}
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyObservation(ctx, dbgen.EnrichLegacyUpsertProxyObservationParams{ChainID: queryValue0, ProxyAddress: detection.candidate.address[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), ProxyCodeHash: detection.codeHash[:], ProxyKind: string(resolved.kind), ProxyPattern: string(resolved.pattern), StandardVersion: standardVersion, ImplementationAddress: resolved.implementation[:], AdminAddress: admin, AdminCodeHash: adminHash, BeaconAddress: beacon, BeaconCodeHash: beaconHash, ImmutableArgs: immutableArgs, ImplementationCodeHash: resolved.implementationHash[:], Confidence: string(ConfidenceHigh), EvidenceState: resolved.evidenceState, Details: []byte(string(encoded))})
	}()
	if err != nil {
		return fmt.Errorf("persist exact proxy observation: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing exact proxy observation conflicts with RPC state"))
	}
	return nil
}

func proxyGenerationSQLIdentity(job Job) (*int64, *int64, error) {
	if job.Generation == 0 {
		return nil, nil, nil
	}
	id, err := strconv.ParseInt(job.ID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != job.ID || job.Generation > math.MaxInt64 {
		return nil, nil, errors.New("proxy generation identity is not a canonical positive BIGINT")
	}
	return new(id), new(int64(job.Generation)), nil
}

func persistProxyObservationGeneration(ctx context.Context, tx pgx.Tx, job Job, address common.Address) error {
	return persistObservationGeneration(ctx, tx, job, address, false)
}

func (processor *PostgresProxyProcessor) persistProxyArtifactResolution(
	ctx context.Context,
	tx pgx.Tx,
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
	var admin []byte
	var adminHash []byte
	var beacon []byte
	var beaconHash []byte
	var implementationArtifact *string
	if exact.admin != nil {
		admin, adminHash = exact.admin[:], exact.adminHash[:]
	}
	if exact.beacon != nil {
		beacon, beaconHash = exact.beacon[:], exact.beaconHash[:]
	}
	if exact.implementationArtifactJob != "" {
		implementationArtifact = new(exact.implementationArtifactJob)
	}
	var resolutionID int64
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		if job.Stage.Version > 2147483647 {
			return errors.New("invalid stored query value")
		}
		var queryValue2 pgtype.UUID
		if err := queryValue2.Scan(exact.proxyArtifactJob); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if implementationArtifact != nil {
			if err := queryValue3.Scan(*implementationArtifact); err != nil {
				return err
			}
		}
		queryRow, err := dbgen.New(tx).EnrichLegacyInsertProxyArtifactResolution(ctx, dbgen.EnrichLegacyInsertProxyArtifactResolutionParams{ChainID: queryValue0, ProxyAddress: detection.candidate.address[:], ObservationBlockHash: job.BlockHash[:], ObservationStageVersion: int32(job.Stage.Version), ProxyCodeHash: detection.codeHash[:], ProxyKind: string(exact.kind), ProxyPattern: string(exact.pattern), StandardVersion: exact.standardVersion, ImplementationAddress: exact.implementation[:], ImplementationCodeHash: exact.implementationHash[:], AdminAddress: admin, AdminCodeHash: adminHash, BeaconAddress: beacon, BeaconCodeHash: beaconHash, ProxyArtifactJobID: queryValue2, ImplementationArtifactJobID: queryValue3, DurableJobID: jobID, JobGeneration: generation, Evidence: []byte(string(encoded))})
		if err != nil {
			return err
		}
		resolutionID = queryRow
		return nil
	}()
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
	tx pgx.Tx,
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
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertBeaconImplementationObservation(ctx, dbgen.EnrichLegacyUpsertBeaconImplementationObservationParams{ChainID: queryValue0, BeaconAddress: observation.address[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], BeaconCodeHash: observation.codeHash[:], ImplementationAddress: observation.implementation[:], ImplementationCodeHash: observation.implementationHash[:], StageVersion: int32(job.Stage.Version), Confidence: string(ConfidenceHigh), Details: []byte(string(details))})
	}()
	if err != nil {
		return fmt.Errorf("persist exact beacon implementation observation: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing exact beacon implementation observation conflicts with RPC state"))
	}
	return nil
}

func persistBeaconObservationGeneration(ctx context.Context, tx pgx.Tx, job Job, address common.Address) error {
	return persistObservationGeneration(ctx, tx, job, address, true)
}

func persistProxyUpgradeEvent(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	event proxyUpgradeEvent,
) error {
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		queryValue2, err := strconv.ParseInt(strconv.FormatUint(event.index, 10), 10, 64)
		if err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyUpgradeEvent(ctx, dbgen.EnrichLegacyUpsertProxyUpgradeEventParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], LogIndex: int64(queryValue2), TransactionHash: event.hash[:], EmitterAddress: event.emitter[:], EventKind: event.kind, TargetAddress: event.target[:], StageVersion: int32(job.Stage.Version)})
	}()
	if err != nil {
		return fmt.Errorf("persist strict proxy upgrade event: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing strict proxy upgrade event conflicts with indexed log"))
	}
	return nil
}

func persistProxyInitializationEvent(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	event proxyInitializationEvent,
) error {
	result, err := func() (int64, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return 0, err
		}
		queryValue2, err := strconv.ParseInt(strconv.FormatUint(event.index, 10), 10, 64)
		if err != nil {
			return 0, err
		}
		var queryValue3 pgtype.Numeric
		if err := queryValue3.Scan(strconv.FormatUint(event.version, 10)); err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichLegacyUpsertProxyInitializationEvent(ctx, dbgen.EnrichLegacyUpsertProxyInitializationEventParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], LogIndex: int64(queryValue2), TransactionHash: event.hash[:], ContractAddress: event.address[:], Version: queryValue3, StageVersion: int32(job.Stage.Version)})
	}()
	if err != nil {
		return fmt.Errorf("persist strict proxy initialization event: %w", err)
	}
	affected := result
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
func resetTerminalDependentStageTx(ctx context.Context, tx pgx.Tx, job Job, dependent StageID) (bool, error) {
	requested, err := requestDependentStageReplayTx(ctx, tx, job, dependent)
	if err != nil {
		return false, fmt.Errorf("request dependent stage replay %s: %w", dependent, err)
	}
	return requested, nil
}

func persistObservationGeneration(ctx context.Context, tx pgx.Tx, job Job, address common.Address, beacon bool) error {
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	var chain pgtype.Numeric
	if err := chain.Scan(job.ChainID); err != nil {
		return err
	}
	if job.Stage.Version > math.MaxInt32 {
		return Permanent(errors.New("observation stage version exceeds PostgreSQL INTEGER"))
	}
	queries := dbgen.New(tx)
	kind := "proxy"
	var affected int64
	if beacon {
		kind = "beacon"
		affected, err = queries.EnrichLegacyInsertBeaconObservationGeneration(ctx, dbgen.EnrichLegacyInsertBeaconObservationGenerationParams{ChainID: chain, BeaconAddress: address[:], ObservationBlockHash: job.BlockHash[:], ObservationStageVersion: int32(job.Stage.Version), DurableJobID: jobID, JobGeneration: generation})
	} else {
		affected, err = queries.EnrichLegacyInsertProxyObservationGeneration(ctx, dbgen.EnrichLegacyInsertProxyObservationGenerationParams{ChainID: chain, ProxyAddress: address[:], ObservationBlockHash: job.BlockHash[:], ObservationStageVersion: int32(job.Stage.Version), DurableJobID: jobID, JobGeneration: generation})
	}
	if err != nil {
		return fmt.Errorf("persist %s observation generation: %w", kind, err)
	}
	if affected > 1 {
		return Permanent(fmt.Errorf("%s observation generation affected multiple rows", kind))
	}
	return nil
}
