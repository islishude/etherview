package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
)

type CandidateArtifact struct {
	FileName              string          `json:"fileName"`
	ContractName          string          `json:"contractName"`
	Language              Language        `json:"language"`
	CompilerVersion       string          `json:"compilerVersion"`
	CreationBytecode      string          `json:"creationBytecode"`
	RuntimeBytecode       string          `json:"runtimeBytecode"`
	ABI                   json.RawMessage `json:"abi"`
	CompilationArtifacts  json.RawMessage `json:"compilationArtifacts"`
	CreationCodeArtifacts json.RawMessage `json:"creationCodeArtifacts"`
	RuntimeCodeArtifacts  json.RawMessage `json:"runtimeCodeArtifacts"`

	creationBytes     []byte
	runtimeBytes      []byte
	creationAuxdata   map[string]AuxdataValue
	runtimeAuxdata    map[string]AuxdataValue
	creationLinks     map[string]map[string][]bytecodeRange
	runtimeLinks      map[string]map[string][]bytecodeRange
	runtimeImmutables map[string][]bytecodeRange
}

func (candidate CandidateArtifact) FullyQualifiedName() string {
	return candidate.FileName + ":" + candidate.ContractName
}

type BytecodePair struct {
	Creation string `json:"creation_bytecode,omitempty"`
	Runtime  string `json:"runtime_bytecode,omitempty"`
}

type CandidateVerification struct {
	Candidate CandidateArtifact         `json:"candidate"`
	Creation  *VerificationMatchDetails `json:"creationMatch,omitempty"`
	Runtime   *VerificationMatchDetails `json:"runtimeMatch,omitempty"`
	Blueprint bool                      `json:"isBlueprint"`
}

type CompilationFailure struct {
	Message string
}

func (failure CompilationFailure) Error() string { return failure.Message }

// CompileAndVerify performs the source-whitespace dual compilation and
// verifies every bounded compiler candidate.
func CompileAndVerify(
	ctx context.Context,
	compiler Compiler,
	language Language,
	version string,
	input json.RawMessage,
	bytecodes BytecodePair,
	hint string,
	requireRuntime bool,
	maxInputBytes int,
) ([]CandidateVerification, error) {
	prepared, err := PrepareVerifierStandardJSON(input, language, version, maxInputBytes)
	if err != nil {
		return nil, err
	}
	modified, err := PerturbVerifierSources(prepared, maxInputBytes)
	if err != nil {
		return nil, err
	}
	originalOutput, err := compiler.Compile(ctx, language, version, prepared)
	if err != nil {
		return nil, CompilationFailure{Message: "compiler execution failed"}
	}
	modifiedOutput, err := compiler.Compile(ctx, language, version, modified)
	if err != nil {
		return nil, CompilationFailure{Message: "modified compiler execution failed"}
	}
	candidates, err := ExtractCandidatesV2(originalOutput, modifiedOutput, language, version)
	if err != nil {
		return nil, err
	}
	return VerifyCandidateArtifacts(candidates, bytecodes, hint, requireRuntime)
}

