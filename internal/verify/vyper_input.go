package verify

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

var vyperOutputs = []string{"abi", "metadata", "layout", "evm.bytecode.object", "evm.deployedBytecode.object", "evm.methodIdentifiers", "userdoc", "devdoc"}

type VyperMultipartRequest struct {
	Sources          map[string]string
	Interfaces       map[string]json.RawMessage
	EVMVersion       string
	OptimizationMode string
}

func PrepareVyperStandardJSON(input json.RawMessage, target string, maxBytes int) (json.RawMessage, error) {
	invalid := errors.New("invalid Vyper Standard JSON")
	if maxBytes <= 0 {
		maxBytes = defaultCompilerInputBytes
	}
	if len(input) == 0 || len(input) > maxBytes || !validGeasSourcePath(target) || !strings.HasSuffix(target, ".vy") {
		return nil, invalid
	}
	stem := strings.TrimSuffix(path.Base(target), ".vy")
	if stem == "" || len(stem) > 256 {
		return nil, invalid
	}
	doc, err := decodeStandardJSONObject(input)
	if err != nil || doc["language"] != "Vyper" {
		return nil, invalid
	}
	for key := range doc {
		if key != "language" && key != "sources" && key != "interfaces" && key != "settings" {
			return nil, invalid
		}
	}
	sources, ok := doc["sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > maxStandardJSONSources {
		return nil, invalid
	}
	if _, ok = sources[target]; !ok {
		return nil, invalid
	}
	seen := map[string]bool{}
	for _, section := range []string{"sources", "interfaces"} {
		values, exists := doc[section]
		if !exists {
			continue
		}
		entries, valid := values.(map[string]any)
		if !valid || len(entries)+len(seen) > maxStandardJSONSources {
			return nil, invalid
		}
		for name, raw := range entries {
			if !validGeasSourcePath(name) || seen[name] {
				return nil, invalid
			}
			seen[name] = true
			entry, valid := raw.(map[string]any)
			if !valid || len(entry) != 1 {
				return nil, invalid
			}
			if _, valid = entry["content"].(string); valid {
				continue
			}
			if _, valid = entry["abi"].([]any); !valid || section != "interfaces" {
				return nil, invalid
			}
		}
	}
	settings := map[string]any{}
	if raw, exists := doc["settings"]; exists {
		var ok bool
		settings, ok = raw.(map[string]any)
		if !ok {
			return nil, invalid
		}
	}
	for key, value := range settings {
		switch key {
		case "evmVersion":
			if name, ok := value.(string); !ok || len(name) > 64 || name == "" {
				return nil, invalid
			}
		case "optimize":
			if value != "none" && value != "gas" && value != "codesize" {
				return nil, invalid
			}
		case "bytecodeMetadata", "enable_decimals":
			if _, ok := value.(bool); !ok {
				return nil, invalid
			}
		case "outputSelection":
			if _, ok := value.(map[string]any); !ok {
				return nil, invalid
			}
		case "search_paths":
			entries, ok := value.([]any)
			if !ok || len(entries) != 1 || entries[0] != "." {
				return nil, invalid
			}
		default:
			return nil, invalid
		}
	}
	if _, exists := settings["optimize"]; !exists {
		settings["optimize"] = "gas"
	}
	settings["search_paths"] = []string{"."}
	settings["outputSelection"] = map[string]any{target: vyperOutputs}
	doc["settings"] = settings
	encoded, err := json.Marshal(doc)
	if err != nil || len(encoded) > maxBytes {
		return nil, invalid
	}
	return encoded, nil
}

func BuildVyperMultipart(request VyperMultipartRequest, target string, maxBytes int) (json.RawMessage, error) {
	sources := map[string]any{}
	for name, content := range request.Sources {
		sources[name] = map[string]string{"content": content}
	}
	settings := map[string]any{}
	if request.EVMVersion != "" {
		settings["evmVersion"] = request.EVMVersion
	}
	if request.OptimizationMode != "" {
		settings["optimize"] = request.OptimizationMode
	}
	doc := map[string]any{"language": "Vyper", "sources": sources, "settings": settings}
	if len(request.Interfaces) > 0 {
		doc["interfaces"] = request.Interfaces
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return PrepareVyperStandardJSON(encoded, target, maxBytes)
}

func prepareVyperSubmission(request *SubmissionV2, maxBytes int) error {
	if request.CompilerVersion != VyperCompilerVersion || request.Geas != nil || request.Multipart != nil || len(request.StandardJSONVariants) > 0 || (request.Kind != JobAddress && request.Kind != JobVyperStandardJSON && request.Kind != JobVyperMultipart) {
		return errors.New("invalid Vyper verification request")
	}
	var input json.RawMessage
	var err error
	if request.VyperMultipart != nil {
		if len(request.StandardJSON) != 0 {
			return errors.New("conflicting Vyper inputs")
		}
		input, err = BuildVyperMultipart(*request.VyperMultipart, request.TargetFile, maxBytes)
	} else {
		input, err = PrepareVyperStandardJSON(request.StandardJSON, request.TargetFile, maxBytes)
	}
	if err != nil {
		return err
	}
	request.ContractNameHint = request.TargetFile + ":" + strings.TrimSuffix(path.Base(request.TargetFile), ".vy")
	request.StandardJSON = input
	request.StandardJSONVariants = []json.RawMessage{input}
	return nil
}
