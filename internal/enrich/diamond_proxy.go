package enrich

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sync/errgroup"
)

const diamondProxyDetectorVersion = "1.0.0"

var diamondLoupeABI = mustGethABI(`[
  {"type":"function","name":"facets","stateMutability":"view","inputs":[],"outputs":[{"name":"facets_","type":"tuple[]","components":[{"name":"facetAddress","type":"address"},{"name":"functionSelectors","type":"bytes4[]"}]}]},
  {"type":"function","name":"facetFunctionSelectors","stateMutability":"view","inputs":[{"name":"facet","type":"address"}],"outputs":[{"name":"functionSelectors_","type":"bytes4[]"}]},
  {"type":"function","name":"facetAddresses","stateMutability":"view","inputs":[],"outputs":[{"name":"facetAddresses_","type":"address[]"}]},
  {"type":"function","name":"facetAddress","stateMutability":"view","inputs":[{"name":"selector","type":"bytes4"}],"outputs":[{"name":"facetAddress_","type":"address"}]},
  {"type":"function","name":"supportsInterface","stateMutability":"view","inputs":[{"name":"interfaceId","type":"bytes4"}],"outputs":[{"name":"supported","type":"bool"}]}
]`)

var (
	diamondFacetsSelector                 = SignatureSelector("facets()")
	diamondFacetFunctionSelectorsSelector = SignatureSelector("facetFunctionSelectors(address)")
	diamondFacetAddressesSelector         = SignatureSelector("facetAddresses()")
	diamondFacetAddressSelector           = SignatureSelector("facetAddress(bytes4)")
	diamondCutSelector                    = SignatureSelector("diamondCut((address,uint8,bytes4[])[],address,bytes)")
	diamondSupportsInterfaceSelector      = SignatureSelector("supportsInterface(bytes4)")
	diamondLoupeInterfaceID               = [4]byte{0x48, 0xe2, 0xb0, 0x93}
)

var diamondRequiredLoupeSelectors = [...]([4]byte){
	diamondFacetsSelector,
	diamondFacetFunctionSelectorsSelector,
	diamondFacetAddressesSelector,
	diamondFacetAddressSelector,
}

type diamondFacetRow struct {
	FacetAddress      common.Address
	FunctionSelectors [][4]byte
}

type diamondProxyDetector struct {
	candidateEvidence []ProxyDetectionEvidence
	retainCandidate   bool
}

func newDiamondProxyDetector(candidate proxyCandidate) *diamondProxyDetector {
	detector := &diamondProxyDetector{}
	if candidate.hasSource(proxySourceDiamondCut) {
		detector.retainCandidate = true
		detector.candidateEvidence = append(detector.candidateEvidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceDiamondCutEvent, Description: "address emitted a DiamondCut candidate event",
		})
	}
	if candidate.hasSource(proxySourceVerifiedDiamondLoupe) {
		detector.retainCandidate = true
		detector.candidateEvidence = append(detector.candidateEvidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceVerifiedSource, Description: "verified ABI declares all ERC-2535 Loupe functions",
		})
	}
	if candidate.hasSource(proxySourceDelegatecallRouter) {
		detector.candidateEvidence = append(detector.candidateEvidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceExecutionTrace, Description: "execution trace observed this address routing with DELEGATECALL",
		})
	}
	return detector
}

func (*diamondProxyDetector) ID() string      { return "erc2535" }
func (*diamondProxyDetector) Version() string { return diamondProxyDetectorVersion }
func (*diamondProxyDetector) Priority() int   { return 150 }
func (*diamondProxyDetector) SupportedModes() []ProxyDetectionMode {
	return []ProxyDetectionMode{ProxyDetectionBulk, ProxyDetectionDeep}
}