func ExtractCandidatesV2(
	originalOutput json.RawMessage,
	modifiedOutput json.RawMessage,
	language Language,
	version string,
) ([]CandidateArtifact, error) {
	original, err := compilerContractDocuments(originalOutput)
	if err != nil {
		return nil, err
	}
	modified, err := compilerContractDocuments(modifiedOutput)
	if err != nil {
		return nil, err
	}
	originalNames := sortedKeys(original)
	modifiedNames := sortedKeys(modified)
	if !equalStrings(originalNames, modifiedNames) {
		return nil, errors.New("dual compilation changed the candidate set")
	}
	originalBases, err := compilerLinearizedBaseContracts(originalOutput)
	if err != nil {
		return nil, err
	}
	modifiedBases, err := compilerLinearizedBaseContracts(modifiedOutput)
	if err != nil {
		return nil, err
	}
	if !equalStringSlicesByKey(originalBases, modifiedBases) {
		return nil, errors.New("dual compilation changed contract inheritance")
	}
	originalImmutables, err := compilerImmutableVariables(originalOutput)
	if err != nil {
		return nil, err
	}
	modifiedImmutables, err := compilerImmutableVariables(modifiedOutput)
	if err != nil {
		return nil, err
	}
	if !equalStringMapsByKey(originalImmutables, modifiedImmutables) {
		return nil, errors.New("dual compilation changed immutable declarations")
	}
	candidates := make([]CandidateArtifact, 0, len(originalNames))
	for _, name := range originalNames {
		first, firstEmpty, err := parseCandidateContract(original[name], language)
		if err != nil {
			return nil, err
		}
		second, secondEmpty, err := parseCandidateContract(modified[name], language)
		if err != nil {
			return nil, err
		}
		if firstEmpty != secondEmpty {
			return nil, errors.New("dual compilation changed an abstract candidate")
		}
		if firstEmpty {
			continue
		}
		if len(first.creationBytes) != len(second.creationBytes) ||
			len(first.runtimeBytes) != len(second.runtimeBytes) {
			return nil, errors.New("dual compilation changed candidate bytecode length")
		}
		first.creationAuxdata, err = LocateAuxdata(first.creationBytes, second.creationBytes, language)
		if err != nil {
			return nil, fmt.Errorf("locate creation auxdata: %w", err)
		}
		first.runtimeAuxdata, err = LocateAuxdata(first.runtimeBytes, second.runtimeBytes, language)
		if err != nil {
			return nil, fmt.Errorf("locate runtime auxdata: %w", err)
		}
		parts := strings.SplitN(name, "\x00", 2)
		first.FileName, first.ContractName = parts[0], parts[1]
		first.Language = language
		first.CompilerVersion = normalizeCompilerVersion(version)
		first.CreationCodeArtifacts = mergeAuxdataArtifact(first.CreationCodeArtifacts, first.creationAuxdata)
		first.RuntimeCodeArtifacts = mergeAuxdataArtifact(first.RuntimeCodeArtifacts, first.runtimeAuxdata)
		if bases, exists := originalBases[first.FullyQualifiedName()]; exists {
			first.CompilationArtifacts = mergeArtifactField(
				first.CompilationArtifacts, "linearizedBaseContracts", bases,
			)
		}
		if immutables, exists := originalImmutables[first.FullyQualifiedName()]; exists && len(immutables) != 0 {
			first.CompilationArtifacts = mergeArtifactField(
				first.CompilationArtifacts, "immutableVariables", immutables,
			)
		}
		candidates = append(candidates, first)
	}
	return candidates, nil
}

type compilerSourceDocument struct {
	AST struct {
		Nodes []compilerContractDefinition `json:"nodes"`
	} `json:"ast"`
}

type compilerContractDefinition struct {
	ID                      int64   `json:"id"`
	NodeType                string  `json:"nodeType"`
	Name                    string  `json:"name"`
	LinearizedBaseContracts []int64 `json:"linearizedBaseContracts"`
	Nodes                   []struct {
		ID            int64  `json:"id"`
		NodeType      string `json:"nodeType"`
		Name          string `json:"name"`
		StateVariable bool   `json:"stateVariable"`
		Mutability    string `json:"mutability"`
	} `json:"nodes"`
}

