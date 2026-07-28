package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
)

var verifierRequiredOutputs = []string{
	"abi",
	"metadata",
	"devdoc",
	"userdoc",
	"storageLayout",
	"evm.bytecode.object",
	"evm.bytecode.sourceMap",
	"evm.bytecode.linkReferences",
	"evm.deployedBytecode.object",
	"evm.deployedBytecode.sourceMap",
	"evm.deployedBytecode.linkReferences",
	"evm.deployedBytecode.immutableReferences",
	"evm.methodIdentifiers",
}

type MultipartRequest struct {
	Language         Language
	Sources          map[string]string
	EVMVersion       string
	OptimizationRuns *int
	Libraries        map[string]string
}

// PrepareVerifierStandardJSON validates an all-candidate inline input and
// replaces caller output selection with the bounded verifier-owned selection.
func PrepareVerifierStandardJSON(
	input json.RawMessage,
	language Language,
	compilerVersion string,
	maxInputBytes int,
) (json.RawMessage, error) {
	if maxInputBytes <= 0 {
		maxInputBytes = defaultStandardJSONBytes
	}
	if len(input) == 0 || len(input) > maxInputBytes || !jsonObject(input) {
		return nil, fmt.Errorf("standard JSON must be an object of at most %d bytes", maxInputBytes)
	}
	document, err := decodeStandardJSONObject(input)
	if err != nil {
		if errors.Is(err, errJSONDuplicateKey) {
			return nil, errors.New("standard JSON contains a duplicate object key")
		}
		return nil, errors.New("standard JSON must be one object")
	}
	expected, err := standardJSONLanguage(language)
	if err != nil {
		return nil, err
	}
	if actual, ok := document["language"].(string); !ok || actual != expected {
		return nil, fmt.Errorf("standard JSON language must be %s", expected)
	}
	if err := validateStandardJSONTopLevel(document, language); err != nil {
		return nil, err
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > maxStandardJSONSources {
		return nil, fmt.Errorf("standard JSON sources must contain between 1 and %d entries", maxStandardJSONSources)
	}
	var vyperCompiler vyperVersion
	if language == LanguageVyper {
		var valid bool
		vyperCompiler, valid = parseVyperVersion(compilerVersion)
		if !valid {
			return nil, errors.New("vyper compiler version must be semantic")
		}
	}
	for sourceName, rawSource := range sources {
		if !validStandardJSONSourceName(sourceName) {
			return nil, errors.New("standard JSON source name is invalid")
		}
		if language == LanguageVyper {
			if !validVyperStandardJSONPath(sourceName) {
				return nil, errors.New("vyper source path must be a clean relative POSIX path")
			}
			extension := path.Ext(sourceName)
			if vyperCompiler.atLeast(0, 4, 0) {
				if extension != ".vy" && extension != ".vyi" {
					return nil, errors.New("vyper sources must use .vy or .vyi filenames")
				}
			} else if extension != ".vy" {
				return nil, errors.New("vyper before 0.4.0 requires .vy sources")
			}
		}
		source, ok := rawSource.(map[string]any)
		if !ok || len(source) > 2 {
			return nil, errors.New("standard JSON sources must be inline objects")
		}
		content, hasContent := source["content"]
		if _, valid := content.(string); !hasContent || !valid {
			return nil, errors.New("every standard JSON source must contain inline content")
		}
		if _, hasURLs := source["urls"]; hasURLs {
			return nil, errors.New("standard JSON URL sources are forbidden")
		}
		for key := range source {
			if key != "content" && key != "keccak256" {
				return nil, errors.New("standard JSON source contains an unsupported field")
			}
		}
		if checksum, exists := source["keccak256"]; exists {
			value, valid := checksum.(string)
			if !valid || !fixedHex(value, 32) {
				return nil, errors.New("standard JSON source checksum is invalid")
			}
		}
	}
	settings := make(map[string]any)
	if rawSettings, exists := document["settings"]; exists {
		var valid bool
		settings, valid = rawSettings.(map[string]any)
		if !valid {
			return nil, errors.New("standard JSON settings must be an object")
		}
	}
	if err := validateCallerOutputSelection(settings, language); err != nil {
		return nil, err
	}
	switch language {
	case LanguageSolidity, LanguageYul:
		if rawMetadata, exists := settings["metadata"]; exists {
			metadata, valid := rawMetadata.(map[string]any)
			if !valid {
				return nil, errors.New("solidity metadata settings must be an object")
			}
			if rawAppend, exists := metadata["appendCBOR"]; exists {
				if _, valid := rawAppend.(bool); !valid {
					return nil, errors.New("solidity metadata appendCBOR must be boolean")
				}
			}
		}
		settings["outputSelection"] = map[string]map[string][]string{
			"*": {
				"":  {"ast"},
				"*": append([]string(nil), verifierRequiredOutputs...),
			},
		}
	case LanguageVyper:
		if vyperCompiler.atLeast(0, 4, 0) {
			if rawPaths, exists := settings["search_paths"]; exists {
				paths, valid := rawPaths.([]any)
				if !valid || len(paths) != 1 || paths[0] != "." {
					return nil, errors.New("vyper search paths must contain only the virtual root")
				}
			}
			settings["search_paths"] = []string{"."}
		} else if _, exists := settings["search_paths"]; exists {
			return nil, errors.New("vyper search paths require compiler 0.4.0 or newer")
		}
		if err := validateVyperInlineInputs(document, sources, vyperCompiler); err != nil {
			return nil, err
		}
		selection := make(map[string]any)
		for sourceName := range sources {
			if path.Ext(sourceName) == ".vy" {
				selection[sourceName] = append([]string(nil), vyperRequiredOutputsForVersion(vyperCompiler)...)
			}
		}
		settings["outputSelection"] = selection
	default:
		return nil, errors.New("language must be solidity, yul, or vyper")
	}
	document["settings"] = settings
	prepared, err := json.Marshal(document)
	if err != nil || len(prepared) > maxInputBytes {
		return nil, errors.New("normalized standard JSON exceeds the input limit")
	}
	return prepared, nil
}

func validateCallerOutputSelection(settings map[string]any, language Language) error {
	raw, exists := settings["outputSelection"]
	if !exists {
		return nil
	}
	outer, ok := raw.(map[string]any)
	if !ok || len(outer) > maxStandardJSONSelectorEntries {
		return errors.New("standard JSON outputSelection is invalid")
	}
	total := 0
	for selector, rawValue := range outer {
		if !validStandardJSONSelector(selector, maxStandardJSONSourceNameBytes) {
			return errors.New("standard JSON outputSelection selector is invalid")
		}
		switch value := rawValue.(type) {
		case []any:
			outputs, err := standardJSONOutputNames(value)
			if err != nil {
				return errors.New("standard JSON outputSelection is invalid")
			}
			total += len(outputs)
		case map[string]any:
			if len(value) > maxStandardJSONSelectorEntries {
				return errors.New("standard JSON outputSelection is too large")
			}
			for contract, rawOutputs := range value {
				validContract := validStandardJSONContractSelector(contract)
				if language == LanguageVyper {
					validContract = validVyperContractSelector(contract)
				}
				if !validContract {
					return errors.New("standard JSON outputSelection contract selector is invalid")
				}
				outputs, err := standardJSONOutputNames(rawOutputs)
				if err != nil {
					return errors.New("standard JSON outputSelection is invalid")
				}
				total += len(outputs)
			}
		default:
			return errors.New("standard JSON outputSelection is invalid")
		}
		if total > maxStandardJSONOutputEntries {
			return errors.New("standard JSON outputSelection is too large")
		}
	}
	return nil
}

// PerturbVerifierSources creates the second compiler input. Only source
// contents change; checksums are removed because they authenticate the original
// content and would make the intentionally modified input fail before compile.
func PerturbVerifierSources(input json.RawMessage, maxInputBytes int) (json.RawMessage, error) {
	document, err := decodeStandardJSONObject(input)
	if err != nil {
		return nil, errors.New("standard JSON must be normalized before perturbation")
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok || len(sources) == 0 {
		return nil, errors.New("standard JSON sources are missing")
	}
	for name, raw := range sources {
		source, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("standard JSON source is invalid")
		}
		content, ok := source["content"].(string)
		if !ok {
			return nil, errors.New("standard JSON source content is invalid")
		}
		source["content"] = content + " "
		delete(source, "keccak256")
		sources[name] = source
	}
	modified, err := json.Marshal(document)
	if err != nil || len(modified) > maxInputBytes {
		return nil, errors.New("modified standard JSON exceeds the input limit")
	}
	return modified, nil
}

func BuildMultipartStandardJSON(request MultipartRequest, compilerVersion string, maxInputBytes int) ([]json.RawMessage, error) {
	if len(request.Sources) == 0 || len(request.Sources) > maxStandardJSONSources {
		return nil, errors.New("multipart sources are outside configured bounds")
	}
	names := make([]string, 0, len(request.Sources))
	for name := range request.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	sources := make(map[string]map[string]string, len(names))
	for _, name := range names {
		sources[name] = map[string]string{"content": request.Sources[name]}
	}
	settings := make(map[string]any)
	if request.EVMVersion != "" {
		settings["evmVersion"] = request.EVMVersion
	}
	if request.OptimizationRuns != nil {
		if *request.OptimizationRuns < 0 || *request.OptimizationRuns > 1<<31-1 {
			return nil, errors.New("optimization runs are invalid")
		}
		settings["optimizer"] = map[string]any{
			"enabled": true,
			"runs":    *request.OptimizationRuns,
		}
	}
	if len(request.Libraries) > 0 {
		libraries := make(map[string]map[string]string)
		for identifier, address := range request.Libraries {
			source, name, err := parseStandardJSONContractIdentifier(identifier, LanguageSolidity)
			if err != nil || !fixedHex(address, 20) {
				return nil, errors.New("multipart library is invalid")
			}
			if libraries[source] == nil {
				libraries[source] = make(map[string]string)
			}
			libraries[source][name] = address
		}
		settings["libraries"] = libraries
	}
	languageName, err := standardJSONLanguage(request.Language)
	if err != nil {
		return nil, err
	}
	document := map[string]any{
		"language": languageName,
		"sources":  sources,
		"settings": settings,
	}
	variants := []string{""}
	if request.Language == LanguageSolidity {
		variants = []string{"ipfs", "none", "bzzr1"}
	}
	result := make([]json.RawMessage, 0, len(variants))
	for _, metadataHash := range variants {
		copyDocument := cloneJSONMap(document)
		copySettings := copyDocument["settings"].(map[string]any)
		if metadataHash != "" {
			copySettings["metadata"] = map[string]any{
				"appendCBOR":   true,
				"bytecodeHash": metadataHash,
			}
		}
		raw, marshalErr := json.Marshal(copyDocument)
		if marshalErr != nil {
			return nil, errors.New("encode multipart compiler input")
		}
		prepared, prepareErr := PrepareVerifierStandardJSON(raw, request.Language, compilerVersion, maxInputBytes)
		if prepareErr != nil {
			return nil, prepareErr
		}
		result = append(result, prepared)
	}
	return result, nil
}

func cloneJSONMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	return clone
}