func (detector *diamondProxyDetector) Detect(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	if detectionContext.Mode() == ProxyDetectionBulk && len(detector.candidateEvidence) == 0 {
		return nil, nil
	}
	proxy := detectionContext.Address()
	code, err := detectionContext.GetCode(ctx, proxy)
	if err != nil {
		return nil, err
	}
	if len(code) == 0 {
		return nil, nil
	}

	result := detector.baseResult()
	rows, callState, err := readDiamondFacets(ctx, detectionContext)
	if err != nil {
		return nil, err
	}
	if callState == diamondCallMalformed {
		return detector.inconsistent(result, "facets() returned malformed or non-canonical ABI data"), nil
	}
	if callState != diamondCallSuccess {
		rows, callState, err = readDiamondFacetsFallback(ctx, detectionContext)
		if err != nil {
			return nil, err
		}
		if callState == diamondCallMalformed {
			return detector.inconsistent(result, "fallback Loupe enumeration returned malformed or non-canonical ABI data"), nil
		}
		if callState != diamondCallSuccess {
			return detector.interfaceProbe(ctx, detectionContext, result, callState)
		}
		result.Warnings = append(result.Warnings, "facets() was unavailable; snapshot used facetAddresses() and facetFunctionSelectors(address)")
	}

	diamond, normalizeErr := normalizeDiamondFacetRows(proxy, rows)
	if normalizeErr != nil {
		if errors.Is(normalizeErr, errDiamondLimit) {
			result.Status = ProxyStatusCandidate
			result.Confidence = ProxyConfidenceLow
			result.Diamond.Completeness = DiamondPartial
			result.Diamond.Truncated = true
			result.Diamond.TruncationReason = diamondLimitReason(normalizeErr)
			result.Warnings = append(result.Warnings, normalizeErr.Error())
			return result, nil
		}
		return detector.inconsistent(result, normalizeErr.Error()), nil
	}
	result.Diamond = &diamond
	result.Targets = append([]ProxyTarget(nil), diamond.Facets...)

	if err := validateDiamondFacetCode(ctx, detectionContext, result); err != nil {
		return nil, err
	}
	if result.Status == ProxyStatusInconsistent {
		return result, nil
	}
	if err := crossCheckDiamondLoupe(ctx, detectionContext, result); err != nil {
		return nil, err
	}
	if result.Status == ProxyStatusInconsistent {
		return result, nil
	}
	probeDiamondERC165(ctx, detectionContext, result)
	result.Status = ProxyStatusConfirmed
	result.Confidence = ProxyConfidenceHigh
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind:        ProxyEvidenceLoupeCall,
		Description: fmt.Sprintf("Loupe snapshot validated %d facets and %d selectors", len(result.Diamond.Facets), len(result.Diamond.SelectorToFacet)),
	})
	return result, nil
}

func (detector *diamondProxyDetector) baseResult() *ProxyDetectionV2 {
	return &ProxyDetectionV2{
		Family: ProxyFamilyERC2535, Variant: "diamond",
		Status: ProxyStatusCandidate, Confidence: ProxyConfidenceLow,
		Diamond: &DiamondDetection{
			Completeness: DiamondUnknown, Validation: DiamondValidationInterfaceOnly,
			Facets: []ProxyTarget{}, SelectorToFacet: map[[4]byte]common.Address{},
			ImplementationAddresses: []common.Address{},
			StandardDiamondCut:      DiamondStandardCut{Status: DiamondCutUnknown},
		},
		Targets:  []ProxyTarget{},
		Evidence: append([]ProxyDetectionEvidence(nil), detector.candidateEvidence...),
		Warnings: []string{},
	}
}

func (detector *diamondProxyDetector) inconsistent(result *ProxyDetectionV2, warning string) *ProxyDetectionV2 {
	result.Status = ProxyStatusInconsistent
	result.Confidence = ProxyConfidenceHigh
	result.Warnings = append(result.Warnings, warning)
	return result
}

type diamondCallState uint8

const (
	diamondCallSuccess diamondCallState = iota
	diamondCallReverted
	diamondCallTooLarge
	diamondCallMalformed
)

func readDiamondFacets(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) ([]diamondFacetRow, diamondCallState, error) {
	call, err := callDiamondMethod(ctx, detectionContext, "facets")
	if err != nil {
		return nil, 0, err
	}
	if !call.Success {
		return nil, diamondCallStateForResult(call), nil
	}
	rows, err := unpackDiamondOutput[[]diamondFacetRow]("facets", call.Data)
	if err != nil {
		return nil, diamondCallMalformed, nil
	}
	return rows, diamondCallSuccess, nil
}

