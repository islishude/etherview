package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	codeHash         common.Hash
	selector         [4]byte
	transactionHash  common.Hash
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
	tx pgx.Tx,
	job Job,
	observation abiObservation,
	selector [4]byte,
) (diamondABIRoute, error) {
	var detected bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichInlineResolveDiamondABIRouteStatement1(ctx, queryValue0, observation.target[:], queryValue1)
		if err != nil {
			return err
		}
		detected = queryRow
		return nil
	}(); err != nil {
		return diamondABIRoute{}, fmt.Errorf("query Diamond ABI identity: %w", err)
	}
	if !detected {
		return diamondABIRoute{}, nil
	}
	if observation.objectKind == abiObjectTraceCalldata {
		var sameTransactionCut bool
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			queryValue1, err := strconv.ParseInt(strconv.FormatUint(observation.transactionIndex, 10), 10, 64)
			if err != nil {
				return err
			}
			if ProxyStage.Version > 2147483647 {
				return errors.New("invalid stored query value")
			}
			queryRow, err := dbgen.New(tx).EnrichInlineResolveDiamondABIRouteStatement2(ctx, dbgen.EnrichInlineResolveDiamondABIRouteStatement2Params{ChainID: queryValue0, BlockHash: job.BlockHash[:], DiamondAddress: observation.target[:], TransactionIndex: int64(queryValue1), StageVersion: int32(ProxyStage.Version)})
			if err != nil {
				return err
			}
			sameTransactionCut = queryRow
			return nil
		}(); err != nil {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryValue2, err := strconv.ParseInt(strconv.FormatUint(observation.transactionIndex, 10), 10, 64)
		if err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichInlineResolveDiamondABIRouteStatement3(ctx, dbgen.EnrichInlineResolveDiamondABIRouteStatement3Params{ChainID: queryValue0, DiamondAddress: observation.target[:], Selector: selector[:], MaxBlockNumber: queryValue1, MaxTransactionIndex: int64(queryValue2)})
		if err != nil {
			return err
		}
		action = int(queryRow.Action)
		facetBytes = queryRow.FacetAddress
		return nil
	}()
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
	if !errors.Is(err, pgx.ErrNoRows) {
		return diamondABIRoute{}, fmt.Errorf("query historical Diamond ABI route: %w", err)
	}

	// A snapshot is a block-end fact. It is safe as a transaction-start route
	// only when this Diamond has no cuts in the containing block.
	var blockHasCut bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		if ProxyStage.Version > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).EnrichInlineResolveDiamondABIRouteStatement4(ctx, dbgen.EnrichInlineResolveDiamondABIRouteStatement4Params{ChainID: queryValue0, BlockHash: job.BlockHash[:], DiamondAddress: observation.target[:], StageVersion: int32(ProxyStage.Version)})
		if err != nil {
			return err
		}
		blockHasCut = queryRow
		return nil
	}(); err != nil {
		return diamondABIRoute{}, fmt.Errorf("query block DiamondCut presence: %w", err)
	}
	if blockHasCut {
		return diamondABIRoute{
			detected: true,
			warning:  "Diamond route before this transaction is not covered by selector history",
		}, nil
	}
	var completeness string
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichInlineResolveDiamondABIRouteStatement5(ctx, dbgen.EnrichInlineResolveDiamondABIRouteStatement5Params{ChainID: queryValue0, DiamondAddress: observation.target[:], Selector: selector[:], MaxBlockNumber: queryValue1})
		if err != nil {
			return err
		}
		completeness = queryRow.Completeness
		facetBytes = queryRow.FacetAddress
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	tx pgx.Tx,
	job Job,
	diamond common.Address,
	facet common.Address,
) (common.Hash, bool, error) {
	if facet == diamond {
		return common.Hash{}, true, nil
	}
	var hashBytes []byte
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichInlineLoadDiamondFacetCodeHashStatement1(ctx, dbgen.EnrichInlineLoadDiamondFacetCodeHashStatement1Params{ChainID: queryValue0, DiamondAddress: diamond[:], MaxBlockNumber: queryValue1, FacetAddress: facet[:]})
		if err != nil {
			return err
		}
		hashBytes = queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	tx pgx.Tx,
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
	tx pgx.Tx,
	job Job,
	target ABIIdentity,
	limits DecodeLimits,
) ([]persistedABIBinding, string, error) {
	rows, err := func() ([][]byte, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		if ProxyStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if diamondMaxAuxiliaryFacetCandidates+1 < -2147483648 || diamondMaxAuxiliaryFacetCandidates+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichInlineLoadDiamondAuxiliaryABIBindingsStatement1(ctx, dbgen.EnrichInlineLoadDiamondAuxiliaryABIBindingsStatement1Params{ChainID: queryValue0, DiamondAddress: target.Address[:], MaxBlockNumber: queryValue1, BlockHash: job.BlockHash[:], StageVersion: int32(ProxyStage.Version), Limit: int32(diamondMaxAuxiliaryFacetCandidates + 1)})
	}()
	if err != nil {
		return nil, "", fmt.Errorf("query Diamond auxiliary ABI facets: %w", err)
	}

	addresses := make([]common.Address, 0)
	for _, storedRow := range rows {
		var addressBytes []byte
		{
			addressBytes = storedRow
		}
		if len(addressBytes) != common.AddressLength {
			return nil, "", Permanent(errors.New("stored Diamond auxiliary facet is invalid"))
		}
		addresses = append(addresses, common.BytesToAddress(addressBytes))
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
		address: observation.target, codeHash: observation.identity.CodeHash,
		selector: selector, transactionHash: observation.transactionHash,
		transactionIndex: observation.transactionIndex,
		internalTrace:    observation.objectKind == abiObjectTraceCalldata,
	}
}