func compilerLinearizedBaseContracts(output json.RawMessage) (map[string][]string, error) {
	document, err := decodeRawJSONObject(output)
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	if len(document["sources"]) == 0 {
		return map[string][]string{}, nil
	}
	sources, err := decodeRawJSONObject(document["sources"])
	if err != nil || len(sources) > maxStandardJSONSources {
		return nil, errCompilerOutputMalformed
	}
	type located struct {
		file string
		node compilerContractDefinition
	}
	byID := make(map[int64]string)
	definitions := make([]located, 0)
	for fileName, raw := range sources {
		if !validStandardJSONSourceName(fileName) {
			return nil, errCompilerOutputMalformed
		}
		var source compilerSourceDocument
		if json.Unmarshal(raw, &source) != nil {
			return nil, errCompilerOutputMalformed
		}
		for _, node := range source.AST.Nodes {
			if node.NodeType != "ContractDefinition" {
				continue
			}
			if node.ID < 0 || !solidityContractNamePattern.MatchString(node.Name) ||
				len(node.LinearizedBaseContracts) == 0 ||
				node.LinearizedBaseContracts[0] != node.ID {
				return nil, errCompilerOutputMalformed
			}
			name := fileName + ":" + node.Name
			if _, exists := byID[node.ID]; exists {
				return nil, errCompilerOutputMalformed
			}
			byID[node.ID] = name
			definitions = append(definitions, located{file: fileName, node: node})
			if len(definitions) > maxStandardJSONSelectorEntries {
				return nil, errCompilerOutputMalformed
			}
		}
	}
	result := make(map[string][]string, len(definitions))
	for _, definition := range definitions {
		bases := make([]string, 0, len(definition.node.LinearizedBaseContracts)-1)
		for _, id := range definition.node.LinearizedBaseContracts[1:] {
			name, exists := byID[id]
			if !exists {
				return nil, errCompilerOutputMalformed
			}
			bases = append(bases, name)
		}
		result[definition.file+":"+definition.node.Name] = bases
	}
	return result, nil
}

func equalStringSlicesByKey(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, values := range left {
		other, exists := right[key]
		if !exists || !equalStrings(values, other) {
			return false
		}
	}
	return true
}

func compilerImmutableVariables(output json.RawMessage) (map[string]map[string]string, error) {
	document, err := decodeRawJSONObject(output)
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	if len(document["sources"]) == 0 {
		return map[string]map[string]string{}, nil
	}
	sources, err := decodeRawJSONObject(document["sources"])
	if err != nil || len(sources) > maxStandardJSONSources {
		return nil, errCompilerOutputMalformed
	}
	type contractInfo struct {
		name       string
		linearized []int64
		variables  map[string]string
	}
	contracts := make(map[int64]contractInfo)
	variableIDs := make(map[int64]struct{})
	for fileName, raw := range sources {
		if !validStandardJSONSourceName(fileName) {
			return nil, errCompilerOutputMalformed
		}
		var source compilerSourceDocument
		if json.Unmarshal(raw, &source) != nil {
			return nil, errCompilerOutputMalformed
		}
		for _, contract := range source.AST.Nodes {
			if contract.NodeType != "ContractDefinition" {
				continue
			}
			variables := make(map[string]string)
			for _, node := range contract.Nodes {
				if node.NodeType != "VariableDeclaration" || !node.StateVariable || node.Mutability != "immutable" {
					continue
				}
				if node.ID < 0 || node.Name == "" || len(node.Name) > 256 {
					return nil, errCompilerOutputMalformed
				}
				if _, duplicate := variableIDs[node.ID]; duplicate {
					return nil, errCompilerOutputMalformed
				}
				variableIDs[node.ID] = struct{}{}
				variables[strconv.FormatInt(node.ID, 10)] = fileName + ":" + contract.Name + ":" + node.Name
			}
			contracts[contract.ID] = contractInfo{
				name:       fileName + ":" + contract.Name,
				linearized: append([]int64(nil), contract.LinearizedBaseContracts...),
				variables:  variables,
			}
		}
	}
	result := make(map[string]map[string]string, len(contracts))
	for _, contract := range contracts {
		variables := make(map[string]string)
		for _, baseID := range contract.linearized {
			base, exists := contracts[baseID]
			if !exists {
				return nil, errCompilerOutputMalformed
			}
			maps.Copy(variables, base.variables)
		}
		result[contract.name] = variables
	}
	return result, nil
}

func equalStringMapsByKey(left, right map[string]map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, values := range left {
		other, exists := right[key]
		if !exists || len(values) != len(other) {
			return false
		}
		for id, name := range values {
			if other[id] != name {
				return false
			}
		}
	}
	return true
}

