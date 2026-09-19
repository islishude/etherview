package enrich

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	pgx "github.com/jackc/pgx/v5"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

var diamondCutEventABI = mustGethABI(`[
  {"type":"event","name":"DiamondCut","anonymous":false,"inputs":[
    {"name":"diamondCut","type":"tuple[]","indexed":false,"components":[
      {"name":"facetAddress","type":"address"},
      {"name":"action","type":"uint8"},
      {"name":"functionSelectors","type":"bytes4[]"}
    ]},
    {"name":"init","type":"address","indexed":false},
    {"name":"calldata","type":"bytes","indexed":false}
  ]}
]`)

type diamondFacetCut struct {
	FacetAddress      common.Address
	Action            uint8
	FunctionSelectors [][4]byte
}

type diamondCutRecord struct {
	index            uint64
	transactionIndex uint64
	hash             common.Hash
	diamond          common.Address
	init             common.Address
	calldata         []byte
	cuts             []diamondFacetCut
}

type diamondCutDocument struct {
	CutIndex  int      `json:"cut_index"`
	Action    uint8    `json:"action"`
	Facet     string   `json:"facet_address"`
	Selectors []string `json:"selectors"`
}

func parseStrictDiamondCutEvent(log types.Log) (diamondCutRecord, bool) {
	if len(log.Topics) != 1 || log.Topics[0] != proxyDiamondCutTopic ||
		len(log.Data) > DiamondMaxRawReturnBytes || uint64(log.TxIndex) > math.MaxInt64 {
		return diamondCutRecord{}, false
	}
	definition, exists := diamondCutEventABI.Events["DiamondCut"]
	if !exists {
		panic("DiamondCut ABI is missing")
	}
	values, err := definition.Inputs.Unpack(log.Data)
	if err != nil || len(values) != 3 {
		return diamondCutRecord{}, false
	}
	reencoded, err := definition.Inputs.Pack(values...)
	if err != nil || !bytes.Equal(reencoded, log.Data) {
		return diamondCutRecord{}, false
	}
	var cuts []diamondFacetCut
	func() {
		defer func() { _ = recover() }()
		converted, ok := gethabi.ConvertType(values[0], new([]diamondFacetCut)).(*[]diamondFacetCut)
		if ok && converted != nil {
			cuts = *converted
		}
	}()
	init, initOK := values[1].(common.Address)
	calldata, calldataOK := values[2].([]byte)
	if cuts == nil || !initOK || !calldataOK || len(cuts) > DiamondMaxFacets {
		return diamondCutRecord{}, false
	}
	selectorCount := 0
	for index := range cuts {
		if cuts[index].Action > 2 || len(cuts[index].FunctionSelectors) == 0 ||
			len(cuts[index].FunctionSelectors) > DiamondMaxSelectorsPerFacet {
			return diamondCutRecord{}, false
		}
		selectorCount += len(cuts[index].FunctionSelectors)
		if selectorCount > DiamondMaxSelectorsTotal {
			return diamondCutRecord{}, false
		}
		cuts[index].FunctionSelectors = append([][4]byte(nil), cuts[index].FunctionSelectors...)
	}
	return diamondCutRecord{
		transactionIndex: uint64(log.TxIndex), init: init,
		// pgx treats a nil []byte as SQL NULL. Preserve canonical empty
		// event bytes as a non-nil zero-length BYTEA value.
		calldata: append([]byte{}, calldata...), cuts: cuts,
	}, true
}

