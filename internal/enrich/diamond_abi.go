package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type diamondABIRoute struct {
	detected      bool
	exact         bool
	facet         common.Address
	facetCodeHash common.Hash
	warning       string
}

type diamondABIRouteKey struct {
	address          common.Address
	selector         [4]byte
	transactionIndex uint64
	internalTrace    bool
}

var diamondAuxiliaryABIScope = crypto.Keccak256Hash([]byte("erc2535:event-error-abi:v1"))

const diamondMaxAuxiliaryFacetCandidates = DiamondMaxFacets * 2

func diamondFunctionObservation(observation abiObservation) ([4]byte, bool) {
	if observation.objectKind != abiObjectTransactionCalldata &&
		observation.objectKind != abiObjectTraceCalldata || len(observation.input) < 4 {
		return [4]byte{}, false
	}
	var selector [4]byte
	copy(selector[:], observation.input[:4])
	return selector, true
}

func resolveDiamondABIRoute(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	observation abiObservation,
	selector [4]byte,
) (diamondABIRoute, error) {
	var detected bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM published_diamond_loupe_snapshots AS snapshot
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = snapshot.chain_id
		     AND canonical.number = snapshot.block_number
		     AND canonical.block_hash = snapshot.block_hash
		    WHERE snapshot.chain_id = $1::numeric
		      AND snapshot.diamond_address = $2
		      AND snapshot.block_number <= $3::numeric
		      AND snapshot.detection_state = 'confirmed'
		      AND snapshot.canonical
		)`, job.ChainID, observation.target[:],
		strconv.FormatUint(job.BlockNumber, 10)).Scan(&detected); err != nil {
		return diamondABIRoute{}, fmt.Errorf("query Diamond ABI identity: %w", err)
	}
	if !detected {
		return diamondABIRoute{}, nil
	}
	if observation.objectKind == abiObjectTraceCalldata {
		var sameTransactionCut bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
			    SELECT 1
			    FROM diamond_cut_events AS event
			    WHERE event.chain_id = $1::numeric
			      AND event.block_hash = $2
			      AND event.diamond_address = $3
			      AND event.transaction_index = $4::bigint
			      AND event.canonical
			      AND event.stage_version = $5
			)`, job.ChainID, job.BlockHash[:], observation.target[:],
			strconv.FormatUint(observation.transactionIndex, 10),
			ProxyStage.Version).Scan(&sameTransactionCut); err != nil {
			return diamondABIRoute{}, fmt.Errorf("query same-transaction DiamondCut: %w", err)
		}
		if sameTransactionCut {
			return diamondABIRoute{
				detected: true,
				warning:  "Diamond selector changed in the same transaction; call-frame routing requires an execution trace with exact ordering",
			}, nil
		}
	}

	var action int
	var facetBytes []byte
	err := tx.QueryRowContext(ctx, `
		SELECT change.action, change.facet_address
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
		JOIN published_block_stage_results AS published
		  ON published.chain_id = event.chain_id
		 AND published.block_number = event.block_number
		 AND published.block_hash = event.block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = event.stage_version
		 AND published.state = 'complete'
		WHERE event.chain_id = $1::numeric
		  AND event.diamond_address = $2
		  AND change.selector = $3
		  AND event.canonical
		  AND (
		      event.block_number < $4::numeric OR
		      (event.block_number = $4::numeric AND
		       event.transaction_index < $5::bigint)
		  )
		ORDER BY event.block_number DESC, event.transaction_index DESC,
		         event.log_index DESC, change.cut_index DESC,
		         change.selector_index DESC
		LIMIT 1`,
		job.ChainID, observation.target[:], selector[:],
		strconv.FormatUint(job.BlockNumber, 10),
		strconv.FormatUint(observation.transactionIndex, 10),
	).Scan(&action, &facetBytes)
	if err == nil {
		if action < 0 || action > 2 || len(facetBytes) != common.AddressLength {
			return diamondABIRoute{}, Permanent(errors.New("stored Diamond ABI route is invalid"))
		}
		if action == 2 {
			return diamondABIRoute{
				detected: true, exact: true,
				warning: "selector was not registered at the transaction position",
			}, nil
		}
		facet := common.BytesToAddress(facetBytes)
		if facet == (common.Address{}) {
			return diamondABIRoute{}, Permanent(errors.New("stored active Diamond ABI route is zero"))
		}
		hash, found, hashErr := loadDiamondFacetCodeHash(
			ctx, tx, job, observation.target, facet,
		)
		if hashErr != nil {
			return diamondABIRoute{}, hashErr
		}
		route := diamondABIRoute{detected: true, exact: true, facet: facet}
		if facet != observation.target {
			if !found {
				route.warning = "active Diamond facet has no published exact code identity"
			} else {
				route.facetCodeHash = hash
			}
		}
		return route, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return diamondABIRoute{}, fmt.Errorf("query historical Diamond ABI route: %w", err)
	}

	// A snapshot is a block-end fact. It is safe as a transaction-start route
	// only when this Diamond has no cuts in the containing block.
	var blockHasCut bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM diamond_cut_events AS event
		    WHERE event.chain_id = $1::numeric
		      AND event.block_hash = $2
		      AND event.diamond_address = $3
		      AND event.canonical
		      AND event.stage_version = $4
		)`, job.ChainID, job.BlockHash[:], observation.target[:],
		ProxyStage.Version).Scan(&blockHasCut); err != nil {
		return diamondABIRoute{}, fmt.Errorf("query block DiamondCut presence: %w", err)
	}
	if blockHasCut {
		return diamondABIRoute{
			detected: true,
			warning:  "Diamond route before this transaction is not covered by selector history",
		}, nil
	}
	var completeness string
	err = tx.QueryRowContext(ctx, `
		SELECT snapshot.completeness, selector.facet_address
		FROM published_diamond_loupe_snapshots AS snapshot
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = snapshot.chain_id
		 AND canonical.number = snapshot.block_number
		 AND canonical.block_hash = snapshot.block_hash
		LEFT JOIN diamond_loupe_selectors AS selector
		  ON selector.snapshot_id = snapshot.id AND selector.selector = $3
		WHERE snapshot.chain_id = $1::numeric
		  AND snapshot.diamond_address = $2
		  AND snapshot.block_number <= $4::numeric
		  AND snapshot.detection_state = 'confirmed'
		  AND snapshot.canonical
		ORDER BY snapshot.block_number DESC, snapshot.id DESC
		LIMIT 1`, job.ChainID, observation.target[:], selector[:],
		strconv.FormatUint(job.BlockNumber, 10)).Scan(&completeness, &facetBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return diamondABIRoute{detected: true}, nil
	}
	if err != nil {
		return diamondABIRoute{}, fmt.Errorf("query Diamond Loupe ABI route: %w", err)
	}
	if len(facetBytes) == 0 {
		if completeness == string(DiamondComplete) {
			return diamondABIRoute{
				detected: true, exact: true,
				warning: "selector was not registered in the complete Loupe snapshot",
			}, nil
		}
		return diamondABIRoute{
			detected: true,
			warning:  "partial Loupe snapshot does not cover this selector",
		}, nil
	}
	if len(facetBytes) != common.AddressLength {
		return diamondABIRoute{}, Permanent(errors.New("stored Loupe selector facet is invalid"))
	}
	facet := common.BytesToAddress(facetBytes)
	hash, found, err := loadDiamondFacetCodeHash(ctx, tx, job, observation.target, facet)
	if err != nil {
		return diamondABIRoute{}, err
	}
	route := diamondABIRoute{detected: true, exact: true, facet: facet}
	if facet != observation.target {
		if found {
			route.facetCodeHash = hash
		} else {
			route.warning = "active Diamond facet has no published exact code identity"
		}
	}
	return route, nil
}

func loadDiamondFacetCodeHash(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	diamond common.Address,
	facet common.Address,
) (common.Hash, bool, error) {
	if facet == diamond {
		return common.Hash{}, true, nil
	}
	var hashBytes []byte
	err := tx.QueryRowContext(ctx, `
		SELECT facet.code_hash
		FROM published_diamond_loupe_snapshots AS snapshot
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = snapshot.chain_id
		 AND canonical.number = snapshot.block_number
		 AND canonical.block_hash = snapshot.block_hash
		JOIN diamond_loupe_facets AS facet
		  ON facet.snapshot_id = snapshot.id
		WHERE snapshot.chain_id = $1::numeric
		  AND snapshot.diamond_address = $2
		  AND snapshot.block_number <= $3::numeric
		  AND snapshot.detection_state = 'confirmed'
		  AND snapshot.canonical
		  AND facet.facet_address = $4
		  AND facet.facet_kind = 'facet'
		  AND facet.code_exists
		  AND facet.code_hash IS NOT NULL
		ORDER BY snapshot.block_number DESC, snapshot.id DESC
		LIMIT 1`, job.ChainID, diamond[:],
		strconv.FormatUint(job.BlockNumber, 10), facet[:]).Scan(&hashBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return common.Hash{}, false, nil
	}
	if err != nil {
		return common.Hash{}, false, fmt.Errorf("query Diamond facet code identity: %w", err)
	}
	hash, err := WordFromBytes(hashBytes)
	if err != nil || hash == (common.Hash{}) {
		return common.Hash{}, false, Permanent(errors.New("stored Diamond facet code hash is invalid"))
	}
	return hash, true, nil
}

func loadDiamondFacetABIBinding(
	ctx context.Context,
	tx *sql.Tx,
	target ABIIdentity,
	route diamondABIRoute,
	selector [4]byte,
	limits DecodeLimits,
) (persistedABIBinding, bool, error) {
	if !route.exact || route.facet == (common.Address{}) ||
		route.facet == target.Address || route.facetCodeHash == (common.Hash{}) {
		return persistedABIBinding{}, false, nil
	}
	block := target.BlockNumber
	candidate, found, err := loadVerifiedABIBinding(
		ctx, tx, target, route.facet, route.facetCodeHash,
		abiBlockRange{from: block, to: &block}, ABISourceDiamondFacet,
	)
	if err != nil || !found {
		return persistedABIBinding{}, found, err
	}
	filtered, err := filterABIFunctionSelector(
		candidate.abi, candidate.binding.Source, selector, limits,
	)
	if err != nil {
		return persistedABIBinding{}, false, Permanent(fmt.Errorf("filter Diamond facet ABI: %w", err))
	}
	if len(filtered) == 0 {
		return persistedABIBinding{}, false, nil
	}
	candidate.abi = filtered
	candidate.binding.SelectorScope = crypto.Keccak256Hash(selector[:])
	return candidate, true, nil
}

func filterABIFunctionSelector(
	document []byte,
	source ABISource,
	selector [4]byte,
	limits DecodeLimits,
) ([]byte, error) {
	if len(document) == 0 || len(document) > limits.MaxDocumentBytes {
		return nil, errors.New("ABI document exceeds configured bounds")
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(document, &rawEntries); err != nil {
		return nil, err
	}
	if len(rawEntries) > limits.MaxEntries {
		return nil, errors.New("ABI entry count exceeds configured bounds")
	}
	selected := make([]json.RawMessage, 0, 1)
	for _, raw := range rawEntries {
		wrapper := make([]byte, 0, len(raw)+2)
		wrapper = append(wrapper, '[')
		wrapper = append(wrapper, raw...)
		wrapper = append(wrapper, ']')
		entries, err := parseABIEntries(wrapper, source, limits)
		if err != nil {
			return nil, err
		}
		if len(entries) == 1 && entries[0].kind == ABIKindFunction &&
			entries[0].selector == selector {
			selected = append(selected, append(json.RawMessage(nil), raw...))
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func loadDiamondAuxiliaryABIBindings(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	target ABIIdentity,
	limits DecodeLimits,
) ([]persistedABIBinding, string, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH selected_snapshots AS (
		    (SELECT snapshot.id
		     FROM published_diamond_loupe_snapshots AS snapshot
		     JOIN canonical_blocks AS canonical
		       ON canonical.chain_id = snapshot.chain_id
		      AND canonical.number = snapshot.block_number
		      AND canonical.block_hash = snapshot.block_hash
		     WHERE snapshot.chain_id = $1::numeric
		       AND snapshot.diamond_address = $2
		       AND snapshot.block_number <= $3::numeric
		       AND snapshot.detection_state = 'confirmed'
		       AND snapshot.completeness = 'complete'
		       AND snapshot.canonical
		     ORDER BY snapshot.block_number DESC, snapshot.id DESC
		     LIMIT 1)
		    UNION
		    (SELECT snapshot.id
		     FROM published_diamond_loupe_snapshots AS snapshot
		     JOIN canonical_blocks AS canonical
		       ON canonical.chain_id = snapshot.chain_id
		      AND canonical.number = snapshot.block_number
		      AND canonical.block_hash = snapshot.block_hash
		     WHERE snapshot.chain_id = $1::numeric
		       AND snapshot.diamond_address = $2
		       AND snapshot.block_number < $3::numeric
		       AND snapshot.detection_state = 'confirmed'
		       AND snapshot.completeness = 'complete'
		       AND snapshot.canonical
		     ORDER BY snapshot.block_number DESC, snapshot.id DESC
		     LIMIT 1)
		), candidates AS (
		    SELECT facet.facet_address
		    FROM diamond_loupe_facets AS facet
		    WHERE facet.snapshot_id IN (SELECT id FROM selected_snapshots)
		      AND facet.facet_kind = 'facet'
		      AND facet.code_exists
		      AND facet.code_hash IS NOT NULL
		    UNION
		    SELECT change.facet_address
		    FROM diamond_cut_events AS event
		    JOIN diamond_selector_changes AS change
		      ON change.chain_id = event.chain_id
		     AND change.block_hash = event.block_hash
		     AND change.log_index = event.log_index
		     AND change.stage_version = event.stage_version
		    WHERE event.chain_id = $1::numeric
		      AND event.block_hash = $4
		      AND event.diamond_address = $2
		      AND event.canonical
		      AND event.stage_version = $5
		      AND change.action IN (0, 1)
		      AND change.facet_address <> decode(repeat('00', 20), 'hex')
		)
		SELECT facet_address
		FROM candidates
		ORDER BY facet_address
		LIMIT $6`,
		job.ChainID, target.Address[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], ProxyStage.Version, diamondMaxAuxiliaryFacetCandidates+1,
	)
	if err != nil {
		return nil, "", fmt.Errorf("query Diamond auxiliary ABI facets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	addresses := make([]common.Address, 0)
	for rows.Next() {
		var addressBytes []byte
		if err := rows.Scan(&addressBytes); err != nil {
			return nil, "", fmt.Errorf("scan Diamond auxiliary ABI facet: %w", err)
		}
		if len(addressBytes) != common.AddressLength {
			return nil, "", Permanent(errors.New("stored Diamond auxiliary facet is invalid"))
		}
		addresses = append(addresses, common.BytesToAddress(addressBytes))
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate Diamond auxiliary ABI facets: %w", err)
	}
	if len(addresses) > diamondMaxAuxiliaryFacetCandidates {
		return nil, "Diamond event/error ABI facet candidate limit exceeded", nil
	}
	bindings := make([]persistedABIBinding, 0, len(addresses))
	for _, address := range addresses {
		codeHash, found, err := loadDiamondFacetCodeHash(ctx, tx, job, target.Address, address)
		if err != nil {
			return nil, "", err
		}
		if !found {
			continue
		}
		block := target.BlockNumber
		candidate, found, err := loadVerifiedABIBinding(
			ctx, tx, target, address, codeHash,
			abiBlockRange{from: block, to: &block}, ABISourceDiamondFacet,
		)
		if err != nil {
			return nil, "", err
		}
		if !found {
			continue
		}
		candidate.abi, err = filterABIAuxiliaryEntries(candidate.abi, limits)
		if err != nil {
			return nil, "", Permanent(fmt.Errorf("filter Diamond event/error ABI: %w", err))
		}
		if len(candidate.abi) == 0 {
			continue
		}
		candidate.binding.SelectorScope = diamondAuxiliaryABIScope
		bindings = append(bindings, candidate)
	}
	return bindings, "", nil
}

func filterABIAuxiliaryEntries(document []byte, limits DecodeLimits) ([]byte, error) {
	if len(document) == 0 || len(document) > limits.MaxDocumentBytes {
		return nil, errors.New("ABI document exceeds configured bounds")
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(document, &rawEntries); err != nil {
		return nil, err
	}
	if len(rawEntries) > limits.MaxEntries {
		return nil, errors.New("ABI entry count exceeds configured bounds")
	}
	selected := make([]json.RawMessage, 0)
	for _, raw := range rawEntries {
		wrapper := append([]byte{'['}, raw...)
		wrapper = append(wrapper, ']')
		entries, err := parseABIEntries(wrapper, ABISourceDiamondFacet, limits)
		if err != nil {
			return nil, err
		}
		if len(entries) == 1 && (entries[0].kind == ABIKindEvent || entries[0].kind == ABIKindError) {
			selected = append(selected, append(json.RawMessage(nil), raw...))
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return json.Marshal(selected)
}

func diamondFunctionCandidates(
	base []persistedABIBinding,
	route diamondABIRoute,
	selector [4]byte,
	limits DecodeLimits,
) ([]persistedABIBinding, error) {
	result := make([]persistedABIBinding, 0, len(base)+1)
	for _, candidate := range base {
		if route.facet != candidate.binding.Identity.Address &&
			candidate.binding.Source != ABISourceSignatureDatabase {
			continue
		}
		filtered, err := filterABIFunctionSelector(
			candidate.abi, candidate.binding.Source, selector, limits,
		)
		if err != nil {
			return nil, err
		}
		if len(filtered) == 0 {
			continue
		}
		candidate.abi = filtered
		result = append(result, candidate)
	}
	return result, nil
}

func appendDecodeWarning(decoded *decodedABIObservation, warning string) {
	if decoded == nil || warning == "" {
		return
	}
	if decoded.result.Warning == "" {
		decoded.result.Warning = warning
	} else {
		decoded.result.Warning += "; " + warning
	}
}

func abiBindingKey(candidate persistedABIBinding) string {
	binding := candidate.binding
	return fmt.Sprintf(
		"%s:%x:%x:%s:%x:%x:%x:%d:%x",
		binding.Identity.ChainID, binding.Identity.Address[:], binding.Identity.CodeHash[:],
		binding.Source, binding.SourceAddress[:], binding.SourceCodeHash[:],
		binding.SelectorScope[:], binding.ValidFromBlock, binding.Identity.BlockHash[:],
	)
}

func routeKeyForObservation(
	observation abiObservation,
	selector [4]byte,
) diamondABIRouteKey {
	return diamondABIRouteKey{
		address: observation.target, selector: selector,
		transactionIndex: observation.transactionIndex,
		internalTrace:    observation.objectKind == abiObjectTraceCalldata,
	}
}
