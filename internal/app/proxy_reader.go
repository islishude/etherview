package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/query"
)

type proxyQueryReader interface {
	Proxy(context.Context, string) (query.ProxyDetail, error)
	ProxyUpgrades(context.Context, string, string, int) (query.ProxyUpgradePage, error)
	ProxyInitializations(context.Context, string, string, int) (query.ProxyInitializationPage, error)
	DiamondCuts(context.Context, string, string, int) (query.DiamondCutPage, error)
}

// proxyReaderAdapter owns the public-model boundary. The query reader returns
// repository-local values so malformed persisted identities cannot leak into
// the generated OpenAPI response types.
type proxyReaderAdapter struct {
	reader            proxyQueryReader
	chainID           string
	publicDetectionV2 bool
}

func newProxyReaderAdapter(reader proxyQueryReader, chainID uint64, publicDetectionV2 bool) proxyReaderAdapter {
	return proxyReaderAdapter{
		reader: reader, chainID: strconv.FormatUint(chainID, 10),
		publicDetectionV2: publicDetectionV2,
	}
}

func (adapter proxyReaderAdapter) Proxy(ctx context.Context, address string) (gen.ProxyDetails, error) {
	detail, err := adapter.reader.Proxy(ctx, address)
	if err != nil {
		return gen.ProxyDetails{}, err
	}
	return adapter.proxyDetails(detail)
}

func (adapter proxyReaderAdapter) ProxyUpgrades(
	ctx context.Context,
	address, cursor string,
	limit int,
) (gen.ProxyUpgradeHistory, string, error) {
	page, err := adapter.reader.ProxyUpgrades(ctx, address, cursor, limit)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, "", err
	}
	model, err := adapter.proxyUpgradeHistory(page)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, "", err
	}
	return model, page.NextCursor, nil
}

func (adapter proxyReaderAdapter) ProxyInitializations(
	ctx context.Context,
	address, cursor string,
	limit int,
) (gen.ProxyInitializationHistory, string, error) {
	page, err := adapter.reader.ProxyInitializations(ctx, address, cursor, limit)
	if err != nil {
		return gen.ProxyInitializationHistory{}, "", err
	}
	model, err := adapter.proxyInitializationHistory(page)
	if err != nil {
		return gen.ProxyInitializationHistory{}, "", err
	}
	return model, page.NextCursor, nil
}

func (adapter proxyReaderAdapter) DiamondCuts(
	ctx context.Context,
	address, cursor string,
	limit int,
) (gen.DiamondCutHistory, string, error) {
	page, err := adapter.reader.DiamondCuts(ctx, address, cursor, limit)
	if err != nil {
		return gen.DiamondCutHistory{}, "", err
	}
	model, err := adapter.diamondCutHistory(page)
	if err != nil {
		return gen.DiamondCutHistory{}, "", err
	}
	return model, page.NextCursor, nil
}

