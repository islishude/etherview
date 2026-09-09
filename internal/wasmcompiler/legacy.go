package wasmcompiler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Legacy translation preserves solc@0.8.36 wrapper behavior for compilers that
// predate Standard JSON. Upstream solc-js is MIT licensed (see runtime notices).
func (s *solcState) legacyCompile(ctx context.Context, input []byte) ([]byte, error) {
	var req struct {
		Language string
		Sources  map[string]struct{ Content *string }
		Settings struct {
			Optimizer struct{ Enabled bool }
			Libraries map[string]json.RawMessage
		}
	}
	if json.Unmarshal(input, &req) != nil {
		return nil, ErrExecution
	}
	if req.Language != "Solidity" {
		return fatalJSON(`Only "Solidity" is supported as a language.`)
	}
	if req.Sources == nil {
		return fatalJSON("No input sources specified.")
	}
	sources := map[string]string{}
	for key, source := range req.Sources {
		if source.Content == nil {
			return fatalJSON("Failed to process sources.")
		}
		sources[key] = *source.Content
	}
	if len(sources) == 0 {
		return fatalJSON("Failed to process sources.")
	}
	raw, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		return nil, ErrExecution
	}
	name := "_compileJSONCallback"
	if !s.optional(name) {
		name = "_compileJSONMulti"
	}
	if !s.optional(name) {
		return nil, ErrUnsupported
	}
	saved := s.call(ctx, "stackSave")[0]
	defer s.call(ctx, "stackRestore", saved)
	p := s.stackString(ctx, string(raw))
	optimize := uint64(0)
	if req.Settings.Optimizer.Enabled {
		optimize = 1
	}
	args := []uint64{uint64(p), optimize}
	if name == "_compileJSONCallback" {
		args = append(args, uint64(s.callback))
	}
	result, err := s.outputBytes(uint32(s.call(ctx, name, args...)[0]))
	if err != nil {
		return nil, err
	}
	var original map[string]any
	if json.Unmarshal(result, &original) != nil {
		return nil, ErrExecution
	}
	translated, err := translateLegacy(original, req.Settings.Libraries)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(translated)
	if uint32(len(out)) > s.maxOutput {
		return nil, ErrOutputLimit
	}
	return out, err
}
func fatalJSON(message string) ([]byte, error) {
	return json.Marshal(map[string]any{"errors": []any{map[string]string{"type": "JSONError", "component": "solcjs", "severity": "error", "message": message, "formattedMessage": "Error: " + message}}})
}

var legacyErrorType = regexp.MustCompile(`^(.*):([0-9]+):([0-9]+):(.*):`)

