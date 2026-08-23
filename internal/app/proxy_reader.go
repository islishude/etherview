package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	for _, current := range []struct {
		role     string
		identity *query.ProxyIdentity
	}{
		{role: "proxy", identity: detail.Proxy},
		{role: "admin", identity: detail.Admin},
		{role: "beacon", identity: detail.Beacon},
	} {
		if current.identity != nil && current.identity.ArtifactResolution == "code_hash" {
			return gen.ProxyDetails{}, fmt.Errorf("%s identity cannot reuse a code-hash artifact", current.role)
		}
	}
	if detail.Management != nil && detail.Management.Target.ArtifactResolution == "code_hash" {
		return gen.ProxyDetails{}, errors.New("management identity cannot reuse a code-hash artifact")
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
	if model.ImmutableArgsDecoding, err = cwiaImmutableArgsDecoding(detail.ImmutableArgsDecoding); err != nil {
		return gen.ProxyDetails{}, err
	}
	if model.Mechanism != nil && *model.Mechanism == gen.ProxyMechanismCwia {
		if model.ImmutableArgs == nil || model.ImmutableArgsDecoding == nil {
			return gen.ProxyDetails{}, errors.New("CWIA proxy detail lacks immutable argument state")
		}
	} else if model.ImmutableArgsDecoding != nil {
		return gen.ProxyDetails{}, errors.New("non-CWIA proxy detail carries CWIA argument decoding")
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

func cwiaImmutableArgsDecoding(
	value *query.CWIAImmutableArgsDecoding,
) (*gen.CWIAImmutableArgsDecoding, error) {
	if value == nil {
		return nil, nil
	}
	status := gen.CWIAImmutableArgsDecodingStatus(value.Status)
	if !status.Valid() || len(value.Arguments) > 64 {
		return nil, errors.New("CWIA immutable argument decoding is outside the public contract")
	}
	model := &gen.CWIAImmutableArgsDecoding{
		Status: status, Arguments: make([]gen.CWIAImmutableArgValue, len(value.Arguments)),
	}
	if value.Reason != "" {
		reason := gen.CWIAImmutableArgsDecodingReason(value.Reason)
		if !reason.Valid() {
			return nil, errors.New("CWIA immutable argument decoding reason is invalid")
		}
		model.Reason = &reason
	}
	if value.Schema != nil {
		if value.Schema.Version != 2 || value.Schema.Source != "solidity_ast" ||
			value.Schema.Encoding != "solady-cwia-offsets" ||
			len(value.Schema.Fields) == 0 || len(value.Schema.Fields) > 64 {
			return nil, errors.New("CWIA immutable argument schema is invalid")
		}
		digest, err := proxyHash(value.Schema.SHA256)
		if err != nil {
			return nil, errors.New("CWIA immutable argument schema digest is invalid")
		}
		helperDigest, err := proxyHash(value.Schema.HelperSHA256)
		if err != nil {
			return nil, errors.New("CWIA helper source digest is invalid")
		}
		schema := &gen.CWIAImmutableArgSchema{
			Version:      gen.CWIAImmutableArgSchemaVersion(value.Schema.Version),
			Encoding:     gen.CWIAImmutableArgSchemaEncoding(value.Schema.Encoding),
			Source:       gen.CWIAImmutableArgSchemaSource(value.Schema.Source),
			HelperSha256: helperDigest, Sha256: digest,
			Fields: make([]gen.CWIAImmutableArgField, len(value.Schema.Fields)),
		}
		if !schema.Version.Valid() || !schema.Source.Valid() || !schema.Encoding.Valid() {
			return nil, errors.New("CWIA immutable argument schema version or encoding is invalid")
		}
		for index, field := range value.Schema.Fields {
			fieldType := gen.CWIAImmutableArgType(field.Type)
			role := gen.CWIAImmutableArgFieldRole(field.Role)
			sizeKind := gen.CWIAImmutableArgSizeKind(field.Size.Kind)
			if field.Name == "" || len(field.Name) > 64 || !fieldType.Valid() || !role.Valid() ||
				!sizeKind.Valid() || field.Offset < 0 || field.Offset > enrich.MaxCloneImmutableArgs ||
				len(field.Getters) > 16 {
				return nil, errors.New("CWIA immutable argument schema field is invalid")
			}
			size := gen.CWIAImmutableArgSize{Kind: sizeKind}
			if field.Size.Bytes != nil {
				bytes := *field.Size.Bytes
				size.Bytes = &bytes
			}
			if field.Size.Field != "" {
				lengthField := field.Size.Field
				size.Field = &lengthField
			}
			if field.Size.Multiplier != 0 {
				multiplier := gen.CWIAImmutableArgSizeMultiplier(field.Size.Multiplier)
				if !multiplier.Valid() {
					return nil, errors.New("CWIA immutable argument schema multiplier is invalid")
				}
				size.Multiplier = &multiplier
			}
			schema.Fields[index] = gen.CWIAImmutableArgField{
				Name: field.Name, Type: fieldType, Offset: field.Offset, Role: role,
				Getters: append([]string(nil), field.Getters...), Size: size,
			}
		}
		model.Schema = schema
	}
	if value.SchemaResolution != "" {
		resolution := gen.CWIASchemaResolution(value.SchemaResolution)
		if !resolution.Valid() {
			return nil, errors.New("CWIA schema resolution is invalid")
		}
		model.SchemaResolution = &resolution
	}
	for index, argument := range value.Arguments {
		argumentType := gen.CWIAImmutableArgType(argument.Type)
		if argument.Name == "" || len(argument.Name) > 64 || !argumentType.Valid() ||
			argument.Offset < 0 || argument.Length < 0 ||
			argument.Offset+argument.Length > enrich.MaxCloneImmutableArgs {
			return nil, errors.New("CWIA immutable argument value is invalid")
		}
		var publicValue any
		switch typed := argument.Value.(type) {
		case string:
			publicValue = typed
		case []string:
			publicValue = append([]string(nil), typed...)
		default:
			return nil, errors.New("CWIA immutable argument value type is invalid")
		}
		model.Arguments[index] = gen.CWIAImmutableArgValue{
			Name: argument.Name, Type: argumentType, Offset: argument.Offset,
			Length: argument.Length, Value: publicValue,
		}
	}
	switch status {
	case gen.CWIAImmutableArgsDecodingStatusDecoded:
		if model.Reason != nil || model.Schema == nil || model.SchemaResolution == nil ||
			len(model.Arguments) != len(model.Schema.Fields) {
			return nil, errors.New("decoded CWIA immutable arguments have an invalid shape")
		}
		for index, argument := range model.Arguments {
			field := model.Schema.Fields[index]
			if argument.Name != field.Name || argument.Type != field.Type || argument.Offset != field.Offset {
				return nil, errors.New("decoded CWIA immutable arguments disagree with their schema")
			}
		}
	case gen.CWIAImmutableArgsDecodingStatusSchemaUnavailable,
		gen.CWIAImmutableArgsDecodingStatusSchemaInvalid:
		if model.Reason == nil || model.Schema != nil || model.SchemaResolution != nil || len(model.Arguments) != 0 {
			return nil, errors.New("unavailable CWIA immutable arguments have an invalid shape")
		}
	case gen.CWIAImmutableArgsDecodingStatusDataInvalid:
		if model.Reason == nil || model.Schema == nil || model.SchemaResolution == nil || len(model.Arguments) != 0 {
			return nil, errors.New("invalid CWIA immutable argument data has an invalid shape")
		}
	}
	return model, nil
}
