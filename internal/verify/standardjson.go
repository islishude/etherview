package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var solidityContractNamePattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)

var solidityRequiredOutputs = []string{
	"abi",
	"metadata",
	"evm.bytecode.object",
	"evm.bytecode.linkReferences",
	"evm.deployedBytecode.object",
	"evm.deployedBytecode.linkReferences",
	"evm.deployedBytecode.immutableReferences",
}

// PrepareStandardJSON validates an inline Solidity Standard JSON input and
// replaces caller outputSelection with the exact bounded target fields.
func PrepareStandardJSON(
	input json.RawMessage,
	language Language,
	_ string,
	contractIdentifier string,
	maxInputBytes int,
) (json.RawMessage, error) {
	document, err := validateStandardJSON(
		input, language, "", contractIdentifier, maxInputBytes,
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
	_ string,
	contractIdentifier string,
	maxInputBytes int,
) (map[string]any, error) {
	if language != LanguageSolidity {
		return nil, errors.New("language must be solidity")
	}
	if maxInputBytes <= 0 {
		maxInputBytes = defaultStandardJSONBytes
	}
	if len(input) == 0 || len(input) > maxInputBytes || !jsonObject(input) {
		return nil, fmt.Errorf(
			"standard JSON must be an object of at most %d bytes",
			maxInputBytes,
		)
	}
	document, err := decodeStandardJSONObject(input)
	if err != nil {
		if errors.Is(err, errJSONDuplicateKey) {
			return nil, errors.New("standard JSON contains a duplicate object key")
		}
		return nil, errors.New("standard JSON must be one object")
	}
	if actual, ok := document["language"].(string); !ok || actual != "Solidity" {
		return nil, errors.New("standard JSON language must be Solidity")
	}
	if err := validateStandardJSONTopLevel(document, language); err != nil {
		return nil, err
	}
	targetSource, targetName, err := parseStandardJSONContractIdentifier(
		contractIdentifier, language,
	)
	if err != nil {
		return nil, err
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > maxStandardJSONSources {
		return nil, fmt.Errorf(
			"standard JSON sources must be a non-empty object with at most %d entries",
			maxStandardJSONSources,
		)
	}
	for sourceName, sourceValue := range sources {
		if !validStandardJSONSourceName(sourceName) {
			return nil, errors.New("standard JSON source name is invalid")
		}
		source, ok := sourceValue.(map[string]any)
		if !ok || len(source) > 2 {
			return nil, errors.New("standard JSON sources must be objects")
		}
		content, hasContent := source["content"]
		_, hasURLs := source["urls"]
		if _, ok := content.(string); !hasContent || !ok || hasURLs {
			return nil, errors.New(
				"every standard JSON source must contain inline content and no URLs",
			)
		}
		for key := range source {
			if key != "content" && key != "keccak256" {
				return nil, errors.New(
					"standard JSON source contains an unsupported field",
				)
			}
		}
		if checksum, exists := source["keccak256"]; exists {
			value, ok := checksum.(string)
			if !ok || !fixedHex(value, 32) {
				return nil, errors.New("standard JSON source checksum is invalid")
			}
		}
	}
	if _, ok := sources[targetSource]; !ok {
		return nil, errors.New(
			"contract identifier source is not present in standard JSON",
		)
	}
	settings := make(map[string]any)
	if rawSettings, exists := document["settings"]; exists {
		var valid bool
		settings, valid = rawSettings.(map[string]any)
		if !valid {
			return nil, errors.New("standard JSON settings must be an object")
		}
	}
	if rawMetadata, exists := settings["metadata"]; exists {
		metadata, ok := rawMetadata.(map[string]any)
		if !ok {
			return nil, errors.New("solidity metadata setting must be an object")
		}
		if rawAppendCBOR, exists := metadata["appendCBOR"]; exists {
			if _, ok := rawAppendCBOR.(bool); !ok {
				return nil, errors.New(
					"solidity metadata appendCBOR setting must be boolean",
				)
			}
		}
	}
	if err := mergeSolidityOutputSelection(settings, targetSource, targetName); err != nil {
		return nil, err
	}
	document["settings"] = settings
	return document, nil
}

func validateStandardJSONTopLevel(document map[string]any, language Language) error {
	if language != LanguageSolidity && language != LanguageYul {
		return errors.New("language must be solidity or yul")
	}
	allowed := map[string]struct{}{
		"language":       {},
		"sources":        {},
		"settings":       {},
		"auxiliaryInput": {},
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
	return nil
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
	case LanguageYul:
		return "Yul", nil
	default:
		return "", errors.New("language must be solidity or yul")
	}
}

func parseStandardJSONContractIdentifier(
	identifier string,
	language Language,
) (string, string, error) {
	if language != LanguageSolidity && language != LanguageYul {
		return "", "", errors.New("language must be solidity or yul")
	}
	separator := strings.LastIndex(identifier, ":")
	if len(identifier) > 512 || separator <= 0 || separator == len(identifier)-1 {
		return "", "", errors.New("contract identifier must be source:name")
	}
	source, name := identifier[:separator], identifier[separator+1:]
	if !validStandardJSONSourceName(source) {
		return "", "", errors.New("contract identifier source is invalid")
	}
	if !solidityContractNamePattern.MatchString(name) {
		return "", "", errors.New("contract identifier name is invalid")
	}
	return source, name, nil
}

func validStandardJSONSourceName(name string) bool {
	if len(name) == 0 || name == "*" ||
		len(name) > maxStandardJSONSourceNameBytes ||
		strings.TrimSpace(name) != name {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func mergeSolidityOutputSelection(
	settings map[string]any,
	targetSource string,
	targetName string,
) error {
	rawSelection, exists := settings["outputSelection"]
	if exists {
		outer, ok := rawSelection.(map[string]any)
		if !ok || len(outer) > maxStandardJSONSelectorEntries {
			return errors.New("solidity outputSelection is invalid")
		}
		totalOutputs := 0
		for sourceSelector, rawContracts := range outer {
			if !validStandardJSONSelector(
				sourceSelector, maxStandardJSONSourceNameBytes,
			) {
				return errors.New(
					"solidity outputSelection source selector is invalid",
				)
			}
			contracts, ok := rawContracts.(map[string]any)
			if !ok || len(contracts) > maxStandardJSONSelectorEntries {
				return errors.New("solidity outputSelection is invalid")
			}
			for contractSelector, rawOutputs := range contracts {
				if !validStandardJSONContractSelector(contractSelector) {
					return errors.New(
						"solidity outputSelection contract selector is invalid",
					)
				}
				outputs, err := standardJSONOutputNames(rawOutputs)
				if err != nil {
					return errors.New("solidity outputSelection is invalid")
				}
				totalOutputs += len(outputs)
				if totalOutputs > maxStandardJSONOutputEntries {
					return errors.New(
						"solidity outputSelection has too many entries",
					)
				}
			}
		}
	}
	settings["outputSelection"] = map[string]map[string][]string{
		targetSource: {
			targetName: append([]string(nil), solidityRequiredOutputs...),
		},
	}
	return nil
}

func standardJSONOutputNames(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok || len(values) > maxStandardJSONOutputEntries {
		return nil, errors.New("outputs must be an array")
	}
	outputs := make([]string, 0, len(values))
	for _, value := range values {
		output, ok := value.(string)
		if !ok || len(output) == 0 ||
			len(output) > maxStandardJSONOutputNameBytes ||
			strings.TrimSpace(output) != output {
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
	if len(selector) == 0 || len(selector) > maximum ||
		strings.TrimSpace(selector) != selector {
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
	return selector == "" || selector == "*" ||
		solidityContractNamePattern.MatchString(selector)
}