func translateLegacy(input map[string]any, libraries map[string]json.RawMessage) (map[string]any, error) {
	result := map[string]any{}
	diagnostics := []any{}
	errs, _ := input["errors"].([]any)
	if e, ok := input["error"].(string); ok && e != "" {
		errs = []any{e}
	}
	for _, raw := range errs {
		message, ok := raw.(string)
		if !ok {
			return nil, ErrExecution
		}
		kind := "error"
		if m := legacyErrorType.FindStringSubmatch(message); m != nil {
			kind = strings.TrimSpace(m[4])
		} else if strings.Index(message, ": Warning:") != 0 {
			kind = "Warning"
		} else if strings.Index(message, ": Error:") != 0 {
			kind = "Error"
		}
		severity := "error"
		if kind == "Warning" {
			severity = "warning"
		}
		diagnostics = append(diagnostics, map[string]string{"type": kind, "severity": severity, "component": "general", "message": message, "formattedMessage": message})
	}
	result["errors"] = diagnostics
	contracts := map[string]any{}
	originals, _ := input["contracts"].(map[string]any)
	for name, raw := range originals {
		contract, ok := raw.(map[string]any)
		if !ok {
			return nil, ErrExecution
		}
		file, contractName := "", name
		if index := strings.LastIndex(name, ":"); index >= 0 {
			file, contractName = name[:index], name[index+1:]
		}
		if contractName == "" {
			return nil, ErrExecution
		}
		item, err := translateLegacyContract(contract, libraries)
		if err != nil {
			return nil, err
		}
		if contracts[file] == nil {
			contracts[file] = map[string]any{}
		}
		contracts[file].(map[string]any)[contractName] = item
	}
	result["contracts"] = contracts
	ids := map[string]string{}
	sourceList, _ := input["sourceList"].([]any)
	for i, name := range sourceList {
		if name, ok := name.(string); ok {
			ids[name] = strconv.Itoa(i)
		}
	}
	sources := map[string]any{}
	originalSources, _ := input["sources"].(map[string]any)
	for name, raw := range originalSources {
		src, ok := raw.(map[string]any)
		if !ok {
			return nil, ErrExecution
		}
		item := map[string]any{}
		if id, ok := ids[name]; ok {
			item["id"] = id
		}
		if ast, ok := src["AST"]; ok {
			item["legacyAST"] = ast
		}
		sources[name] = item
	}
	result["sources"] = sources
	return result, nil
}
func translateLegacyContract(in map[string]any, libraries map[string]json.RawMessage) (map[string]any, error) {
	iface, ok := in["interface"].(string)
	if !ok {
		return nil, ErrExecution
	}
	var abi any
	if json.Unmarshal([]byte(iface), &abi) != nil {
		return nil, ErrExecution
	}
	result := map[string]any{"abi": abi}
	copyField(result, "metadata", in, "metadata")
	evm := map[string]any{}
	copyField(evm, "legacyAssembly", in, "assembly")
	copyField(evm, "methodIdentifiers", in, "functionHashes")
	for _, pair := range [][3]string{{"bytecode", "bytecode", "srcmap"}, {"deployedBytecode", "runtimeBytecode", "srcmapRuntime"}} {
		value := map[string]any{}
		copyField(value, "sourceMap", in, pair[2])
		if pair[0] == "bytecode" {
			copyField(value, "opcodes", in, "opcodes")
		}
		if code, ok := in[pair[1]].(string); ok {
			if code == "" {
				value["object"] = ""
				value["linkReferences"] = ""
			} else {
				linked, err := legacyLink(code, libraries)
				if err != nil {
					return nil, err
				}
				value["object"] = linked
				value["linkReferences"] = legacyReferences(code)
			}
		}
		evm[pair[0]] = value
	}
	gas := map[string]any{}
	original, _ := in["gasEstimates"].(map[string]any)
	if creation, ok := original["creation"].([]any); ok && len(creation) == 2 {
		gas["creation"] = map[string]any{"codeDepositCost": translateGas(creation[1]), "executionCost": translateGas(creation[0])}
	}
	for _, key := range []string{"internal", "external"} {
		if value, ok := original[key]; ok {
			gas[key] = translateGas(value)
		}
	}
	evm["gasEstimates"] = gas
	result["evm"] = evm
	return result, nil
}
func copyField(dst map[string]any, key string, src map[string]any, original string) {
	if value, ok := src[original]; ok {
		dst[key] = value
	}
}
func translateGas(value any) any {
	switch v := value.(type) {
	case nil:
		return "infinite"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case map[string]any:
		out := map[string]any{}
		for k, v := range v {
			out[k] = translateGas(v)
		}
		return out
	default:
		return map[string]any{}
	}
}

var legacyPlaceholder = regexp.MustCompile(`__(.{36})__`)

func legacyReferences(code string) map[string]any {
	refs := map[string]any{}
	offset := 0
	for {
		match := legacyPlaceholder.FindStringSubmatchIndex(code)
		if match == nil {
			break
		}
		name := strings.TrimRight(code[match[2]:match[3]], "_")
		old, _ := refs[name].([]any)
		refs[name] = append(old, map[string]int{"start": (offset + match[0]) / 2, "length": 20})
		offset += match[0] + 20
		code = code[match[0]+20:]
	}
	return refs
}
func legacyLink(code string, libraries map[string]json.RawMessage) (string, error) {
	complete := map[string]string{}
	for file, raw := range libraries {
		var address string
		if json.Unmarshal(raw, &address) == nil {
			complete[file] = address
			if _, short, ok := strings.Cut(file, ":"); ok {
				complete[short] = address
			}
			continue
		}
		var entries map[string]string
		if json.Unmarshal(raw, &entries) != nil {
			return "", ErrExecution
		}
		for name, address := range entries {
			complete[name] = address
			complete[file+":"+name] = address
		}
	}
	for name, address := range complete {
		if !strings.HasPrefix(address, "0x") || len(address) > 42 {
			return "", ErrExecution
		}
		address = strings.Repeat("0", 42-len(address)) + address[2:]
		h := sha3.NewLegacyKeccak256()
		_, _ = h.Write([]byte(name))
		hashed := "$" + hex.EncodeToString(h.Sum(nil))[:34] + "$"
		for _, label := range []string{name, hashed} {
			if len(label) > 36 {
				label = label[:36]
			}
			label = "__" + label + strings.Repeat("_", 36-len(label)) + "__"
			code = strings.ReplaceAll(code, label, address)
		}
	}
	return code, nil
}
