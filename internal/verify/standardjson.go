package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"
)

const (
	defaultStandardJSONBytes       = 5 << 20
	maxStandardJSONSources         = 1024
	maxStandardJSONSourceNameBytes = 384
	maxStandardJSONSelectorEntries = 4096
	maxStandardJSONOutputEntries   = 4096
	maxStandardJSONOutputNameBytes = 256
)

var (
	solidityContractNamePattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)
	vyperContractNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

var solidityRequiredOutputs = []string{
	"abi",
	"metadata",
	"evm.bytecode.object",
	"evm.bytecode.linkReferences",
	"evm.deployedBytecode.object",
	"evm.deployedBytecode.linkReferences",
	"evm.deployedBytecode.immutableReferences",
}

var vyperRequiredOutputs = []string{
	"abi",
	"metadata",
	"layout",
	"evm.bytecode.object",
	"evm.deployedBytecode.object",
}

var vyperLegacyRequiredOutputs = []string{
	"abi",
	"evm.bytecode.object",
	"evm.deployedBytecode.object",
}

var vyperV040RequiredOutputs = []string{
	"abi",
	"metadata",
	"evm.bytecode.object",
	"evm.deployedBytecode.object",
}

// PrepareStandardJSON validates an inline Standard JSON compiler input and
// returns a fresh, deterministic encoding whose outputSelection is replaced
// by the bounded fields verification needs for the exact target. Caller-owned
// bytes are not mutated and code-generation settings are preserved.
func PrepareStandardJSON(
	input json.RawMessage,
	language Language,
	compilerVersion string,
	contractIdentifier string,
	maxInputBytes int,
) (json.RawMessage, error) {
	document, _, err := validateStandardJSON(
		input,
		language,
		compilerVersion,
		contractIdentifier,
		maxInputBytes,
	)
	if err != nil {
		return nil, err
	}
	if maxInputBytes <= 0 {
		maxInputBytes = defaultStandardJSONBytes
	}

	prepared, err := json.Marshal(document)
	if err != nil {
		return nil, errors.New("standard JSON cannot be encoded")
	}
	if len(prepared) > maxInputBytes {
		return nil, fmt.Errorf("normalized standard JSON exceeds %d bytes", maxInputBytes)
	}
	return json.RawMessage(prepared), nil
}