func persistDiamondCutRecord(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	record diamondCutRecord,
) error {
	document := make([]diamondCutDocument, len(record.cuts))
	for cutIndex, cut := range record.cuts {
		document[cutIndex] = diamondCutDocument{
			CutIndex: cutIndex, Action: cut.Action, Facet: cut.FacetAddress.Hex(),
			Selectors: make([]string, len(cut.FunctionSelectors)),
		}
		for selectorIndex, selector := range cut.FunctionSelectors {
			document[cutIndex].Selectors[selectorIndex] = "0x" + hex.EncodeToString(selector[:])
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode DiamondCut record: %w", err)
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
		queryValue2, err := strconv.ParseInt(strconv.FormatUint(record.transactionIndex, 10), 10, 64)
		if err != nil {
			return 0, err
		}
		queryValue3, err := strconv.ParseInt(strconv.FormatUint(record.index, 10), 10, 64)
		if err != nil {
			return 0, err
		}
		if job.Stage.Version > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichInlinePersistDiamondCutRecordStatement1(ctx, dbgen.EnrichInlinePersistDiamondCutRecordStatement1Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], TransactionHash: record.hash[:], TransactionIndex: int64(queryValue2), LogIndex: int64(queryValue3), DiamondAddress: record.diamond[:], InitAddress: record.init[:], InitCalldata: record.calldata, Cuts: []byte(string(encoded)), StageVersion: int32(job.Stage.Version)})
	}()
	if err != nil {
		return fmt.Errorf("persist DiamondCut event: %w", err)
	}
	affected := result
	if affected != 1 {
		return Permanent(errors.New("existing DiamondCut event conflicts with indexed log"))
	}
	for cutIndex, cut := range record.cuts {
		for selectorIndex, selector := range cut.FunctionSelectors {
			result, err = func() (int64, error) {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(job.ChainID); err != nil {
					return 0, err
				}
				queryValue1, err := strconv.ParseInt(strconv.FormatUint(record.index, 10), 10, 64)
				if err != nil {
					return 0, err
				}
				if job.Stage.Version > 2147483647 {
					return 0, errors.New("invalid stored query value")
				}
				if cutIndex < -2147483648 || cutIndex > 2147483647 {
					return 0, errors.New("invalid stored query value")
				}
				if selectorIndex < -2147483648 || selectorIndex > 2147483647 {
					return 0, errors.New("invalid stored query value")
				}
				return dbgen.New(tx).EnrichInlinePersistDiamondCutRecordStatement2(ctx, dbgen.EnrichInlinePersistDiamondCutRecordStatement2Params{ChainID: queryValue0, BlockHash: job.BlockHash[:], LogIndex: int64(queryValue1), StageVersion: int32(job.Stage.Version), CutIndex: int32(cutIndex), SelectorIndex: int32(selectorIndex), Selector: selector[:], Action: int16(cut.Action), FacetAddress: cut.FacetAddress[:]})
			}()
			if err != nil {
				return fmt.Errorf("persist Diamond selector change: %w", err)
			}
			affected = result
			if affected != 1 {
				return Permanent(errors.New("existing Diamond selector change conflicts with indexed log"))
			}
		}
	}
	return nil
}