func compilerContractDocuments(output json.RawMessage) (map[string]json.RawMessage, error) {
	document, err := decodeRawJSONObject(output)
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	if err := validateCompilerDiagnostics(document["errors"]); err != nil {
		if errors.Is(err, errCompilerOutputDiagnostic) {
			return nil, CompilationFailure{Message: "compiler reported an error"}
		}
		return nil, err
	}
	contracts, err := decodeRawJSONObject(document["contracts"])
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	flattened := make(map[string]json.RawMessage)
	if len(contracts) > maxStandardJSONSources {
		return nil, errCompilerOutputMalformed
	}
	for fileName, rawContracts := range contracts {
		if !validStandardJSONSourceName(fileName) {
			return nil, errCompilerOutputMalformed
		}
		byName, err := decodeRawJSONObject(rawContracts)
		if err != nil || len(byName) > maxStandardJSONSelectorEntries {
			return nil, errCompilerOutputMalformed
		}
		for contractName, rawContract := range byName {
			if !solidityContractNamePattern.MatchString(contractName) {
				return nil, errCompilerOutputMalformed
			}
			flattened[fileName+"\x00"+contractName] = rawContract
		}
	}
	if len(flattened) > maxStandardJSONSelectorEntries {
		return nil, errCompilerOutputMalformed
	}
	return flattened, nil
}

func parseCandidateContract(
	raw json.RawMessage,
	language Language,
) (CandidateArtifact, bool, error) {
	contract, err := decodeRawJSONObject(raw)
	if err != nil {
		return CandidateArtifact{}, false, errCompilerOutputMalformed
	}
	abiValue := contract["abi"]
	if len(abiValue) == 0 && language == LanguageYul {
		abiValue = json.RawMessage(`[]`)
	}
	if !jsonArray(abiValue) {
		return CandidateArtifact{}, false, errCompilerOutputMalformed
	}
	evm, err := decodeRawJSONObject(contract["evm"])
	if err != nil {
		return CandidateArtifact{}, false, errCompilerOutputMalformed
	}
	creation, creationLinks, _, creationEmpty, err := parseCandidateBytecode(evm["bytecode"], false)
	if err != nil {
		return CandidateArtifact{}, false, err
	}
	runtime, runtimeLinks, immutables, runtimeEmpty, err := parseCandidateBytecode(evm["deployedBytecode"], true)
	if err != nil {
		return CandidateArtifact{}, false, err
	}
	if creationEmpty && runtimeEmpty {
		return CandidateArtifact{}, true, nil
	}
	if creationEmpty || runtimeEmpty {
		return CandidateArtifact{}, false, errCompilerOutputMalformed
	}
	compilationArtifacts := marshalArtifactObject(map[string]json.RawMessage{
		"abi":           abiValue,
		"devdoc":        objectOrEmpty(contract["devdoc"]),
		"userdoc":       objectOrEmpty(contract["userdoc"]),
		"storageLayout": objectOrEmpty(contract["storageLayout"]),
	})
	creationFields, _ := decodeRawJSONObject(evm["bytecode"])
	runtimeFields, _ := decodeRawJSONObject(evm["deployedBytecode"])
	creationArtifacts := marshalArtifactObject(map[string]json.RawMessage{
		"sourceMap":      stringOrEmpty(creationFields["sourceMap"]),
		"linkReferences": objectOrEmpty(creationFields["linkReferences"]),
	})
	runtimeArtifacts := marshalArtifactObject(map[string]json.RawMessage{
		"sourceMap":           stringOrEmpty(runtimeFields["sourceMap"]),
		"linkReferences":      objectOrEmpty(runtimeFields["linkReferences"]),
		"immutableReferences": objectOrEmpty(runtimeFields["immutableReferences"]),
	})
	return CandidateArtifact{
		CreationBytecode:      "0x" + hex.EncodeToString(creation),
		RuntimeBytecode:       "0x" + hex.EncodeToString(runtime),
		ABI:                   append(json.RawMessage(nil), abiValue...),
		CompilationArtifacts:  compilationArtifacts,
		CreationCodeArtifacts: creationArtifacts,
		RuntimeCodeArtifacts:  runtimeArtifacts,
		creationBytes:         creation,
		runtimeBytes:          runtime,
		creationLinks:         creationLinks,
		runtimeLinks:          runtimeLinks,
		runtimeImmutables:     immutables,
	}, false, nil
}