func readDiamondFacetsFallback(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) ([]diamondFacetRow, diamondCallState, error) {
	call, err := callDiamondMethod(ctx, detectionContext, "facetAddresses")
	if err != nil {
		return nil, 0, err
	}
	if !call.Success {
		return nil, diamondCallStateForResult(call), nil
	}
	addresses, err := unpackDiamondOutput[[]common.Address]("facetAddresses", call.Data)
	if err != nil {
		return nil, diamondCallMalformed, nil
	}
	if len(addresses) > DiamondMaxFacets {
		return nil, diamondCallTooLarge, nil
	}
	calls, err := mapDiamondBatch(ctx, addresses, func(callContext context.Context, address common.Address) (ProxyCallResult, error) {
		return callDiamondMethod(callContext, detectionContext, "facetFunctionSelectors", address)
	})
	if err != nil {
		return nil, 0, err
	}
	rows := make([]diamondFacetRow, 0, len(addresses))
	for index, address := range addresses {
		call := calls[index]
		if !call.Success {
			return nil, diamondCallStateForResult(call), nil
		}
		selectors, decodeErr := unpackDiamondOutput[[][4]byte]("facetFunctionSelectors", call.Data)
		if decodeErr != nil {
			return nil, diamondCallMalformed, nil
		}
		rows = append(rows, diamondFacetRow{FacetAddress: address, FunctionSelectors: selectors})
	}
	return rows, diamondCallSuccess, nil
}

func (detector *diamondProxyDetector) interfaceProbe(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	result *ProxyDetectionV2,
	priorState diamondCallState,
) (*ProxyDetectionV2, error) {
	call, err := callDiamondMethod(ctx, detectionContext, "facetAddress", diamondFacetsSelector)
	if err != nil {
		return nil, err
	}
	if call.Success {
		facet, decodeErr := unpackDiamondOutput[common.Address]("facetAddress", call.Data)
		if decodeErr != nil {
			return detector.inconsistent(result, "facetAddress(bytes4) returned malformed or non-canonical ABI data"), nil
		}
		if facet != (common.Address{}) {
			role := ProxyTargetFacet
			if facet == detectionContext.Address() {
				role = ProxyTargetImmutable
			}
			target := ProxyTarget{
				Address: facet, Role: role,
				Selectors: [][4]byte{diamondFacetsSelector}, CodeExists: true,
			}
			if role == ProxyTargetFacet {
				code, codeErr := detectionContext.GetCode(ctx, facet)
				if codeErr != nil {
					return nil, codeErr
				}
				if len(code) == 0 {
					return detector.inconsistent(result, "facetAddress(bytes4) returned an external facet without runtime code"), nil
				}
				hash := codeHash(code)
				target.CodeHash = &hash
			}
			result.Targets = []ProxyTarget{target}
			result.Diamond.Facets = append([]ProxyTarget(nil), result.Targets...)
			result.Diamond.SelectorToFacet[diamondFacetsSelector] = facet
			if role == ProxyTargetFacet {
				result.Diamond.ImplementationAddresses = []common.Address{facet}
			}
			result.Diamond.Completeness = DiamondPartial
			if priorState == diamondCallTooLarge {
				result.Diamond.Truncated = true
				result.Diamond.TruncationReason = "max-raw-return-bytes-exceeded"
				result.Warnings = append(result.Warnings, "Loupe enumeration exceeded the configured return-data limit")
			}
			result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
				Kind: ProxyEvidenceLoupeCall, Description: "facetAddress(facets.selector) returned a non-zero target",
				Address: addressCopy(facet),
			})
			result.Warnings = append(result.Warnings, "only one Loupe selector route could be validated")
			return result, nil
		}
	}
	if !detector.retainCandidate && priorState != diamondCallTooLarge {
		return nil, nil
	}
	if priorState == diamondCallTooLarge {
		result.Diamond.Completeness = DiamondPartial
		result.Diamond.Truncated = true
		result.Diamond.TruncationReason = "max-raw-return-bytes-exceeded"
		result.Warnings = append(result.Warnings, "Loupe enumeration exceeded the configured return-data limit")
	} else {
		result.Warnings = append(result.Warnings, "candidate evidence exists but Loupe enumeration was not callable")
	}
	return result, nil
}

