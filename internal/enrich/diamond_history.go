package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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
		// database/sql treats a nil []byte as SQL NULL. Preserve canonical empty
		// event bytes as a non-nil zero-length BYTEA value.
		calldata: append([]byte{}, calldata...), cuts: cuts,
	}, true
}

func persistDiamondCutRecord(
	ctx context.Context,
	tx *sql.Tx,
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
	result, err := tx.ExecContext(ctx, `
		INSERT INTO diamond_cut_events AS current (
		    chain_id, block_number, block_hash, transaction_hash,
		    transaction_index, log_index, diamond_address, init_address,
		    init_calldata, cuts, stage_version, canonical
		)
		SELECT $1::numeric, $2::numeric, $3, $4, $5::bigint, $6::bigint,
		       $7, $8, $9, $10::jsonb, $11,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = $1::numeric AND number = $2::numeric
		             AND block_hash = $3
		       )
		ON CONFLICT (chain_id, block_hash, log_index, stage_version)
		DO UPDATE SET canonical = EXCLUDED.canonical
		WHERE current.block_number = EXCLUDED.block_number
		  AND current.transaction_hash = EXCLUDED.transaction_hash
		  AND current.transaction_index = EXCLUDED.transaction_index
		  AND current.diamond_address = EXCLUDED.diamond_address
		  AND current.init_address = EXCLUDED.init_address
		  AND current.init_calldata = EXCLUDED.init_calldata
		  AND current.cuts = EXCLUDED.cuts`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		record.hash[:], strconv.FormatUint(record.transactionIndex, 10),
		strconv.FormatUint(record.index, 10), record.diamond[:], record.init[:],
		record.calldata, string(encoded), job.Stage.Version,
	)
	if err != nil {
		return fmt.Errorf("persist DiamondCut event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read DiamondCut event persistence result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing DiamondCut event conflicts with indexed log"))
	}
	for cutIndex, cut := range record.cuts {
		for selectorIndex, selector := range cut.FunctionSelectors {
			result, err = tx.ExecContext(ctx, `
				INSERT INTO diamond_selector_changes AS current (
				    chain_id, block_hash, log_index, stage_version,
				    cut_index, selector_index, selector, action, facet_address
				) VALUES (
				    $1::numeric, $2, $3::bigint, $4, $5, $6, $7, $8, $9
				)
				ON CONFLICT (
				    chain_id, block_hash, log_index, stage_version,
				    cut_index, selector_index
				) DO UPDATE SET selector = EXCLUDED.selector
				WHERE current.selector = EXCLUDED.selector
				  AND current.action = EXCLUDED.action
				  AND current.facet_address = EXCLUDED.facet_address`,
				job.ChainID, job.BlockHash[:], strconv.FormatUint(record.index, 10),
				job.Stage.Version, cutIndex, selectorIndex, selector[:], cut.Action,
				cut.FacetAddress[:],
			)
			if err != nil {
				return fmt.Errorf("persist Diamond selector change: %w", err)
			}
			affected, err = result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read Diamond selector change persistence result: %w", err)
			}
			if affected != 1 {
				return Permanent(errors.New("existing Diamond selector change conflicts with indexed log"))
			}
		}
	}
	return nil
}