func parseCandidateBytecode(
	raw json.RawMessage,
	withImmutables bool,
) ([]byte, map[string]map[string][]bytecodeRange, map[string][]bytecodeRange, bool, error) {
	fields, err := decodeRawJSONObject(raw)
	if err != nil {
		return nil, nil, nil, false, errCompilerOutputMalformed
	}
	var object string
	if err := json.Unmarshal(fields["object"], &object); err != nil {
		return nil, nil, nil, false, errCompiledCodeMalformed
	}
	if strings.TrimPrefix(object, "0x") == "" {
		return nil, nil, nil, true, nil
	}
	byteLength, err := compilerBytecodeLength(object)
	if err != nil || byteLength > maxMatcherBytecodeBytes {
		return nil, nil, nil, false, errCompiledCodeMalformed
	}
	links := make(map[string]map[string][]bytecodeRange)
	if rawLinks, exists := fields["linkReferences"]; exists {
		if !jsonObject(rawLinks) || decodeStrictJSON(rawLinks, &links) != nil {
			return nil, nil, nil, false, errCompilerOutputMalformed
		}
	}
	if err := validateLinkReferences(links, byteLength); err != nil {
		return nil, nil, nil, false, err
	}
	immutables := make(map[string][]bytecodeRange)
	if rawImmutables, exists := fields["immutableReferences"]; exists {
		if !jsonObject(rawImmutables) || decodeStrictJSON(rawImmutables, &immutables) != nil {
			return nil, nil, nil, false, errCompilerOutputMalformed
		}
	}
	if !withImmutables && len(immutables) != 0 {
		return nil, nil, nil, false, errCompilerOutputMalformed
	}
	decoded, err := decodeLinkedCompilerBytecode(object, links)
	if err != nil || len(decoded) != byteLength {
		return nil, nil, nil, false, errCompiledCodeMalformed
	}
	if err := validateImmutableReferences(immutables, decoded); err != nil {
		return nil, nil, nil, false, err
	}
	return decoded, links, immutables, false, nil
}

func decodeLinkedCompilerBytecode(
	object string,
	links map[string]map[string][]bytecodeRange,
) ([]byte, error) {
	object = strings.TrimPrefix(strings.TrimSpace(object), "0x")
	normalized := []byte(object)
	for _, libraries := range links {
		for _, ranges := range libraries {
			for _, span := range ranges {
				start, end := int(span.Start*2), int((span.Start+span.Length)*2)
				if start < 0 || end < start || end > len(normalized) {
					return nil, errCompiledCodeMalformed
				}
				for index := start; index < end; index++ {
					normalized[index] = '0'
				}
			}
		}
	}
	decoded, err := hex.DecodeString(string(normalized))
	if err != nil {
		return nil, errCompiledCodeMalformed
	}
	return decoded, nil
}