func normalizeDiamondFacetRows(proxy common.Address, rows []diamondFacetRow) (DiamondDetection, error) {
	result := DiamondDetection{
		Completeness: DiamondComplete, Validation: DiamondValidationFull,
		Facets: []ProxyTarget{}, SelectorToFacet: make(map[[4]byte]common.Address),
		ImplementationAddresses: []common.Address{},
		StandardDiamondCut:      DiamondStandardCut{Status: DiamondCutUnknown},
	}
	if len(rows) == 0 {
		return result, errors.New("loupe returned no facets")
	}
	if len(rows) > DiamondMaxFacets {
		return result, newDiamondLimitError("max-facets-exceeded", "Loupe facet count exceeds configured limit")
	}
	seenFacets := make(map[common.Address]struct{}, len(rows))
	selectorCount := 0
	for _, row := range rows {
		if row.FacetAddress == (common.Address{}) {
			return result, errors.New("loupe returned the zero address as a facet")
		}
		if _, duplicate := seenFacets[row.FacetAddress]; duplicate {
			return result, errors.New("loupe returned a duplicate facet address")
		}
		seenFacets[row.FacetAddress] = struct{}{}
		if len(row.FunctionSelectors) == 0 {
			return result, errors.New("loupe returned a facet without active selectors")
		}
		if len(row.FunctionSelectors) > DiamondMaxSelectorsPerFacet {
			return result, newDiamondLimitError("max-selectors-per-facet-exceeded", "Loupe facet selector count exceeds configured limit")
		}
		role := ProxyTargetFacet
		if row.FacetAddress == proxy {
			role = ProxyTargetImmutable
		}
		target := ProxyTarget{
			Address: row.FacetAddress, Role: role,
			Selectors: append([][4]byte(nil), row.FunctionSelectors...), CodeExists: true,
		}
		slices.SortFunc(target.Selectors, func(left, right [4]byte) int {
			return bytes.Compare(left[:], right[:])
		})
		for _, selector := range target.Selectors {
			if _, duplicate := result.SelectorToFacet[selector]; duplicate {
				return result, errors.New("loupe returned one selector for multiple facets")
			}
			result.SelectorToFacet[selector] = row.FacetAddress
			selectorCount++
			if selectorCount > DiamondMaxSelectorsTotal {
				return result, newDiamondLimitError("max-selectors-total-exceeded", "Loupe selector count exceeds configured limit")
			}
		}
		result.Facets = append(result.Facets, target)
		if role == ProxyTargetFacet {
			result.ImplementationAddresses = append(result.ImplementationAddresses, row.FacetAddress)
		}
	}
	for _, selector := range diamondRequiredLoupeSelectors {
		if _, exists := result.SelectorToFacet[selector]; !exists {
			return result, errors.New("loupe snapshot omitted a required ERC-2535 Loupe selector")
		}
	}
	slices.SortFunc(result.Facets, func(left, right ProxyTarget) int {
		return bytes.Compare(left.Address[:], right.Address[:])
	})
	slices.SortFunc(result.ImplementationAddresses, func(left, right common.Address) int {
		return bytes.Compare(left[:], right[:])
	})
	return result, nil
}