func (processor *PostgresProxyProcessor) reconcileDiamondHistory(
	ctx context.Context,
	tx *sql.Tx,
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
	tx *sql.Tx,
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
	rows, err := tx.QueryContext(ctx, `
		SELECT change.selector, change.action, change.facet_address
		FROM diamond_cut_events AS event
		JOIN diamond_selector_changes AS change
		  ON change.chain_id = event.chain_id
		 AND change.block_hash = event.block_hash
		 AND change.log_index = event.log_index
		 AND change.stage_version = event.stage_version
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = event.chain_id
		 AND canonical.number = event.block_number
		 AND canonical.block_hash = event.block_hash
		WHERE event.chain_id = $1::numeric
		  AND event.diamond_address = $2
		  AND event.block_number <= $3::numeric
		  AND event.canonical
		  AND event.stage_version = $5
		  AND (
		      event.block_hash = $4 OR EXISTS (
		          SELECT 1
		          FROM published_block_stage_results AS published
		          WHERE published.chain_id = event.chain_id
		            AND published.block_number = event.block_number
		            AND published.block_hash = event.block_hash
		            AND published.stage = 'proxy'
		            AND published.stage_version = event.stage_version
		            AND published.state = 'complete'
		      )
		  )
		ORDER BY event.block_number, event.transaction_index, event.log_index,
		         change.cut_index, change.selector_index
		LIMIT $6`,
		job.ChainID, diamond[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], job.Stage.Version, DiamondMaxHistoryChanges+1,
	)
	if err != nil {
		return diamondHistoryUnavailable, "", fmt.Errorf("query DiamondCut history: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	changes := make([]diamondSelectorChange, 0)
	for rows.Next() {
		var selectorBytes, facetBytes []byte
		var action int
		if err := rows.Scan(&selectorBytes, &action, &facetBytes); err != nil {
			return diamondHistoryUnavailable, "", fmt.Errorf("scan DiamondCut history: %w", err)
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
	if err := rows.Err(); err != nil {
		return diamondHistoryUnavailable, "", fmt.Errorf("iterate DiamondCut history: %w", err)
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
	tx *sql.Tx,
	job Job,
	diamond common.Address,
) (bool, error) {
	var complete bool
	err := tx.QueryRowContext(ctx, `
		WITH first_cut AS (
		    SELECT event.block_number, event.block_hash
		    FROM diamond_cut_events AS event
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = event.chain_id
		     AND canonical.number = event.block_number
		     AND canonical.block_hash = event.block_hash
		    WHERE event.chain_id = $1::numeric
		      AND event.diamond_address = $2
		      AND event.block_number <= $3::numeric
		      AND event.stage_version = $5
		      AND event.canonical
		      AND (
		          event.block_hash = $4 OR EXISTS (
		              SELECT 1
		              FROM published_block_stage_results AS published
		              WHERE published.chain_id = event.chain_id
		                AND published.block_number = event.block_number
		                AND published.block_hash = event.block_hash
		                AND published.stage = 'proxy'
		                AND published.stage_version = event.stage_version
		                AND published.state = 'complete'
		          )
		      )
		    ORDER BY event.block_number, event.transaction_index, event.log_index
		    LIMIT 1
		), created AS (
		    SELECT EXISTS (
		        SELECT 1
		        FROM receipts AS receipt
		        WHERE receipt.chain_id = $1::numeric
		          AND receipt.block_number = first_cut.block_number
		          AND receipt.block_hash = first_cut.block_hash
		          AND lower(receipt.raw->>'contractAddress') =
		              lower('0x' || encode($2, 'hex'))
		        UNION ALL
		        SELECT 1
		        FROM normalized_traces AS trace
		        JOIN published_block_stage_results AS published
		          ON published.chain_id = trace.chain_id
		         AND published.block_number = trace.block_number
		         AND published.block_hash = trace.block_hash
		         AND published.stage = 'trace'
		         AND published.stage_version = 2
		         AND published.state = 'complete'
		        WHERE trace.chain_id = $1::numeric
		          AND trace.block_number = first_cut.block_number
		          AND trace.block_hash = first_cut.block_hash
		          AND trace.created_address = $2
		          AND trace.call_type IN ('CREATE', 'CREATE2')
		          AND NOT trace.reverted
		          AND trace.canonical
		    ) AS at_first_cut
		    FROM first_cut
		)
		SELECT COALESCE(
		    created.at_first_cut AND proxy_interaction_coverage_contains(
		        $1::numeric, first_cut.block_number, first_cut.block_hash,
		        $3::numeric, $4
		    ), FALSE
		)
		FROM first_cut
		JOIN created ON TRUE`,
		job.ChainID, diamond[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], job.Stage.Version,
	).Scan(&complete)
	if errors.Is(err, sql.ErrNoRows) {
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
	tx *sql.Tx,
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
	tx *sql.Tx,
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
	var cutFacet any
	if diamond.StandardDiamondCut.Facet != nil {
		cutFacet = diamond.StandardDiamondCut.Facet[:]
	}
	var loupeReported any
	if diamond.LoupeInterfaceReported != nil {
		loupeReported = *diamond.LoupeInterfaceReported
	}
	var truncationReason any
	if diamond.TruncationReason != "" {
		truncationReason = diamond.TruncationReason
	}
	state := string(outcome.Status)
	validation := string(diamond.Validation)
	var snapshotID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO diamond_loupe_snapshots AS current (
		    chain_id, diamond_address, block_number, block_hash, stage_version,
		    detection_state, completeness, validation, standard_diamond_cut,
		    standard_diamond_cut_facet, loupe_interface_reported, truncated,
		    truncation_reason, warnings, canonical, durable_job_id, job_generation
		)
		SELECT $1::numeric, $2, $3::numeric, $4, $5, $6, $7, $8, $9,
		       $10, $11, $12, $13, $14::jsonb,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = $1::numeric AND number = $3::numeric
		             AND block_hash = $4
		       ),
		       $15, $16::bigint
		ON CONFLICT (
		    chain_id, diamond_address, block_hash, stage_version,
		    durable_job_id, job_generation
		) DO UPDATE SET canonical = EXCLUDED.canonical
		WHERE current.block_number = EXCLUDED.block_number
		  AND current.detection_state = EXCLUDED.detection_state
		  AND current.completeness = EXCLUDED.completeness
		  AND current.validation = EXCLUDED.validation
		  AND current.standard_diamond_cut = EXCLUDED.standard_diamond_cut
		  AND current.standard_diamond_cut_facet IS NOT DISTINCT FROM
		      EXCLUDED.standard_diamond_cut_facet
		  AND current.loupe_interface_reported IS NOT DISTINCT FROM
		      EXCLUDED.loupe_interface_reported
		  AND current.truncated = EXCLUDED.truncated
		  AND current.truncation_reason IS NOT DISTINCT FROM
		      EXCLUDED.truncation_reason
		  AND current.warnings = EXCLUDED.warnings
		RETURNING id`,
		job.ChainID, outcome.Proxy[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], job.Stage.Version, state, string(diamond.Completeness),
		validation, string(diamond.StandardDiamondCut.Status), cutFacet,
		loupeReported, diamond.Truncated, truncationReason, string(warnings),
		jobID, generation,
	).Scan(&snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
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
		var codeHash any
		if facet.CodeHash != nil {
			codeHash = facet.CodeHash[:]
		}
		result, insertErr := tx.ExecContext(ctx, `
			INSERT INTO diamond_loupe_facets AS current (
			    snapshot_id, facet_address, facet_kind, code_exists, code_hash
			) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (snapshot_id, facet_address)
			DO UPDATE SET facet_kind = EXCLUDED.facet_kind
			WHERE current.facet_kind = EXCLUDED.facet_kind
			  AND current.code_exists = EXCLUDED.code_exists
			  AND current.code_hash IS NOT DISTINCT FROM EXCLUDED.code_hash`,
			snapshotID, facet.Address[:], string(facet.Role), facet.CodeExists, codeHash,
		)
		if insertErr != nil {
			return fmt.Errorf("persist Diamond Loupe facet: %w", insertErr)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read Diamond Loupe facet persistence result: %w", rowsErr)
		}
		if affected != 1 {
			return Permanent(errors.New("existing Diamond Loupe facet conflicts with fixed-block state"))
		}
		selectors := append([][4]byte(nil), facet.Selectors...)
		slices.SortFunc(selectors, func(left, right [4]byte) int {
			return bytes.Compare(left[:], right[:])
		})
		for _, selector := range selectors {
			result, insertErr = tx.ExecContext(ctx, `
				INSERT INTO diamond_loupe_selectors AS current (
				    snapshot_id, selector, facet_address
				) VALUES ($1, $2, $3)
				ON CONFLICT (snapshot_id, selector)
				DO UPDATE SET facet_address = EXCLUDED.facet_address
				WHERE current.facet_address = EXCLUDED.facet_address`,
				snapshotID, selector[:], facet.Address[:],
			)
			if insertErr != nil {
				return fmt.Errorf("persist Diamond Loupe selector: %w", insertErr)
			}
			affected, rowsErr = result.RowsAffected()
			if rowsErr != nil {
				return fmt.Errorf("read Diamond Loupe selector persistence result: %w", rowsErr)
			}
			if affected != 1 {
				return Permanent(errors.New("existing Diamond Loupe selector conflicts with fixed-block state"))
			}
		}
	}
	return nil
}