func (processor *PostgresProxyProcessor) reconcileDiamondHistory(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	detection *proxyDetection,
) error {
	if detection == nil || !detection.v2Active {
		return nil
	}
	outcomes := append([]ProxyDetectionV2(nil), detection.v2.Outcomes...)
	changed := false
	for index := range outcomes {
		outcome := &outcomes[index]
		if outcome.Family != ProxyFamilyERC2535 || outcome.Diamond == nil {
			continue
		}
		if outcome.Diamond.Completeness != DiamondComplete {
			outcome.Warnings = append(outcome.Warnings,
				"DiamondCut history reconciliation requires a complete Loupe snapshot")
			continue
		}
		status, warning, err := loadAndReplayDiamondHistory(
			ctx, tx, job, outcome.Proxy, outcome.Diamond.SelectorToFacet,
		)
		if err != nil {
			return err
		}
		switch status {
		case diamondHistoryUnavailable:
			if warning != "" {
				outcome.Warnings = append(outcome.Warnings, warning)
				changed = true
			}
			continue
		case diamondHistoryConsistent:
			outcome.Evidence = append(outcome.Evidence, ProxyDetectionEvidence{
				Kind:        ProxyEvidenceDiamondCutEvent,
				Description: "canonical DiamondCut history matches the Loupe snapshot",
			})
		case diamondHistoryInconsistent:
			outcome.Status = ProxyStatusInconsistent
			outcome.Confidence = ProxyConfidenceHigh
			outcome.Warnings = append(outcome.Warnings, warning)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	resolved, err := ResolveProxyDetections(outcomes)
	if err != nil {
		return Permanent(fmt.Errorf("resolve Diamond history reconciliation: %w", err))
	}
	compareLegacyProxyProjection(*detection, &resolved)
	detection.v2 = resolved
	return nil
}

type diamondHistoryStatus uint8

const (
	diamondHistoryUnavailable diamondHistoryStatus = iota
	diamondHistoryConsistent
	diamondHistoryInconsistent
)

type diamondSelectorChange struct {
	selector [4]byte
	action   uint8
	facet    common.Address
}

func loadAndReplayDiamondHistory(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	diamond common.Address,
	want map[[4]byte]common.Address,
) (diamondHistoryStatus, string, error) {
	covered, err := diamondHistoryCoverageComplete(ctx, tx, job, diamond)
	if err != nil {
		return diamondHistoryUnavailable, "", err
	}
	if !covered {
		return diamondHistoryUnavailable,
			"DiamondCut history coverage does not begin at the Diamond creation point", nil
	}
	rows, err := func() ([]dbgen.EnrichInlineLoadAndReplayDiamondHistoryStatement1Row, error) {
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
		if DiamondMaxHistoryChanges+1 < -2147483648 || DiamondMaxHistoryChanges+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichInlineLoadAndReplayDiamondHistoryStatement1(ctx, dbgen.EnrichInlineLoadAndReplayDiamondHistoryStatement1Params{ChainID: queryValue0, DiamondAddress: diamond[:], MaxBlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), Limit: int32(DiamondMaxHistoryChanges + 1)})
	}()
	if err != nil {
		return diamondHistoryUnavailable, "", fmt.Errorf("query DiamondCut history: %w", err)
	}

	changes := make([]diamondSelectorChange, 0)
	for _, storedRow := range rows {
		var selectorBytes, facetBytes []byte
		var action int
		{
			selectorBytes = storedRow.Selector
			action = int(storedRow.Action)
			facetBytes = storedRow.FacetAddress
		}
		if len(selectorBytes) != 4 || len(facetBytes) != common.AddressLength ||
			action < 0 || action > 2 {
			return diamondHistoryUnavailable, "", Permanent(errors.New("stored DiamondCut history is invalid"))
		}
		var selector [4]byte
		copy(selector[:], selectorBytes)
		changes = append(changes, diamondSelectorChange{
			selector: selector, action: uint8(action), facet: common.BytesToAddress(facetBytes),
		})
	}

	if len(changes) == 0 {
		return diamondHistoryUnavailable, "", nil
	}
	if len(changes) > DiamondMaxHistoryChanges {
		return diamondHistoryUnavailable, "DiamondCut history exceeds the configured replay limit", nil
	}
	status, warning := replayDiamondSelectorChanges(changes, want)
	return status, warning, nil
}

func replayDiamondSelectorChanges(
	changes []diamondSelectorChange,
	want map[[4]byte]common.Address,
) (diamondHistoryStatus, string) {
	active := make(map[[4]byte]common.Address)
	for _, change := range changes {
		current, exists := active[change.selector]
		switch change.action {
		case 0:
			if exists || change.facet == (common.Address{}) {
				return diamondHistoryInconsistent, "DiamondCut Add history violates selector state"
			}
			active[change.selector] = change.facet
		case 1:
			if !exists || change.facet == (common.Address{}) || current == change.facet {
				return diamondHistoryInconsistent, "DiamondCut Replace history violates selector state"
			}
			active[change.selector] = change.facet
		case 2:
			if !exists || change.facet != (common.Address{}) {
				return diamondHistoryInconsistent, "DiamondCut Remove history violates selector state"
			}
			delete(active, change.selector)
		}
	}
	if !equalDiamondRoutes(active, want) {
		return diamondHistoryInconsistent,
			"DiamondCut history does not match the current Loupe snapshot"
	}
	return diamondHistoryConsistent, ""
}

func diamondHistoryCoverageComplete(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	diamond common.Address,
) (bool, error) {
	var complete bool
	err := func() error {
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
		queryRow, err := dbgen.New(tx).EnrichInlineDiamondHistoryCoverageCompleteStatement1(ctx, dbgen.EnrichInlineDiamondHistoryCoverageCompleteStatement1Params{ChainID: queryValue0, DiamondAddress: diamond[:], MaxBlockNumber: queryValue1, TargetEndHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version)})
		if err != nil {
			return err
		}
		complete = queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query DiamondCut history coverage: %w", err)
	}
	return complete, nil
}

func equalDiamondRoutes(left, right map[[4]byte]common.Address) bool {
	if len(left) != len(right) {
		return false
	}
	for selector, facet := range left {
		if right[selector] != facet {
			return false
		}
	}
	return true
}