func validateDiamondFacetCode(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	result *ProxyDetectionV2,
) error {
	defer func() {
		result.Targets = append([]ProxyTarget(nil), result.Diamond.Facets...)
	}()
	external := make([]int, 0, len(result.Diamond.Facets))
	for index := range result.Diamond.Facets {
		if result.Diamond.Facets[index].Role == ProxyTargetFacet {
			external = append(external, index)
		}
	}
	codes, err := mapDiamondBatch(ctx, external, func(codeContext context.Context, index int) ([]byte, error) {
		return detectionContext.GetCode(codeContext, result.Diamond.Facets[index].Address)
	})
	if err != nil {
		return err
	}
	for codeIndex, facetIndex := range external {
		facet := &result.Diamond.Facets[facetIndex]
		code := codes[codeIndex]
		facet.CodeExists = len(code) != 0
		if len(code) == 0 {
			result.Status = ProxyStatusInconsistent
			result.Confidence = ProxyConfidenceHigh
			result.Warnings = append(result.Warnings, "Loupe returned an external facet without runtime code")
			return nil
		}
		hash := codeHash(code)
		facet.CodeHash = &hash
	}
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind:        ProxyEvidenceFacetCode,
		Description: fmt.Sprintf("validated runtime code for %d external facets", len(external)),
	})
	return nil
}

func crossCheckDiamondLoupe(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	result *ProxyDetectionV2,
) error {
	diamond := result.Diamond
	call, err := callDiamondMethod(ctx, detectionContext, "facetAddresses")
	if err != nil {
		return err
	}
	if !call.Success {
		markDiamondInconsistent(result, "facetAddresses() did not agree with facets()")
		return nil
	}
	addresses, decodeErr := unpackDiamondOutput[[]common.Address]("facetAddresses", call.Data)
	if decodeErr != nil || !sameDiamondAddressSet(addresses, diamond.Facets) {
		markDiamondInconsistent(result, "facetAddresses() address set differs from facets()")
		return nil
	}

	selectors := sortedDiamondSelectors(diamond.SelectorToFacet)
	fullValidation := len(diamond.Facets)+len(selectors)+3 <= DiamondMaxCrossCheckCalls
	facetChecks := len(diamond.Facets)
	selectorChecks := len(selectors)
	if !fullValidation {
		remaining := DiamondMaxCrossCheckCalls - 3
		facetChecks = min(len(diamond.Facets), remaining/2)
		selectorChecks = min(len(selectors), remaining-facetChecks)
		diamond.Validation = DiamondValidationSampled
	}
	checkedFacets := deterministicTargetSample(diamond.Facets, facetChecks)
	facetCalls, err := mapDiamondBatch(ctx, checkedFacets, func(callContext context.Context, facet ProxyTarget) (ProxyCallResult, error) {
		return callDiamondMethod(callContext, detectionContext, "facetFunctionSelectors", facet.Address)
	})
	if err != nil {
		return err
	}
	for index, facet := range checkedFacets {
		call := facetCalls[index]
		if !call.Success {
			markDiamondInconsistent(result, "facetFunctionSelectors(address) reverted for an enumerated facet")
			return nil
		}
		observed, decodeErr := unpackDiamondOutput[[][4]byte]("facetFunctionSelectors", call.Data)
		if decodeErr != nil || !sameSelectorSet(observed, facet.Selectors) {
			markDiamondInconsistent(result, "facetFunctionSelectors(address) differs from facets()")
			return nil
		}
	}
	checkedSelectors := deterministicSelectorSample(selectors, selectorChecks)
	selectorCalls, err := mapDiamondBatch(ctx, checkedSelectors, func(callContext context.Context, selector [4]byte) (ProxyCallResult, error) {
		return callDiamondMethod(callContext, detectionContext, "facetAddress", selector)
	})
	if err != nil {
		return err
	}
	for index, selector := range checkedSelectors {
		call := selectorCalls[index]
		if !call.Success {
			markDiamondInconsistent(result, "facetAddress(bytes4) reverted for an active selector")
			return nil
		}
		observed, decodeErr := unpackDiamondOutput[common.Address]("facetAddress", call.Data)
		if decodeErr != nil || observed != diamond.SelectorToFacet[selector] {
			markDiamondInconsistent(result, "facetAddress(bytes4) differs from facets()")
			return nil
		}
	}

	unknown := unknownDiamondSelector(diamond.SelectorToFacet)
	call, err = callDiamondMethod(ctx, detectionContext, "facetAddress", unknown)
	if err != nil {
		return err
	}
	if !call.Success {
		markDiamondInconsistent(result, "facetAddress(bytes4) reverted for an absent selector")
		return nil
	}
	missing, decodeErr := unpackDiamondOutput[common.Address]("facetAddress", call.Data)
	if decodeErr != nil || missing != (common.Address{}) {
		markDiamondInconsistent(result, "facetAddress(bytes4) did not return zero for an absent selector")
		return nil
	}

	call, err = callDiamondMethod(ctx, detectionContext, "facetAddress", diamondCutSelector)
	if err != nil {
		return err
	}
	if !call.Success {
		diamond.StandardDiamondCut = DiamondStandardCut{Status: DiamondCutUnknown}
		result.Warnings = append(result.Warnings, "standard diamondCut selector could not be queried")
		return nil
	}
	cutFacet, decodeErr := unpackDiamondOutput[common.Address]("facetAddress", call.Data)
	if decodeErr != nil {
		markDiamondInconsistent(result, "standard diamondCut selector returned malformed data")
		return nil
	}
	if cutFacet == (common.Address{}) {
		diamond.StandardDiamondCut = DiamondStandardCut{Status: DiamondCutAbsent}
	} else {
		diamond.StandardDiamondCut = DiamondStandardCut{Status: DiamondCutPresent, Facet: addressCopy(cutFacet)}
		if mapped, exists := diamond.SelectorToFacet[diamondCutSelector]; !exists || mapped != cutFacet {
			markDiamondInconsistent(result, "standard diamondCut selector differs from the Loupe snapshot")
		}
	}
	return nil
}