func VerifyCandidateArtifacts(
	candidates []CandidateArtifact,
	bytecodes BytecodePair,
	hint string,
	requireRuntime bool,
) ([]CandidateVerification, error) {
	creation, err := optionalBytecode(bytecodes.Creation)
	if err != nil {
		return nil, errors.New("creation bytecode is invalid")
	}
	runtime, err := optionalBytecode(bytecodes.Runtime)
	if err != nil {
		return nil, errors.New("runtime bytecode is invalid")
	}
	if len(creation) == 0 && len(runtime) == 0 {
		return nil, errors.New("at least one bytecode is required")
	}
	creationBlueprint, creationIsBlueprint, err := ParseBlueprint(creation)
	if err != nil {
		return nil, err
	}
	runtimeBlueprint, runtimeIsBlueprint, err := ParseBlueprint(runtime)
	if err != nil {
		return nil, err
	}
	if len(creation) > 0 && len(runtime) > 0 && (creationIsBlueprint || runtimeIsBlueprint) {
		if !creationIsBlueprint || !runtimeIsBlueprint ||
			!bytes.Equal(creationBlueprint.Initcode, runtimeBlueprint.Initcode) {
			return nil, errors.New("creation and runtime blueprints must contain the same initcode")
		}
	}
	var ranked []RankedCandidate
	byName := make(map[string]CandidateVerification)
	for _, candidate := range candidates {
		result := CandidateVerification{Candidate: candidate}
		if len(creation) > 0 {
			deployed := creation
			allowConstructor := true
			if creationIsBlueprint {
				deployed = creationBlueprint.Initcode
				allowConstructor = false
				result.Blueprint = true
			}
			result.Creation, err = matchTransformedCode(deployed, candidate.creationBytes, codeTransformationInput{
				Auxdata: candidate.creationAuxdata, LinkReferences: candidate.creationLinks,
				ABI: candidate.ABI, Creation: allowConstructor,
			})
			if err != nil {
				return nil, err
			}
		}
		if len(runtime) > 0 {
			deployed := runtime
			if runtimeIsBlueprint {
				deployed = runtimeBlueprint.Initcode
				result.Blueprint = true
				result.Runtime, err = matchTransformedCode(deployed, candidate.creationBytes, codeTransformationInput{
					Auxdata: candidate.creationAuxdata, LinkReferences: candidate.creationLinks,
				})
			} else {
				result.Runtime, err = matchTransformedCode(deployed, candidate.runtimeBytes, codeTransformationInput{
					Auxdata: candidate.runtimeAuxdata, LinkReferences: candidate.runtimeLinks,
					ImmutableReferences: candidate.runtimeImmutables,
				})
			}
			if err != nil {
				return nil, err
			}
		}
		name := candidate.FullyQualifiedName()
		ranked = append(ranked, RankedCandidate{
			FullyQualifiedName: name,
			Creation:           result.Creation,
			Runtime:            result.Runtime,
			Hint:               hint != "" && (hint == candidate.ContractName || hint == name),
		})
		byName[name] = result
	}
	ranked = SortCandidates(ranked, requireRuntime)
	results := make([]CandidateVerification, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, byName[item.FullyQualifiedName])
	}
	return results, nil
}

func optionalBytecode(value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return decodeBytecode(value)
}

func mergeAuxdataArtifact(raw json.RawMessage, auxdata map[string]AuxdataValue) json.RawMessage {
	var object map[string]json.RawMessage
	if decodeStrictJSON(raw, &object) != nil {
		return raw
	}
	encoded, _ := json.Marshal(auxdata)
	object["cborAuxdata"] = encoded
	result, _ := json.Marshal(object)
	return result
}

func mergeArtifactField(raw json.RawMessage, name string, value any) json.RawMessage {
	var object map[string]json.RawMessage
	if name == "" || decodeStrictJSON(raw, &object) != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	object[name] = encoded
	result, err := json.Marshal(object)
	if err != nil {
		return raw
	}
	return result
}

func marshalArtifactObject(values map[string]json.RawMessage) json.RawMessage {
	encoded, _ := json.Marshal(values)
	return encoded
}

func objectOrEmpty(value json.RawMessage) json.RawMessage {
	if jsonObject(value) {
		return value
	}
	return json.RawMessage(`{}`)
}

func stringOrEmpty(value json.RawMessage) json.RawMessage {
	var parsed string
	if json.Unmarshal(value, &parsed) == nil {
		encoded, _ := json.Marshal(parsed)
		return encoded
	}
	return json.RawMessage(`""`)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