func persistDiamondDetectionSnapshots(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	resolution ProxyDetectionResolution,
) error {
	for _, outcome := range resolution.Outcomes {
		if outcome.Family != ProxyFamilyERC2535 || outcome.Diamond == nil {
			continue
		}
		if err := persistDiamondDetectionSnapshot(ctx, tx, job, outcome); err != nil {
			return err
		}
	}
	return nil
}

func persistDiamondDetectionSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	outcome ProxyDetectionV2,
) error {
	if err := outcome.validate(); err != nil {
		return Permanent(err)
	}
	diamond := outcome.Diamond
	warnings, err := json.Marshal(outcome.Warnings)
	if err != nil {
		return fmt.Errorf("encode Diamond snapshot warnings: %w", err)
	}
	if len(warnings) > 1<<20 {
		return Permanent(errors.New("diamond snapshot warnings exceed configured limit"))
	}
	jobID, generation, err := proxyGenerationSQLIdentity(job)
	if err != nil {
		return Permanent(err)
	}
	var cutFacet []byte
	if diamond.StandardDiamondCut.Facet != nil {
		cutFacet = diamond.StandardDiamondCut.Facet[:]
	}
	var loupeReported *bool
	if diamond.LoupeInterfaceReported != nil {
		loupeReported = new(*diamond.LoupeInterfaceReported)
	}
	var truncationReason *string
	if diamond.TruncationReason != "" {
		truncationReason = new(diamond.TruncationReason)
	}
	state := string(outcome.Status)
	validation := string(diamond.Validation)
	var snapshotID int64
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
		queryRow, err := dbgen.New(tx).EnrichInlinePersistDiamondDetectionSnapshotStatement1(ctx, dbgen.EnrichInlinePersistDiamondDetectionSnapshotStatement1Params{ChainID: queryValue0, DiamondAddress: outcome.Proxy[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(job.Stage.Version), DetectionState: state, Completeness: string(diamond.Completeness), Validation: validation, StandardDiamondCut: string(diamond.StandardDiamondCut.Status), StandardDiamondCutFacet: cutFacet, LoupeInterfaceReported: loupeReported, Truncated: diamond.Truncated, TruncationReason: truncationReason, Warnings: []byte(string(warnings)), DurableJobID: jobID, JobGeneration: generation})
		if err != nil {
			return err
		}
		snapshotID = queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return Permanent(errors.New("existing Diamond Loupe snapshot conflicts with fixed-block state"))
	}
	if err != nil {
		return fmt.Errorf("persist Diamond Loupe snapshot: %w", err)
	}
	if snapshotID <= 0 {
		return Permanent(errors.New("diamond Loupe snapshot returned an invalid identity"))
	}
	facets := append([]ProxyTarget(nil), diamond.Facets...)
	slices.SortFunc(facets, func(left, right ProxyTarget) int {
		return bytes.Compare(left.Address[:], right.Address[:])
	})
	for _, facet := range facets {
		var codeHash []byte
		if facet.CodeHash != nil {
			codeHash = facet.CodeHash[:]
		}
		result, insertErr := dbgen.New(tx).EnrichInlinePersistDiamondDetectionSnapshotStatement2(ctx, dbgen.EnrichInlinePersistDiamondDetectionSnapshotStatement2Params{SnapshotID: snapshotID, FacetAddress: facet.Address[:], FacetKind: string(facet.Role), CodeExists: facet.CodeExists, CodeHash: codeHash})
		if insertErr != nil {
			return fmt.Errorf("persist Diamond Loupe facet: %w", insertErr)
		}
		affected := result
		if affected != 1 {
			return Permanent(errors.New("existing Diamond Loupe facet conflicts with fixed-block state"))
		}
		selectors := append([][4]byte(nil), facet.Selectors...)
		slices.SortFunc(selectors, func(left, right [4]byte) int {
			return bytes.Compare(left[:], right[:])
		})
		for _, selector := range selectors {
			result, insertErr = dbgen.New(tx).EnrichInlinePersistDiamondDetectionSnapshotStatement3(ctx, snapshotID, selector[:], facet.Address[:])
			if insertErr != nil {
				return fmt.Errorf("persist Diamond Loupe selector: %w", insertErr)
			}
			affected = result
			if affected != 1 {
				return Permanent(errors.New("existing Diamond Loupe selector conflicts with fixed-block state"))
			}
		}
	}
	return nil
}