func probeDiamondERC165(ctx context.Context, detectionContext *ProxyDetectionContext, result *ProxyDetectionV2) {
	call, err := callDiamondMethod(ctx, detectionContext, "supportsInterface", diamondLoupeInterfaceID)
	if err != nil || !call.Success {
		result.Warnings = append(result.Warnings, "ERC-165 Loupe interface support was not reported")
		return
	}
	supported, decodeErr := unpackDiamondOutput[bool]("supportsInterface", call.Data)
	if decodeErr != nil {
		result.Warnings = append(result.Warnings, "supportsInterface(bytes4) returned malformed data")
		return
	}
	result.Diamond.LoupeInterfaceReported = &supported
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceERC165, Description: fmt.Sprintf("supportsInterface(0x48e2b093) returned %t", supported),
	})
	if !supported {
		result.Warnings = append(result.Warnings, "ERC-165 did not report the optional Loupe interface")
	}
}

func callDiamondMethod(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	method string,
	arguments ...any,
) (ProxyCallResult, error) {
	data, err := diamondLoupeABI.Pack(method, arguments...)
	if err != nil {
		return ProxyCallResult{}, Permanent(fmt.Errorf("pack Diamond %s call: %w", method, err))
	}
	callContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return detectionContext.Call(callContext, ProxyCallInput{
		To: detectionContext.Address(), Data: data, GasLimit: DiamondCallGasLimit,
		MaxReturnBytes: DiamondMaxRawReturnBytes,
	})
}