func validateStandardJSON(
	input json.RawMessage,
	language Language,
	compilerVersion string,
	contractIdentifier string,
	maxInputBytes int,
) (map[string]any, map[string]any, error) {
	if maxInputBytes <= 0 {
		maxInputBytes = defaultStandardJSONBytes
	}
	if len(input) == 0 || len(input) > maxInputBytes || !jsonObject(input) {
		return nil, nil, fmt.Errorf("standard JSON must be an object of at most %d bytes", maxInputBytes)
	}

	document, err := decodeStandardJSONObject(input)
	if err != nil {
		if errors.Is(err, errJSONDuplicateKey) {
			return nil, nil, errors.New("standard JSON contains a duplicate object key")
		}
		return nil, nil, errors.New("standard JSON must be one object")
	}
	expectedLanguage, err := standardJSONLanguage(language)
	if err != nil {
		return nil, nil, err
	}
	actualLanguage, ok := document["language"].(string)
	if !ok || actualLanguage != expectedLanguage {
		return nil, nil, fmt.Errorf("standard JSON language must be %s", expectedLanguage)
	}
	if err := validateStandardJSONTopLevel(document, language); err != nil {
		return nil, nil, err
	}

	targetSource, targetName, err := parseStandardJSONContractIdentifier(contractIdentifier, language)
	if err != nil {
		return nil, nil, err
	}
	var compilerVyperVersion vyperVersion
	if language == LanguageVyper {
		var versionOK bool
		compilerVyperVersion, versionOK = parseVyperVersion(compilerVersion)
		if !versionOK {
			return nil, nil, errors.New("vyper compiler version must be semantic")
		}
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > maxStandardJSONSources {
		return nil, nil, fmt.Errorf("standard JSON sources must be a non-empty object with at most %d entries", maxStandardJSONSources)
	}
	for sourceName, sourceValue := range sources {
		if !validStandardJSONSourceName(sourceName) {
			return nil, nil, errors.New("standard JSON source name is invalid")
		}
		if language == LanguageVyper {
			if !validVyperStandardJSONPath(sourceName) {
				return nil, nil, errors.New("vyper source path must be a clean relative POSIX path")
			}
			extension := path.Ext(sourceName)
			if compilerVyperVersion.atLeast(0, 4, 0) {
				if extension != ".vy" && extension != ".vyi" {
					return nil, nil, errors.New("vyper sources must use .vy or .vyi filenames")
				}
			} else if extension != ".vy" {
				return nil, nil, errors.New("vyper before 0.4.0 requires .vy sources")
			}
		}
		source, ok := sourceValue.(map[string]any)
		if !ok || len(source) > 2 {
			return nil, nil, errors.New("standard JSON sources must be objects")
		}
		content, hasContent := source["content"]
		_, hasURLs := source["urls"]
		if _, ok := content.(string); !hasContent || !ok || hasURLs {
			return nil, nil, errors.New("every standard JSON source must contain inline content and no URLs")
		}
		for key := range source {
			if key != "content" && key != "keccak256" {
				return nil, nil, errors.New("standard JSON source contains an unsupported field")
			}
		}
		if checksum, exists := source["keccak256"]; exists {
			value, ok := checksum.(string)
			if !ok || !fixedHex(value, 32) {
				return nil, nil, errors.New("standard JSON source checksum is invalid")
			}
		}
	}
	if _, ok := sources[targetSource]; !ok {
		return nil, nil, errors.New("contract identifier source is not present in standard JSON")
	}
	if language == LanguageVyper {
		if path.Ext(targetSource) != ".vy" {
			return nil, nil, errors.New("vyper verification target must be a .vy source")
		}
		if _, exists := document["integrity"]; exists && !compilerVyperVersion.atLeast(0, 4, 0) {
			return nil, nil, errors.New("vyper integrity requires compiler 0.4.0 or newer")
		}
		base := strings.TrimSuffix(path.Base(targetSource), path.Ext(targetSource))
		if base == "" || targetName != base {
			return nil, nil, errors.New("vyper contract name must match its source filename")
		}
	}

	settings := make(map[string]any)
	if rawSettings, exists := document["settings"]; exists {
		var ok bool
		settings, ok = rawSettings.(map[string]any)
		if !ok {
			return nil, nil, errors.New("standard JSON settings must be an object")
		}
	}
	if language == LanguageVyper {
		if compilerVyperVersion.atLeast(0, 4, 0) {
			if rawSearchPaths, exists := settings["search_paths"]; exists {
				searchPaths, ok := rawSearchPaths.([]any)
				if !ok || len(searchPaths) != 1 || searchPaths[0] != "." {
					return nil, nil, errors.New("vyper search paths must contain only the virtual root")
				}
			}
			// Modern Vyper's in-memory JSONInputBundle needs the virtual root
			// to resolve even the target source. It never calls the host
			// filesystem for a supplied source.
			settings["search_paths"] = []string{"."}
		} else if _, exists := settings["search_paths"]; exists {
			return nil, nil, errors.New("vyper search paths require compiler 0.4.0 or newer")
		}
		if rawMetadata, exists := settings["bytecodeMetadata"]; exists {
			if _, ok := rawMetadata.(bool); !ok {
				return nil, nil, errors.New("vyper bytecodeMetadata must be boolean")
			}
		}
		if err := validateVyperInlineInputs(document, sources, compilerVyperVersion); err != nil {
			return nil, nil, err
		}
	} else if rawMetadata, exists := settings["metadata"]; exists {
		metadata, ok := rawMetadata.(map[string]any)
		if !ok {
			return nil, nil, errors.New("solidity metadata setting must be an object")
		}
		if rawAppendCBOR, exists := metadata["appendCBOR"]; exists {
			if _, ok := rawAppendCBOR.(bool); !ok {
				return nil, nil, errors.New("solidity metadata appendCBOR setting must be boolean")
			}
		}
	}
	switch language {
	case LanguageSolidity:
		if err := mergeSolidityOutputSelection(settings, targetSource, targetName); err != nil {
			return nil, nil, err
		}
	case LanguageVyper:
		if err := mergeVyperOutputSelection(settings, targetSource, sources, compilerVyperVersion); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, errors.New("language must be solidity or vyper")
	}
	document["settings"] = settings
	return document, settings, nil
}

func validateStandardJSONTopLevel(document map[string]any, language Language) error {
	allowed := map[string]struct{}{
		"language": {},
		"sources":  {},
		"settings": {},
	}
	switch language {
	case LanguageSolidity:
		allowed["auxiliaryInput"] = struct{}{}
	case LanguageVyper:
		allowed["interfaces"] = struct{}{}
		allowed["storage_layout_overrides"] = struct{}{}
		allowed["integrity"] = struct{}{}
	}
	for key := range document {
		if _, ok := allowed[key]; !ok {
			return errors.New("standard JSON contains an unsupported top-level field")
		}
	}
	if auxiliaryInput, exists := document["auxiliaryInput"]; exists {
		if _, ok := auxiliaryInput.(map[string]any); !ok {
			return errors.New("solidity auxiliaryInput must be an object")
		}
	}
	if integrity, exists := document["integrity"]; exists {
		value, ok := integrity.(string)
		if !ok || value == "" || len(value) > maxStandardJSONOutputNameBytes {
			return errors.New("vyper integrity is invalid")
		}
	}
	return nil
}

func validateVyperInlineInputs(
	document map[string]any,
	sources map[string]any,
	compilerVersion vyperVersion,
) error {
	if rawInterfaces, exists := document["interfaces"]; exists {
		interfaces, ok := rawInterfaces.(map[string]any)
		if !ok || len(interfaces) > maxStandardJSONSources {
			return errors.New("vyper interfaces must be a bounded object")
		}
		totalNamespaces := 0
		totalABIEntries := 0
		for name, rawInterface := range interfaces {
			if !validVyperStandardJSONPath(name) {
				return errors.New("vyper interface path must be a clean relative POSIX path")
			}
			extension := path.Ext(name)
			namespaces, abiEntries, normalized, err := normalizeVyperInterface(
				rawInterface,
				extension,
				compilerVersion,
			)
			if err != nil {
				return err
			}
			totalNamespaces += namespaces
			if totalNamespaces > maxStandardJSONSources {
				return errors.New("vyper interfaces expand to too many namespaces")
			}
			totalABIEntries += abiEntries
			if totalABIEntries > maxStandardJSONOutputEntries {
				return errors.New("vyper interface ABIs have too many entries")
			}
			interfaces[name] = normalized
		}
	}
	if rawOverrides, exists := document["storage_layout_overrides"]; exists {
		if !compilerVersion.atLeast(0, 4, 1) {
			return errors.New("vyper storage layout overrides require compiler 0.4.1 or newer")
		}
		overrides, ok := rawOverrides.(map[string]any)
		if !ok || len(overrides) > maxStandardJSONSources {
			return errors.New("vyper storage layout overrides must be a bounded object")
		}
		for source, rawOverride := range overrides {
			if !validVyperStandardJSONPath(source) {
				return errors.New("vyper storage layout override target path must be a clean relative POSIX path")
			}
			if _, exists := sources[source]; !exists {
				return errors.New("vyper storage layout override target is not a source")
			}
			override, ok := rawOverride.(map[string]any)
			if !ok || len(override) > maxStandardJSONOutputEntries {
				return errors.New("vyper storage layout override must be an object")
			}
			// Vyper 0.4.2 moved each override into an inline JSON input
			// whose single key is its virtual filename. Vyper 0.4.1 consumes
			// the layout object directly and therefore has no inner path.
			if compilerVersion.atLeast(0, 4, 2) {
				if len(override) != 1 {
					return errors.New("vyper 0.4.2 or newer storage layout override must contain one inline file")
				}
				for overridePath, rawLayout := range override {
					if !validVyperStandardJSONPath(overridePath) {
						return errors.New("vyper storage layout override file path must be a clean relative POSIX path")
					}
					layout, ok := rawLayout.(map[string]any)
					if !ok || len(layout) > maxStandardJSONOutputEntries {
						return errors.New("vyper storage layout override file must contain an object")
					}
				}
			}
		}
	}
	return nil
}

func normalizeVyperInterface(
	rawInterface any,
	extension string,
	compilerVersion vyperVersion,
) (int, int, any, error) {
	modern := compilerVersion.atLeast(0, 4, 0)
	if extension == ".vyi" && !modern {
		return 0, 0, nil, errors.New("vyper before 0.4.0 does not support .vyi interfaces")
	}
	if extension != ".vy" && extension != ".vyi" && extension != ".json" {
		if modern {
			return 0, 0, nil, errors.New("vyper interfaces must use .vy, .vyi, or .json filenames")
		}
		return 0, 0, nil, errors.New("vyper interfaces must use .vy or .json filenames")
	}

	if abi, ok := rawInterface.([]any); ok {
		if extension != ".json" {
			return 0, 0, nil, errors.New("vyper interface ABI must use a .json filename")
		}
		return 1, len(abi), map[string]any{"abi": abi}, nil
	}
	entry, ok := rawInterface.(map[string]any)
	if !ok || len(entry) == 0 {
		return 0, 0, nil, errors.New("vyper interface must be an inline object")
	}
	if _, hasURLs := entry["urls"]; hasURLs {
		return 0, 0, nil, errors.New("vyper interface must contain inline content or ABI and no URLs")
	}

	if rawContractTypes, hasContractTypes := entry["contractTypes"]; hasContractTypes {
		if modern {
			return 0, 0, nil, errors.New("vyper 0.4.0 or newer does not support EthPM contractTypes interfaces")
		}
		if extension != ".json" {
			return 0, 0, nil, errors.New("vyper EthPM contractTypes interface must use a .json filename")
		}
		contractTypes, ok := rawContractTypes.(map[string]any)
		if !ok || len(contractTypes) == 0 || len(contractTypes) > maxStandardJSONSources {
			return 0, 0, nil, errors.New("vyper EthPM contractTypes must be a bounded non-empty object")
		}
		normalizedTypes := make(map[string]any, len(contractTypes))
		totalABIEntries := 0
		for contractName, rawContractType := range contractTypes {
			if !vyperContractNamePattern.MatchString(contractName) {
				return 0, 0, nil, errors.New("vyper EthPM contract type name is invalid")
			}
			contractType, ok := rawContractType.(map[string]any)
			if !ok {
				return 0, 0, nil, errors.New("vyper EthPM contract type must be an object")
			}
			abi, ok := contractType["abi"].([]any)
			if !ok {
				return 0, 0, nil, errors.New("vyper EthPM contract type ABI is invalid")
			}
			totalABIEntries += len(abi)
			if totalABIEntries > maxStandardJSONOutputEntries {
				return 0, 0, nil, errors.New("vyper EthPM contract type ABIs have too many entries")
			}
			normalizedTypes[contractName] = map[string]any{"abi": abi}
		}
		return len(normalizedTypes), totalABIEntries, map[string]any{"contractTypes": normalizedTypes}, nil
	}

	content, hasContent := entry["content"]
	abi, hasABI := entry["abi"]
	if hasContent == hasABI || len(entry) != 1 {
		return 0, 0, nil, errors.New("vyper interface must contain exactly one inline content or ABI value and no URLs")
	}
	if hasContent {
		if extension != ".vy" && extension != ".vyi" {
			return 0, 0, nil, errors.New("vyper interface content must use a .vy or .vyi filename")
		}
		if _, ok := content.(string); !ok {
			return 0, 0, nil, errors.New("vyper interface content is invalid")
		}
		return 1, 0, entry, nil
	}
	if extension != ".json" {
		return 0, 0, nil, errors.New("vyper interface ABI must use a .json filename")
	}
	values, ok := abi.([]any)
	if !ok {
		return 0, 0, nil, errors.New("vyper interface ABI is invalid")
	}
	return 1, len(values), entry, nil
}

func decodeStandardJSONObject(input []byte) (map[string]any, error) {
	if err := validateUniqueJSON(input); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, errors.New("not an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	return document, nil
}

func standardJSONLanguage(language Language) (string, error) {
	switch language {
	case LanguageSolidity:
		return "Solidity", nil
	case LanguageVyper:
		return "Vyper", nil
	default:
		return "", errors.New("language must be solidity or vyper")
	}
}

func parseStandardJSONContractIdentifier(identifier string, language Language) (string, string, error) {
	separator := strings.LastIndex(identifier, ":")
	if len(identifier) > 512 || separator <= 0 || separator == len(identifier)-1 {
		return "", "", errors.New("contract identifier must be source:name")
	}
	source, name := identifier[:separator], identifier[separator+1:]
	if !validStandardJSONSourceName(source) {
		return "", "", errors.New("contract identifier source is invalid")
	}
	pattern := solidityContractNamePattern
	if language == LanguageVyper {
		pattern = vyperContractNamePattern
	}
	if !pattern.MatchString(name) {
		return "", "", errors.New("contract identifier name is invalid")
	}
	return source, name, nil
}

func validStandardJSONSourceName(name string) bool {
	if len(name) == 0 || name == "*" || len(name) > maxStandardJSONSourceNameBytes || strings.TrimSpace(name) != name {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validVyperStandardJSONPath(name string) bool {
	return validStandardJSONSourceName(name) &&
		name != "." && name != ".." &&
		!path.IsAbs(name) &&
		!strings.HasPrefix(name, "../") &&
		!strings.ContainsRune(name, '\\') &&
		path.Clean(name) == name
}

func mergeSolidityOutputSelection(settings map[string]any, targetSource, targetName string) error {
	rawSelection, exists := settings["outputSelection"]
	if exists {
		outer, ok := rawSelection.(map[string]any)
		if !ok || len(outer) > maxStandardJSONSelectorEntries {
			return errors.New("solidity outputSelection is invalid")
		}
		totalOutputs := 0
		for sourceSelector, rawContracts := range outer {
			if !validStandardJSONSelector(sourceSelector, maxStandardJSONSourceNameBytes) {
				return errors.New("solidity outputSelection source selector is invalid")
			}
			contracts, ok := rawContracts.(map[string]any)
			if !ok || len(contracts) > maxStandardJSONSelectorEntries {
				return errors.New("solidity outputSelection is invalid")
			}
			for contractSelector, rawOutputs := range contracts {
				if !validStandardJSONContractSelector(contractSelector) {
					return errors.New("solidity outputSelection contract selector is invalid")
				}
				outputs, err := standardJSONOutputNames(rawOutputs)
				if err != nil {
					return errors.New("solidity outputSelection is invalid")
				}
				totalOutputs += len(outputs)
				if totalOutputs > maxStandardJSONOutputEntries {
					return errors.New("solidity outputSelection has too many entries")
				}
			}
		}
	}
	// outputSelection does not affect compiler semantics, so the server owns it.
	// Retaining caller-selected AST/IR/wildcard outputs would let a tiny public
	// request amplify into a very large compiler result.
	settings["outputSelection"] = map[string]map[string][]string{
		targetSource: {
			targetName: append([]string(nil), solidityRequiredOutputs...),
		},
	}
	return nil
}

func mergeVyperOutputSelection(
	settings map[string]any,
	targetSource string,
	sources map[string]any,
	compilerVersion vyperVersion,
) error {
	rawSelection, exists := settings["outputSelection"]
	outer := make(map[string]any)
	if exists {
		var ok bool
		outer, ok = rawSelection.(map[string]any)
		if !ok || len(outer) > maxStandardJSONSelectorEntries {
			return errors.New("vyper outputSelection is invalid")
		}
	}
	totalOutputs := 0
	for selector, rawValue := range outer {
		if !validStandardJSONSelector(selector, maxStandardJSONSourceNameBytes) {
			return errors.New("vyper outputSelection selector is invalid")
		}
		switch rawValue := rawValue.(type) {
		case []any:
			outputs, err := standardJSONOutputNames(rawValue)
			if err != nil {
				return errors.New("vyper outputSelection is invalid")
			}
			totalOutputs += len(outputs)
		case map[string]any:
			if len(rawValue) > maxStandardJSONSelectorEntries {
				return errors.New("vyper outputSelection has too many selectors")
			}
			for contractSelector, rawOutputs := range rawValue {
				if !validVyperContractSelector(contractSelector) {
					return errors.New("vyper outputSelection contract selector is invalid")
				}
				outputs, err := standardJSONOutputNames(rawOutputs)
				if err != nil {
					return errors.New("vyper outputSelection is invalid")
				}
				totalOutputs += len(outputs)
			}
		default:
			return errors.New("vyper outputSelection is invalid")
		}
		if totalOutputs > maxStandardJSONOutputEntries {
			return errors.New("vyper outputSelection has too many entries")
		}
	}
	requiredOutputs := vyperRequiredOutputsForVersion(compilerVersion)
	canonicalSelection := make(map[string]any)
	// Vyper before 0.4.0 iterates every source and indexes its output format
	// entry even when the caller intends to compile one target. A minimal
	// non-empty userdoc selection avoids the old formatter's missing-data bug
	// without requesting unrelated bytecode, AST, IR, or metadata artifacts.
	if !compilerVersion.atLeast(0, 4, 0) {
		for source := range sources {
			canonicalSelection[source] = []string{"userdoc"}
		}
	}
	canonicalSelection[targetSource] = append([]string(nil), requiredOutputs...)
	settings["outputSelection"] = canonicalSelection
	return nil
}

func vyperRequiredOutputsForVersion(version vyperVersion) []string {
	switch {
	case !version.atLeast(0, 3, 10):
		// The vyper-json translation table did not expose metadata before
		// 0.3.10, although the lower-level compiler had such an output.
		return vyperLegacyRequiredOutputs
	case !version.atLeast(0, 4, 1):
		return vyperV040RequiredOutputs
	default:
		return vyperRequiredOutputs
	}
}

func standardJSONOutputNames(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok || len(values) > maxStandardJSONOutputEntries {
		return nil, errors.New("outputs must be an array")
	}
	outputs := make([]string, 0, len(values))
	for _, value := range values {
		output, ok := value.(string)
		if !ok || len(output) == 0 || len(output) > maxStandardJSONOutputNameBytes || strings.TrimSpace(output) != output {
			return nil, errors.New("output name is invalid")
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func validStandardJSONSelector(selector string, maximum int) bool {
	if selector == "*" {
		return true
	}
	if len(selector) == 0 || len(selector) > maximum || strings.TrimSpace(selector) != selector {
		return false
	}
	for _, character := range selector {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validStandardJSONContractSelector(selector string) bool {
	return selector == "" || selector == "*" || solidityContractNamePattern.MatchString(selector)
}

func validVyperContractSelector(selector string) bool {
	return selector == "" || selector == "*" || vyperContractNamePattern.MatchString(selector)
}