func (adapter proxyReaderAdapter) proxyDetails(detail query.ProxyDetail) (gen.ProxyDetails, error) {
	if len(detail.Evidence) > 64 {
		return gen.ProxyDetails{}, errors.New("proxy evidence exceeds the public bound")
	}
	if detail.Status == string(gen.ProxyDetailStatusVerified) {
		standardVersionValid := detail.StandardVersion == "5.6.1"
		if detail.Pattern == string(gen.ProxyPatternClone) {
			standardVersionValid = detail.StandardVersion == ""
		}
		if detail.BindingID == "" || detail.EvidenceState != string(gen.ProxyEvidenceStateExact) ||
			!standardVersionValid || detail.Pattern == "" ||
			detail.Pattern == string(gen.ProxyPatternUnknown) || detail.Mechanism == "" ||
			detail.Proxy == nil || (detail.Pattern != string(gen.ProxyPatternClone) && !detail.Proxy.Verified) ||
			detail.Implementation == nil || !detail.Implementation.Verified {
			return gen.ProxyDetails{}, errors.New("verified proxy detail lacks an exact verified binding")
		}
		switch detail.Pattern {
		case string(gen.ProxyPatternTransparent):
			if detail.Management == nil || detail.Management.Kind != string(gen.ProxyManagementKindProxyAdmin) ||
				!detail.Management.Target.Verified {
				return gen.ProxyDetails{}, errors.New("verified transparent proxy lacks a verified ProxyAdmin binding")
			}
		case string(gen.ProxyPatternBeacon):
			if detail.Management == nil || detail.Management.Kind != string(gen.ProxyManagementKindUpgradeableBeacon) ||
				!detail.Management.Target.Verified {
				return gen.ProxyDetails{}, errors.New("verified beacon proxy lacks a verified beacon-management binding")
			}
		}
	} else if detail.BindingID != "" {
		return gen.ProxyDetails{}, errors.New("non-verified proxy detail carries a binding identity")
	}
	address, err := proxyAddress(detail.Address)
	if err != nil {
		return gen.ProxyDetails{}, fmt.Errorf("invalid proxy detail address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(detail.Snapshot)
	if err != nil {
		return gen.ProxyDetails{}, err
	}
	status := gen.ProxyDetailStatus(detail.Status)
	if !status.Valid() {
		return gen.ProxyDetails{}, fmt.Errorf("invalid proxy detail status %q", detail.Status)
	}
	model := gen.ProxyDetails{
		Address:  address,
		Status:   status,
		Snapshot: snapshot,
		Evidence: make([]gen.ProxyRecognitionEvidence, len(detail.Evidence)),
	}
	if adapter.publicDetectionV2 && len(detail.DetectionV2) != 0 {
		if len(detail.DetectionV2) > 4<<20 {
			return gen.ProxyDetails{}, errors.New("proxy detection V2 exceeds the public bound")
		}
		var detection gen.ProxyDetectionV2
		if err := json.Unmarshal(detail.DetectionV2, &detection); err != nil {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy detection V2: %w", err)
		}
		if err := adapter.validateProxyDetectionV2(detection, address); err != nil {
			return gen.ProxyDetails{}, err
		}
		model.ProxyDetectionV2 = &detection
		if model.Status == gen.ProxyDetailStatusNotDetected &&
			(detection.Status == gen.ProxyDetectionV2StatusConfirmed ||
				detection.Status == gen.ProxyDetectionV2StatusCandidate ||
				detection.Status == gen.ProxyDetectionV2StatusInconsistent) {
			model.Status = gen.ProxyDetailStatusDetectedUnverified
		}
		if detection.Primary != nil && detection.Primary.Diamond != nil {
			addresses := append([]gen.Address(nil), detection.Primary.Diamond.ImplementationAddresses...)
			model.ImplementationAddresses = &addresses
		}
	}
	if model.Proxy, err = proxyCurrentIdentity(detail.Proxy); err != nil {
		return gen.ProxyDetails{}, fmt.Errorf("invalid proxy identity: %w", err)
	}
	if model.Implementation, err = proxyCurrentIdentity(detail.Implementation); err != nil {
		return gen.ProxyDetails{}, fmt.Errorf("invalid implementation identity: %w", err)
	}
	if model.Admin, err = proxyCurrentIdentity(detail.Admin); err != nil {
		return gen.ProxyDetails{}, fmt.Errorf("invalid admin identity: %w", err)
	}
	if model.Beacon, err = proxyCurrentIdentity(detail.Beacon); err != nil {
		return gen.ProxyDetails{}, fmt.Errorf("invalid beacon identity: %w", err)
	}
	if model.Management, err = proxyManagement(detail.Management); err != nil {
		return gen.ProxyDetails{}, err
	}
	if detail.Mechanism != "" {
		value := gen.ProxyMechanism(detail.Mechanism)
		if !value.Valid() {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy mechanism %q", detail.Mechanism)
		}
		model.Mechanism = &value
	}
	if detail.Pattern != "" {
		value := gen.ProxyPattern(detail.Pattern)
		if !value.Valid() {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy pattern %q", detail.Pattern)
		}
		model.Pattern = &value
	}
	if detail.StandardVersion != "" {
		value := gen.ProxyDetailsStandardVersion(detail.StandardVersion)
		if !value.Valid() {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy standard version %q", detail.StandardVersion)
		}
		model.StandardVersion = &value
	}
	if detail.Confidence != "" {
		value := gen.ProxyConfidence(detail.Confidence)
		if !value.Valid() {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy confidence %q", detail.Confidence)
		}
		model.Confidence = &value
	}
	if detail.EvidenceState != "" {
		value := gen.ProxyEvidenceState(detail.EvidenceState)
		if !value.Valid() {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy evidence state %q", detail.EvidenceState)
		}
		model.EvidenceState = &value
	}
	if detail.ImmutableArgs != "" {
		if len(detail.ImmutableArgs) > 49064 {
			return gen.ProxyDetails{}, errors.New("proxy immutable arguments exceed the public bound")
		}
		if _, parseErr := ethrpc.ParseData(detail.ImmutableArgs); parseErr != nil {
			return gen.ProxyDetails{}, errors.New("proxy immutable arguments are invalid")
		}
		model.ImmutableArgs = &detail.ImmutableArgs
	}
	if detail.BindingID != "" {
		identifier, parseErr := uuid.Parse(detail.BindingID)
		if parseErr != nil || identifier == uuid.Nil {
			return gen.ProxyDetails{}, errors.New("proxy binding identity is invalid")
		}
		model.BindingId = &identifier
	}
	model.ImplementationInteraction, err = proxyImplementationInteraction(detail, model)
	if err != nil {
		return gen.ProxyDetails{}, err
	}
	for index := range detail.Evidence {
		model.Evidence[index], err = proxyEvidence(detail.Evidence[index])
		if err != nil {
			return gen.ProxyDetails{}, fmt.Errorf("invalid proxy evidence: %w", err)
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) validateProxyDetectionV2(
	detection gen.ProxyDetectionV2,
	proxy gen.Address,
) error {
	if !detection.Status.Valid() || len(detection.Outcomes) > 16 || len(detection.Conflicts) > 16 {
		return errors.New("proxy detection V2 resolution is outside the public contract")
	}
	validate := func(outcome gen.ProxyDetectionV2Outcome) error {
		if outcome.Detector == "" || !outcome.Status.Valid() || !outcome.Confidence.Valid() ||
			outcome.Proxy != proxy || string(outcome.ChainId) != adapter.chainID ||
			len(outcome.Evidence) > 64 || len(outcome.Warnings) > 64 ||
			len(outcome.ImplementationPath) > 8 || len(outcome.Targets) > enrich.DiamondMaxFacets+2 {
			return errors.New("proxy detection V2 outcome is outside the public contract")
		}
		if outcome.Family != nil && !outcome.Family.Valid() {
			return errors.New("proxy detection V2 family is invalid")
		}
		if outcome.ImplementationRole != nil && !outcome.ImplementationRole.Valid() {
			return errors.New("proxy detection V2 implementation role is invalid")
		}
		for _, target := range outcome.Targets {
			if !target.Role.Valid() || len(target.Selectors) > enrich.DiamondMaxSelectorsPerFacet {
				return errors.New("proxy detection V2 target is invalid")
			}
			if _, err := proxyAddress(target.Address); err != nil {
				return errors.New("proxy detection V2 target address is invalid")
			}
			for _, selector := range target.Selectors {
				if !validFunctionSelector(string(selector)) {
					return errors.New("proxy detection V2 target selector is invalid")
				}
			}
		}
		if outcome.Family != nil && *outcome.Family == gen.ProxyDetectionV2FamilyErc2535 {
			if outcome.Diamond == nil || outcome.Implementation != nil || outcome.ImplementationRole != nil {
				return errors.New("ERC-2535 outcome does not use selector-scoped targets")
			}
			if err := validatePublicDiamond(*outcome.Diamond, outcome.Targets, proxy, outcome.Status); err != nil {
				return err
			}
		} else if outcome.Diamond != nil {
			return errors.New("non-Diamond outcome carries Diamond details")
		}
		for _, evidence := range outcome.Evidence {
			if !evidence.Kind.Valid() || evidence.Description == "" {
				return errors.New("proxy detection V2 evidence is invalid")
			}
		}
		return nil
	}
	for _, outcome := range detection.Outcomes {
		if err := validate(outcome); err != nil {
			return err
		}
	}
	if detection.Primary != nil {
		if err := validate(*detection.Primary); err != nil {
			return err
		}
	}
	return nil
}

func validFunctionSelector(value string) bool {
	if len(value) != 10 || value[0:2] != "0x" {
		return false
	}
	for _, char := range value[2:] {
		if char < '0' || char > '9' {
			lower := char | 0x20
			if lower < 'a' || lower > 'f' {
				return false
			}
		}
	}
	return true
}

func validatePublicDiamond(
	diamond gen.DiamondDetection,
	targets []gen.ProxyTarget,
	proxy gen.Address,
	status gen.ProxyDetectionV2Status,
) error {
	if !diamond.Completeness.Valid() || !diamond.Validation.Valid() ||
		!diamond.StandardDiamondCut.Status.Valid() || len(diamond.Facets) > enrich.DiamondMaxFacets ||
		len(diamond.SelectorToFacet) > enrich.DiamondMaxSelectorsTotal ||
		len(diamond.ImplementationAddresses) > enrich.DiamondMaxFacets {
		return errors.New("diamond detection is outside the public contract")
	}
	if diamond.Truncated != (diamond.TruncationReason != nil) ||
		diamond.Completeness == gen.DiamondCompletenessComplete && diamond.Truncated {
		return errors.New("diamond truncation state is invalid")
	}
	if (diamond.StandardDiamondCut.Status == gen.DiamondCutPresencePresent) !=
		(diamond.StandardDiamondCut.Facet != nil) {
		return errors.New("standard DiamondCut target is invalid")
	}
	if diamond.Completeness == gen.DiamondCompletenessComplete &&
		(len(diamond.Facets) == 0 || len(diamond.SelectorToFacet) == 0) {
		return errors.New("complete Diamond detection has no selector routes")
	}
	if !samePublicProxyTargetSet(targets, diamond.Facets) {
		return errors.New("ERC-2535 root targets differ from Diamond facets")
	}
	seenFacets := make(map[string]gen.ProxyTarget, len(diamond.Facets))
	seenSelectors := make(map[string]string, len(diamond.SelectorToFacet))
	externalFacets := make(map[string]struct{}, len(diamond.Facets))
	for _, facet := range diamond.Facets {
		if facet.Role != gen.ProxyTargetRoleFacet && facet.Role != gen.ProxyTargetRoleImmutable ||
			facet.Role == gen.ProxyTargetRoleImmutable && facet.Address != proxy ||
			facet.Role == gen.ProxyTargetRoleImmutable && !facet.CodeExists ||
			facet.Role == gen.ProxyTargetRoleFacet && facet.Address == proxy ||
			len(facet.Selectors) == 0 ||
			status == gen.ProxyDetectionV2StatusConfirmed && facet.Role == gen.ProxyTargetRoleFacet &&
				(!facet.CodeExists || facet.CodeHash == nil) {
			return errors.New("diamond facet role is invalid")
		}
		key := strings.ToLower(facet.Address)
		if _, duplicate := seenFacets[key]; duplicate {
			return errors.New("diamond facet is duplicated")
		}
		seenFacets[key] = facet
		if facet.Role == gen.ProxyTargetRoleFacet {
			externalFacets[key] = struct{}{}
		}
		for _, rawSelector := range facet.Selectors {
			selector := strings.ToLower(string(rawSelector))
			if !validFunctionSelector(selector) {
				return errors.New("diamond selector is invalid")
			}
			if _, duplicate := seenSelectors[selector]; duplicate {
				return errors.New("diamond selector appears more than once")
			}
			mapped, ok := diamond.SelectorToFacet[selector]
			if !ok || !strings.EqualFold(mapped, facet.Address) {
				return errors.New("diamond selector map is inconsistent")
			}
			seenSelectors[selector] = key
		}
	}
	for selector, facet := range diamond.SelectorToFacet {
		if !validFunctionSelector(selector) || seenSelectors[strings.ToLower(selector)] != strings.ToLower(facet) {
			return errors.New("diamond selector map contains an unlisted route")
		}
	}
	seenImplementations := make(map[string]struct{}, len(diamond.ImplementationAddresses))
	for _, address := range diamond.ImplementationAddresses {
		key := strings.ToLower(address)
		facet, ok := seenFacets[key]
		if !ok || facet.Role != gen.ProxyTargetRoleFacet {
			return errors.New("diamond implementation address is not an external facet")
		}
		if _, duplicate := seenImplementations[key]; duplicate {
			return errors.New("diamond implementation address is duplicated")
		}
		seenImplementations[key] = struct{}{}
	}
	if len(seenImplementations) != len(externalFacets) {
		return errors.New("diamond implementation addresses do not cover every external facet")
	}
	for address := range externalFacets {
		if _, exists := seenImplementations[address]; !exists {
			return errors.New("diamond implementation address is missing an external facet")
		}
	}
	if diamond.StandardDiamondCut.Status == gen.DiamondCutPresencePresent {
		mapped, exists := diamond.SelectorToFacet["0x1f931c1c"]
		if !exists || !strings.EqualFold(mapped, string(*diamond.StandardDiamondCut.Facet)) {
			return errors.New("standard DiamondCut target differs from selector map")
		}
	}
	return nil
}

func samePublicProxyTargetSet(left, right []gen.ProxyTarget) bool {
	if len(left) != len(right) {
		return false
	}
	byAddress := make(map[string]gen.ProxyTarget, len(left))
	for _, target := range left {
		key := strings.ToLower(target.Address)
		if _, duplicate := byAddress[key]; duplicate {
			return false
		}
		byAddress[key] = target
	}
	for _, target := range right {
		observed, exists := byAddress[strings.ToLower(target.Address)]
		if !exists || observed.Role != target.Role || observed.CodeExists != target.CodeExists ||
			!samePublicOptionalHash(observed.CodeHash, target.CodeHash) ||
			!samePublicSelectorSet(observed.Selectors, target.Selectors) {
			return false
		}
	}
	return true
}

func samePublicOptionalHash(left, right *gen.Hash) bool {
	return left == nil && right == nil || left != nil && right != nil && strings.EqualFold(string(*left), string(*right))
}

func samePublicSelectorSet(left, right []gen.FunctionSelector) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, selector := range left {
		key := strings.ToLower(string(selector))
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	for _, selector := range right {
		if _, exists := seen[strings.ToLower(string(selector))]; !exists {
			return false
		}
	}
	return true
}

func proxyImplementationInteraction(
	detail query.ProxyDetail,
	model gen.ProxyDetails,
) (*gen.ProxyImplementationInteraction, error) {
	if detail.Status != string(gen.ProxyDetailStatusDetectedUnverified) &&
		detail.Status != string(gen.ProxyDetailStatusVerified) {
		return nil, nil
	}
	if detail.Confidence != string(gen.ProxyConfidenceHigh) &&
		detail.Confidence != string(gen.ProxyConfidenceVerified) {
		return nil, nil
	}
	if model.Mechanism == nil || model.Proxy == nil || model.Implementation == nil {
		return nil, nil
	}
	if model.Proxy.Address != model.Address || model.Proxy.Address == model.Implementation.Address {
		return nil, errors.New("proxy implementation interaction identity is inconsistent")
	}
	interaction := &gen.ProxyImplementationInteraction{
		Mechanism:      *model.Mechanism,
		Proxy:          *model.Proxy,
		Implementation: *model.Implementation,
		Pattern:        model.Pattern,
	}
	if *model.Mechanism == gen.ProxyMechanismBeacon {
		if model.Beacon == nil {
			return nil, errors.New("beacon interaction lacks a current beacon identity")
		}
		interaction.Beacon = model.Beacon
	}
	return interaction, nil
}

func (adapter proxyReaderAdapter) proxyUpgradeHistory(page query.ProxyUpgradePage) (gen.ProxyUpgradeHistory, error) {
	if len(page.Items) > 100 {
		return gen.ProxyUpgradeHistory{}, errors.New("proxy upgrade history exceeds the public bound")
	}
	address, err := proxyAddress(page.ProxyAddress)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, fmt.Errorf("invalid upgrade-history proxy address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, err
	}
	model := gen.ProxyUpgradeHistory{
		ProxyAddress: address,
		Snapshot:     snapshot,
		Coverage:     coverage,
		Items:        make([]gen.ProxyUpgrade, len(page.Items)),
	}
	for index := range page.Items {
		model.Items[index], err = proxyUpgrade(page.Items[index])
		if err != nil {
			return gen.ProxyUpgradeHistory{}, fmt.Errorf("invalid proxy upgrade: %w", err)
		}
		if greaterProxyQuantity(page.Items[index].BlockNumber, page.Snapshot.Number) {
			return gen.ProxyUpgradeHistory{}, errors.New("proxy upgrade is newer than its snapshot")
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) proxyInitializationHistory(page query.ProxyInitializationPage) (gen.ProxyInitializationHistory, error) {
	if len(page.Items) > 100 {
		return gen.ProxyInitializationHistory{}, errors.New("proxy initialization history exceeds the public bound")
	}
	address, err := proxyAddress(page.ContractAddress)
	if err != nil {
		return gen.ProxyInitializationHistory{}, fmt.Errorf("invalid initialization-history address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.ProxyInitializationHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.ProxyInitializationHistory{}, err
	}
	model := gen.ProxyInitializationHistory{
		ContractAddress: address,
		Snapshot:        snapshot,
		Coverage:        coverage,
		Items:           make([]gen.ProxyInitialization, len(page.Items)),
	}
	for index := range page.Items {
		item := page.Items[index]
		if greaterProxyQuantity(item.BlockNumber, page.Snapshot.Number) {
			return gen.ProxyInitializationHistory{}, errors.New("proxy initialization is newer than its snapshot")
		}
		blockNumber, parseErr := proxyQuantity(item.BlockNumber)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		logIndex, parseErr := proxyQuantity(item.LogIndex)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		version, parseErr := proxyQuantity(item.Version)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		blockHash, parseErr := proxyHash(item.BlockHash)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		transactionHash, parseErr := proxyHash(item.TransactionHash)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		implementation, parseErr := proxyHistoricalIdentity(item.Implementation)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		model.Items[index] = gen.ProxyInitialization{
			Version:         version,
			BlockNumber:     blockNumber,
			BlockHash:       blockHash,
			BlockTimestamp:  item.BlockTimestamp.UTC(),
			TransactionHash: transactionHash,
			LogIndex:        logIndex,
			Implementation:  implementation,
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) diamondCutHistory(page query.DiamondCutPage) (gen.DiamondCutHistory, error) {
	if len(page.Items) > 100 {
		return gen.DiamondCutHistory{}, errors.New("DiamondCut history exceeds the public bound")
	}
	address, err := proxyAddress(page.DiamondAddress)
	if err != nil {
		return gen.DiamondCutHistory{}, fmt.Errorf("invalid Diamond history address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.DiamondCutHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.DiamondCutHistory{}, err
	}
	model := gen.DiamondCutHistory{
		DiamondAddress: address, Snapshot: snapshot, Coverage: coverage,
		Items: make([]gen.DiamondCut, len(page.Items)),
	}
	for index, item := range page.Items {
		if greaterProxyQuantity(item.BlockNumber, page.Snapshot.Number) {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut is newer than its snapshot")
		}
		blockNumber, parseErr := proxyQuantity(item.BlockNumber)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		transactionIndex, parseErr := proxyQuantity(item.TransactionIndex)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		logIndex, parseErr := proxyQuantity(item.LogIndex)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		blockHash, parseErr := proxyHash(item.BlockHash)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		transactionHash, parseErr := proxyHash(item.TransactionHash)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		initAddress, parseErr := proxyAddress(item.InitAddress)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		if len(item.InitCalldata) > enrich.DiamondMaxRawReturnBytes*2+2 {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut init calldata exceeds the public bound")
		}
		if _, parseErr = ethrpc.ParseData(item.InitCalldata); parseErr != nil {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut init calldata is invalid")
		}
		cuts := make([]gen.DiamondFacetCut, len(item.Cuts))
		for cutIndex, cut := range item.Cuts {
			action := gen.DiamondFacetCutAction(cut.Action)
			if !action.Valid() || cut.CutIndex != cutIndex {
				return gen.DiamondCutHistory{}, errors.New("DiamondCut entry is invalid")
			}
			facetAddress, addressErr := proxyAddress(cut.FacetAddress)
			if addressErr != nil {
				return gen.DiamondCutHistory{}, addressErr
			}
			selectors := make([]gen.FunctionSelector, len(cut.Selectors))
			for selectorIndex, selector := range cut.Selectors {
				if !validFunctionSelector(selector) {
					return gen.DiamondCutHistory{}, errors.New("DiamondCut selector is invalid")
				}
				selectors[selectorIndex] = gen.FunctionSelector(selector)
			}
			cuts[cutIndex] = gen.DiamondFacetCut{
				CutIndex: cut.CutIndex, Action: action,
				FacetAddress: facetAddress, Selectors: selectors,
			}
		}
		model.Items[index] = gen.DiamondCut{
			BlockNumber: blockNumber, BlockHash: blockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: transactionHash,
			TransactionIndex: transactionIndex, LogIndex: logIndex,
			InitAddress: initAddress, InitCalldata: item.InitCalldata, Cuts: cuts,
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) proxySnapshot(snapshot query.ProxySnapshot) (gen.CatalogSnapshot, error) {
	number, err := proxyQuantity(snapshot.Number)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy snapshot number: %w", err)
	}
	hash, err := proxyHash(snapshot.Hash)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy snapshot hash: %w", err)
	}
	chainID, err := proxyQuantity(adapter.chainID)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy chain identity: %w", err)
	}
	return gen.CatalogSnapshot{ChainId: chainID, BlockNumber: number, BlockHash: hash}, nil
}

func proxyCurrentIdentity(identity *query.ProxyIdentity) (*gen.ProxyContractIdentity, error) {
	if identity == nil {
		return nil, nil
	}
	address, err := proxyAddress(identity.Address)
	if err != nil {
		return nil, err
	}
	codeHash, err := proxyHash(identity.CodeHash)
	if err != nil {
		return nil, err
	}
	state := gen.ProxyVerificationStateUnverified
	if identity.Verified {
		state = gen.ProxyVerificationStateVerified
	}
	model := &gen.ProxyContractIdentity{
		Address:           address,
		CodeHash:          codeHash,
		VerificationState: state,
	}
	if identity.ArtifactKind != "" {
		value := gen.ProxyArtifactKind(identity.ArtifactKind)
		if !value.Valid() {
			return nil, fmt.Errorf("invalid proxy artifact kind %q", identity.ArtifactKind)
		}
		model.ArtifactKind = &value
	}
	if identity.StandardVersion != "" {
		value := gen.ProxyContractIdentityStandardVersion(identity.StandardVersion)
		if !value.Valid() {
			return nil, fmt.Errorf("invalid proxy identity standard version %q", identity.StandardVersion)
		}
		model.StandardVersion = &value
	}
	return model, nil
}

func proxyHistoricalIdentity(identity query.ProxyIdentity) (gen.ProxyHistoricalIdentity, error) {
	address, err := proxyAddress(identity.Address)
	if err != nil {
		return gen.ProxyHistoricalIdentity{}, err
	}
	model := gen.ProxyHistoricalIdentity{Address: address}
	if identity.CodeHash != "" {
		codeHash, parseErr := proxyHash(identity.CodeHash)
		if parseErr != nil {
			return gen.ProxyHistoricalIdentity{}, parseErr
		}
		model.CodeHash = &codeHash
	}
	state := gen.ProxyVerificationStateUnverified
	if identity.Verified {
		state = gen.ProxyVerificationStateVerified
	}
	model.VerificationState = &state
	return model, nil
}

func proxyEvidence(evidence query.ProxyEvidence) (gen.ProxyRecognitionEvidence, error) {
	result := gen.ProxyEvidenceResult(evidence.Result)
	if !result.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid result %q", evidence.Result)
	}
	source := gen.ProxyEvidenceSource(evidence.Source)
	if !source.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid source %q", evidence.Source)
	}
	subject := gen.ProxyEvidenceSubject(evidence.Subject)
	if !subject.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid subject %q", evidence.Subject)
	}
	model := gen.ProxyRecognitionEvidence{Result: result, Source: source, Subject: subject}
	if evidence.Address != "" {
		value, err := proxyAddress(evidence.Address)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.Address = &value
	}
	if evidence.CodeHash != "" {
		value, err := proxyHash(evidence.CodeHash)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.CodeHash = &value
	}
	if evidence.BlockNumber != "" {
		value, err := proxyQuantity(evidence.BlockNumber)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.BlockNumber = &value
	}
	if evidence.BlockHash != "" {
		value, err := proxyHash(evidence.BlockHash)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.BlockHash = &value
	}
	return model, nil
}

func proxyManagement(management *query.ProxyManagement) (*gen.ProxyManagement, error) {
	if management == nil {
		return nil, nil
	}
	kind := gen.ProxyManagementKind(management.Kind)
	if !kind.Valid() {
		return nil, fmt.Errorf("invalid proxy management kind %q", management.Kind)
	}
	target, err := proxyCurrentIdentity(&management.Target)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy management target: %w", err)
	}
	model := &gen.ProxyManagement{Kind: kind, Target: *target}
	if management.AffectedProxyCount != "" {
		count, parseErr := proxyQuantity(management.AffectedProxyCount)
		if parseErr != nil {
			return nil, parseErr
		}
		model.AffectedProxyCount = &count
	}
	return model, nil
}

func proxyCoverage(coverage query.ProxyHistoryCoverage, snapshotNumber string) (gen.ProxyHistoryCoverage, error) {
	state := gen.ProxyHistoryCoverageState(coverage.State)
	if !state.Valid() {
		return gen.ProxyHistoryCoverage{}, fmt.Errorf("invalid proxy history coverage %q", coverage.State)
	}
	model := gen.ProxyHistoryCoverage{State: state}
	if coverage.FromBlock != "" {
		value, err := proxyQuantity(coverage.FromBlock)
		if err != nil {
			return gen.ProxyHistoryCoverage{}, err
		}
		model.FromBlock = &value
	}
	if coverage.ToBlock != "" {
		value, err := proxyQuantity(coverage.ToBlock)
		if err != nil {
			return gen.ProxyHistoryCoverage{}, err
		}
		model.ToBlock = &value
	}
	if coverage.FromBlock != "" && coverage.ToBlock != "" &&
		greaterProxyQuantity(coverage.FromBlock, coverage.ToBlock) {
		return gen.ProxyHistoryCoverage{}, errors.New("proxy history coverage range is inverted")
	}
	if coverage.ToBlock != "" && greaterProxyQuantity(coverage.ToBlock, snapshotNumber) {
		return gen.ProxyHistoryCoverage{}, errors.New("proxy history coverage exceeds its snapshot")
	}
	return model, nil
}

func proxyUpgrade(upgrade query.ProxyUpgrade) (gen.ProxyUpgrade, error) {
	changeType := gen.ProxyUpgradeChangeType(upgrade.ChangeType)
	if !changeType.Valid() {
		return gen.ProxyUpgrade{}, fmt.Errorf("invalid upgrade change type %q", upgrade.ChangeType)
	}
	evidenceType := gen.ProxyUpgradeEvidenceType(upgrade.EvidenceType)
	if !evidenceType.Valid() {
		return gen.ProxyUpgrade{}, fmt.Errorf("invalid upgrade evidence type %q", upgrade.EvidenceType)
	}
	blockNumber, err := proxyQuantity(upgrade.BlockNumber)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	blockHash, err := proxyHash(upgrade.BlockHash)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	newImplementation, err := proxyHistoricalIdentity(upgrade.NewImplementation)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	model := gen.ProxyUpgrade{
		ChangeType:        changeType,
		EvidenceType:      evidenceType,
		BlockNumber:       blockNumber,
		BlockHash:         blockHash,
		BlockTimestamp:    upgrade.BlockTimestamp.UTC(),
		NewImplementation: newImplementation,
	}
	if upgrade.OldImplementation != nil {
		value, parseErr := proxyHistoricalIdentity(*upgrade.OldImplementation)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.OldImplementation = &value
	}
	if upgrade.TransactionHash != "" {
		value, parseErr := proxyHash(upgrade.TransactionHash)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.TransactionHash = &value
	}
	if upgrade.LogIndex != "" {
		value, parseErr := proxyQuantity(upgrade.LogIndex)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.LogIndex = &value
	}
	if upgrade.EmitterAddress != "" {
		value, parseErr := proxyAddress(upgrade.EmitterAddress)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.EmitterAddress = &value
	}
	if upgrade.Beacon != nil {
		value, parseErr := proxyHistoricalIdentity(*upgrade.Beacon)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.Beacon = &value
	}
	if upgrade.Management != nil {
		kind := gen.ProxyManagementKind(upgrade.Management.Kind)
		if !kind.Valid() {
			return gen.ProxyUpgrade{}, fmt.Errorf("invalid historical management kind %q", upgrade.Management.Kind)
		}
		target, parseErr := proxyHistoricalIdentity(upgrade.Management.Target)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.Management = &gen.ProxyHistoricalManagement{Kind: kind, Target: target}
	}
	return model, nil
}

func proxyAddress(value string) (gen.Address, error) {
	address, err := query.ChecksumAddress(value)
	return gen.Address(address), err
}

func proxyHash(value string) (gen.Hash, error) {
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return "", err
	}
	return gen.Hash(hash.Hex()), nil
}

func proxyQuantity(value string) (gen.Quantity, error) {
	if _, err := proxyQuantityInteger(value); err != nil {
		return "", err
	}
	return gen.Quantity(value), nil
}

func proxyQuantityInteger(value string) (*big.Int, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return nil, errors.New("quantity is not a canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return nil, errors.New("quantity is not a canonical decimal")
		}
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 || integer.BitLen() > 256 {
		return nil, errors.New("quantity exceeds uint256")
	}
	return integer, nil
}

func greaterProxyQuantity(left, right string) bool {
	leftInteger, leftOK := new(big.Int).SetString(left, 10)
	rightInteger, rightOK := new(big.Int).SetString(right, 10)
	return leftOK && rightOK && leftInteger.Cmp(rightInteger) > 0
}