func mapDiamondBatch[Input, Output any](
	ctx context.Context,
	inputs []Input,
	call func(context.Context, Input) (Output, error),
) ([]Output, error) {
	results := make([]Output, len(inputs))
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(DiamondMaxBatchConcurrency)
	for index, input := range inputs {
		group.Go(func() error {
			result, err := call(groupContext, input)
			if err != nil {
				return err
			}
			results[index] = result
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

func unpackDiamondOutput[T any](method string, data []byte) (output T, err error) {
	definition, ok := diamondLoupeABI.Methods[method]
	if !ok {
		return output, errors.New("unknown Diamond Loupe method")
	}
	values, err := definition.Outputs.Unpack(data)
	if err != nil || len(values) != 1 {
		return output, errors.New("invalid Diamond Loupe ABI output")
	}
	reencoded, err := definition.Outputs.Pack(values...)
	if err != nil || !bytes.Equal(reencoded, data) {
		return output, errors.New("non-canonical Diamond Loupe ABI output")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("diamond Loupe ABI output has an unexpected type")
		}
	}()
	converted, ok := gethabi.ConvertType(values[0], new(T)).(*T)
	if !ok || converted == nil {
		return output, errors.New("diamond Loupe ABI output conversion failed")
	}
	return *converted, nil
}

func diamondCallStateForResult(result ProxyCallResult) diamondCallState {
	if result.Error == "return_too_large" {
		return diamondCallTooLarge
	}
	return diamondCallReverted
}

var errDiamondLimit = errors.New("diamond detector limit exceeded")

type diamondLimitError struct {
	reason  string
	message string
}

func (err diamondLimitError) Error() string { return err.message }
func (err diamondLimitError) Unwrap() error { return errDiamondLimit }

func newDiamondLimitError(reason, message string) error {
	return diamondLimitError{reason: reason, message: message}
}

func diamondLimitReason(err error) string {
	if limit, ok := errors.AsType[diamondLimitError](err); ok {
		return limit.reason
	}
	return "limit-exceeded"
}

func markDiamondInconsistent(result *ProxyDetectionV2, warning string) {
	result.Status = ProxyStatusInconsistent
	result.Confidence = ProxyConfidenceHigh
	result.Warnings = append(result.Warnings, warning)
}

func sameDiamondAddressSet(addresses []common.Address, facets []ProxyTarget) bool {
	if len(addresses) != len(facets) {
		return false
	}
	left := append([]common.Address(nil), addresses...)
	right := make([]common.Address, len(facets))
	for index := range facets {
		right[index] = facets[index].Address
	}
	sortAddresses := func(values []common.Address) {
		slices.SortFunc(values, func(left, right common.Address) int { return bytes.Compare(left[:], right[:]) })
	}
	sortAddresses(left)
	sortAddresses(right)
	return slices.Equal(left, right)
}

func sameSelectorSet(left, right [][4]byte) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([][4]byte(nil), left...), append([][4]byte(nil), right...)
	sortSelectors := func(values [][4]byte) {
		slices.SortFunc(values, func(left, right [4]byte) int { return bytes.Compare(left[:], right[:]) })
	}
	sortSelectors(leftCopy)
	sortSelectors(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func sortedDiamondSelectors(routes map[[4]byte]common.Address) [][4]byte {
	selectors := make([][4]byte, 0, len(routes))
	for selector := range routes {
		selectors = append(selectors, selector)
	}
	slices.SortFunc(selectors, func(left, right [4]byte) int { return bytes.Compare(left[:], right[:]) })
	return selectors
}

func deterministicTargetSample(values []ProxyTarget, limit int) []ProxyTarget {
	if limit >= len(values) {
		return append([]ProxyTarget(nil), values...)
	}
	return append([]ProxyTarget(nil), values[:limit]...)
}

func deterministicSelectorSample(values [][4]byte, limit int) [][4]byte {
	if limit >= len(values) {
		return append([][4]byte(nil), values...)
	}
	return append([][4]byte(nil), values[:limit]...)
}

func unknownDiamondSelector(routes map[[4]byte]common.Address) [4]byte {
	for candidate := uint32(0xffffffff); ; candidate-- {
		selector := [4]byte{byte(candidate >> 24), byte(candidate >> 16), byte(candidate >> 8), byte(candidate)}
		if _, exists := routes[selector]; !exists {
			return selector
		}
	}
}

func validateDiamondSelectorConstants() {
	for name, pair := range map[string]struct {
		actual [4]byte
		want   string
	}{
		"facets":                 {diamondFacetsSelector, "7a0ed627"},
		"facetFunctionSelectors": {diamondFacetFunctionSelectorsSelector, "adfca15e"},
		"facetAddresses":         {diamondFacetAddressesSelector, "52ef6b2c"},
		"facetAddress":           {diamondFacetAddressSelector, "cdffacc6"},
		"diamondCut":             {diamondCutSelector, "1f931c1c"},
		"supportsInterface":      {diamondSupportsInterfaceSelector, "01ffc9a7"},
	} {
		if fmt.Sprintf("%x", pair.actual[:]) != pair.want {
			panic("invalid ERC-2535 selector for " + strings.TrimSpace(name))
		}
	}
}

func init() { validateDiamondSelectorConstants() }
