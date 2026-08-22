package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/cwiaargs"
)

const (
	cwiaASTMaxNodes       = 200_000
	cwiaASTMaxRoutines    = 4_096
	cwiaASTMaxEdges       = 8_192
	cwiaASTMaxConstDepth  = 16
	cwiaASTMaxConstNodes  = 64
	cwiaASTArtifactField  = "soladyLegacyCWIAImmutableArgs"
	cwiaASTCanonicalName  = "CWIA"
	cwiaASTMaximumAliases = 64
	cwiaASTMaxDepth       = 128
)

var cwiaUintHelperPattern = regexp.MustCompile(`^_getArgUint(?:8|16|24|32|40|48|56|64|72|80|88|96|104|112|120|128|136|144|152|160|168|176|184|192|200|208|216|224|232|240|248|256)$`)

type cwiaASTContract struct {
	id         int64
	name       string
	source     string
	linearized []int64
	routines   []int64
}

type cwiaASTRoutine struct {
	id         int64
	contractID int64
	name       string
	kind       string
	visibility string
	parameters []any
	returns    []any
	node       map[string]any
	edges      []int64
	locals     map[int64]any
	mutations  map[int64]int
}

type cwiaASTHelper struct {
	name       string
	valueType  string
	arguments  int
	fixedBytes int
	multiplier int
	whole      bool
}

type cwiaASTIndex struct {
	contracts       map[int64]*cwiaASTContract
	routines        map[int64]*cwiaASTRoutine
	declarationInit map[int64]any
	constantDecl    map[int64]bool
	helperByID      map[int64]cwiaASTHelper
	canonicalIDs    map[int64]struct{}
	nodes           int
	edges           int
}

type cwiaASTName struct {
	value string
	rank  int
}

type cwiaASTAccess struct {
	offset      int
	valueType   string
	fixedBytes  *int
	remaining   bool
	lengthKey   string
	multiplier  int
	whole       bool
	names       map[string]int
	getters     map[string]struct{}
	role        string
	resolved    string
	fieldName   string
	consumerKey []string
}

func deriveSoladyLegacyCWIAAnalyses(
	output json.RawMessage,
	input json.RawMessage,
) (map[string]json.RawMessage, error) {
	canonicalSources, err := canonicalCWIASourceNames(input)
	if err != nil {
		return nil, err
	}
	if len(canonicalSources) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	index, err := buildCWIAASTIndex(output, canonicalSources)
	if err != nil {
		return nil, err
	}
	analyses := make(map[string]json.RawMessage)
	for _, contract := range index.contracts {
		if _, canonical := index.canonicalIDs[contract.id]; canonical ||
			!inheritsCanonicalCWIA(contract.linearized, index.canonicalIDs) {
			continue
		}
		analysis := analyzeCWIAContract(index, contract)
		encoded, encodeErr := cwiaargs.MarshalAnalysis(analysis)
		if encodeErr != nil {
			analysis = cwiaargs.InvalidAnalysis(cwiaargs.ReasonLimit)
			encoded, encodeErr = cwiaargs.MarshalAnalysis(analysis)
		}
		if encodeErr != nil {
			return nil, errCompilerOutputMalformed
		}
		analyses[contract.source+":"+contract.name] = encoded
	}
	return analyses, nil
}

func equalCWIAAnalyses(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if !bytes.Equal(value, right[name]) {
			return false
		}
	}
	return true
}

func canonicalCWIASourceNames(input json.RawMessage) (map[string]struct{}, error) {
	var document struct {
		Sources map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(input, &document); err != nil || len(document.Sources) == 0 {
		return nil, errCompilerOutputMalformed
	}
	want, err := hex.DecodeString(strings.TrimPrefix(cwiaargs.HelperSHA256, "0x"))
	if err != nil || len(want) != sha256.Size {
		return nil, errors.New("canonical CWIA source digest is invalid")
	}
	result := make(map[string]struct{})
	for name, source := range document.Sources {
		digest := sha256.Sum256([]byte(source.Content))
		if bytes.Equal(digest[:], want) {
			result[name] = struct{}{}
		}
	}
	return result, nil
}

func buildCWIAASTIndex(
	output json.RawMessage,
	canonicalSources map[string]struct{},
) (*cwiaASTIndex, error) {
	document, err := decodeCWIAJSONObject(output)
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok || len(sources) == 0 || len(sources) > maxStandardJSONSources {
		return nil, errCompilerOutputMalformed
	}
	index := &cwiaASTIndex{
		contracts: make(map[int64]*cwiaASTContract), routines: make(map[int64]*cwiaASTRoutine),
		declarationInit: make(map[int64]any), constantDecl: make(map[int64]bool),
		helperByID: make(map[int64]cwiaASTHelper), canonicalIDs: make(map[int64]struct{}),
	}
	for sourceName, rawSource := range sources {
		source, valid := rawSource.(map[string]any)
		ast, astValid := source["ast"].(map[string]any)
		if !valid || !astValid {
			return nil, errCompilerOutputMalformed
		}
		nodes, valid := ast["nodes"].([]any)
		if !valid {
			return nil, errCompilerOutputMalformed
		}
		for _, rawNode := range nodes {
			node, valid := rawNode.(map[string]any)
			if !valid || astString(node, "nodeType") != "ContractDefinition" {
				continue
			}
			contract, parseErr := index.addContract(sourceName, node)
			if parseErr != nil {
				return nil, parseErr
			}
			if _, canonical := canonicalSources[sourceName]; canonical && contract.name == cwiaASTCanonicalName {
				index.canonicalIDs[contract.id] = struct{}{}
			}
		}
	}
	if len(index.contracts) == 0 || len(index.routines) > cwiaASTMaxRoutines || index.nodes > cwiaASTMaxNodes {
		return nil, errCompilerOutputMalformed
	}
	for canonicalID := range index.canonicalIDs {
		contract := index.contracts[canonicalID]
		for _, routineID := range contract.routines {
			routine := index.routines[routineID]
			if helper, supported := canonicalCWIAHelper(routine); supported {
				index.helperByID[routineID] = helper
			}
		}
	}
	if len(index.canonicalIDs) > 0 && len(index.helperByID) != 38 {
		return nil, errCompilerOutputMalformed
	}
	for _, routine := range index.routines {
		index.indexRoutine(routine)
		if index.nodes > cwiaASTMaxNodes || index.edges > cwiaASTMaxEdges {
			return nil, errCompilerOutputMalformed
		}
	}
	return index, nil
}

func (index *cwiaASTIndex) addContract(sourceName string, node map[string]any) (*cwiaASTContract, error) {
	id, ok := astInt64(node["id"])
	if !ok || id <= 0 || index.contracts[id] != nil {
		return nil, errCompilerOutputMalformed
	}
	contract := &cwiaASTContract{
		id: id, name: astString(node, "name"), source: sourceName,
		linearized: astInt64Slice(node["linearizedBaseContracts"]),
	}
	if contract.name == "" || len(contract.linearized) == 0 {
		return nil, errCompilerOutputMalformed
	}
	index.contracts[id] = contract
	children, _ := node["nodes"].([]any)
	for _, rawChild := range children {
		child, valid := rawChild.(map[string]any)
		if !valid {
			continue
		}
		index.nodes++
		switch astString(child, "nodeType") {
		case "FunctionDefinition", "ModifierDefinition":
			routine, err := newCWIAASTRoutine(id, child)
			if err != nil || index.routines[routine.id] != nil {
				return nil, errCompilerOutputMalformed
			}
			index.routines[routine.id] = routine
			contract.routines = append(contract.routines, routine.id)
		case "VariableDeclaration":
			declarationID, ok := astInt64(child["id"])
			if ok && astBool(child, "constant") {
				index.constantDecl[declarationID] = true
				index.declarationInit[declarationID] = child["value"]
			}
		}
	}
	return contract, nil
}

func newCWIAASTRoutine(contractID int64, node map[string]any) (*cwiaASTRoutine, error) {
	id, ok := astInt64(node["id"])
	if !ok || id <= 0 {
		return nil, errCompilerOutputMalformed
	}
	parameters := astParameterList(node["parameters"])
	returns := astParameterList(node["returnParameters"])
	return &cwiaASTRoutine{
		id: id, contractID: contractID, name: astString(node, "name"),
		kind: astString(node, "kind"), visibility: astString(node, "visibility"),
		parameters: parameters, returns: returns, node: node,
		locals: make(map[int64]any), mutations: make(map[int64]int),
	}, nil
}

func (index *cwiaASTIndex) indexRoutine(routine *cwiaASTRoutine) {
	completed := walkCWIAAST(routine.node, func(node map[string]any, _ []string) bool {
		index.nodes++
		switch astString(node, "nodeType") {
		case "FunctionCall":
			if reference, ok := astReferencedDeclaration(node["expression"]); ok {
				if _, exists := index.routines[reference]; exists {
					routine.edges = append(routine.edges, reference)
					index.edges++
				}
			}
		case "ModifierInvocation":
			if reference, ok := astReferencedDeclaration(node["modifierName"]); ok {
				if _, exists := index.routines[reference]; exists {
					routine.edges = append(routine.edges, reference)
					index.edges++
				}
			}
		case "VariableDeclarationStatement":
			declarations, _ := node["declarations"].([]any)
			if len(declarations) == 1 {
				if declaration, valid := declarations[0].(map[string]any); valid {
					if id, ok := astInt64(declaration["id"]); ok {
						routine.locals[id] = node["initialValue"]
						index.declarationInit[id] = node["initialValue"]
					}
				}
			}
		case "Assignment":
			for _, reference := range astExpressionReferences(node["leftHandSide"]) {
				routine.mutations[reference]++
			}
		case "UnaryOperation":
			operator := astString(node, "operator")
			if operator == "++" || operator == "--" || operator == "delete" {
				for _, reference := range astExpressionReferences(node["subExpression"]) {
					routine.mutations[reference]++
				}
			}
		}
		return true
	})
	if !completed {
		index.nodes = cwiaASTMaxNodes + 1
	}
	routine.edges = uniqueInt64s(routine.edges)
}

func canonicalCWIAHelper(routine *cwiaASTRoutine) (cwiaASTHelper, bool) {
	name := routine.name
	switch {
	case name == "_getArgAddress" && len(routine.parameters) == 1:
		return cwiaASTHelper{name: name, valueType: "address", arguments: 1, fixedBytes: 20}, true
	case name == "_getArgBytes32" && len(routine.parameters) == 1:
		return cwiaASTHelper{name: name, valueType: "bytes32", arguments: 1, fixedBytes: 32}, true
	case name == "_getArgBytes" && len(routine.parameters) == 0:
		return cwiaASTHelper{name: name, valueType: "bytes", whole: true}, true
	case name == "_getArgBytes" && len(routine.parameters) == 2:
		return cwiaASTHelper{name: name, valueType: "bytes", arguments: 2, multiplier: 1}, true
	case name == "_getArgUint256Array" && len(routine.parameters) == 2:
		return cwiaASTHelper{name: name, valueType: "uint256[]", arguments: 2, multiplier: 32}, true
	case name == "_getArgBytes32Array" && len(routine.parameters) == 2:
		return cwiaASTHelper{name: name, valueType: "bytes32[]", arguments: 2, multiplier: 32}, true
	case cwiaUintHelperPattern.MatchString(name) && len(routine.parameters) == 1:
		valueType := strings.TrimPrefix(name, "_getArg")
		valueType = strings.ToLower(valueType[:1]) + valueType[1:]
		width, ok := cwiaargs.UintWidth(valueType)
		return cwiaASTHelper{name: name, valueType: valueType, arguments: 1, fixedBytes: width}, ok
	default:
		return cwiaASTHelper{}, false
	}
}

func analyzeCWIAContract(index *cwiaASTIndex, contract *cwiaASTContract) cwiaargs.Analysis {
	relevant := make(map[int64]struct{})
	for _, contractID := range contract.linearized {
		if _, canonical := index.canonicalIDs[contractID]; canonical {
			continue
		}
		base := index.contracts[contractID]
		if base == nil {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonMalformed)
		}
		for _, routineID := range base.routines {
			relevant[routineID] = struct{}{}
		}
	}
	reachable, getterContext := reachableCWIARoutines(index, relevant)
	if len(reachable) == 0 {
		return cwiaargs.UnavailableAnalysis()
	}
	accesses := make(map[string]*cwiaASTAccess)
	for routineID := range reachable {
		routine := index.routines[routineID]
		analysis := analyzeCWIARoutine(index, routine, getterContext[routineID], accesses)
		if analysis != "" {
			return cwiaargs.InvalidAnalysis(analysis)
		}
		if len(accesses) > cwiaargs.MaxFields+1 {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonLimit)
		}
	}
	if len(accesses) == 0 {
		return cwiaargs.UnavailableAnalysis()
	}
	fields, reason := finalizeCWIAAccesses(accesses)
	if reason != "" {
		return cwiaargs.InvalidAnalysis(reason)
	}
	schema, err := cwiaargs.FinalizeSchema(fields)
	if err != nil {
		return cwiaargs.InvalidAnalysis(cwiaargs.ReasonIncomplete)
	}
	analysis, err := cwiaargs.DerivedAnalysis(schema)
	if err != nil {
		return cwiaargs.InvalidAnalysis(cwiaargs.ReasonMalformed)
	}
	return analysis
}

func reachableCWIARoutines(
	index *cwiaASTIndex,
	relevant map[int64]struct{},
) (map[int64]struct{}, map[int64]map[string]int) {
	reachable := make(map[int64]struct{})
	getterContext := make(map[int64]map[string]int)
	for routineID := range relevant {
		routine := index.routines[routineID]
		if routine == nil || (routine.visibility != "public" && routine.visibility != "external") || routine.kind == "constructor" {
			continue
		}
		getter := ""
		if routine.kind == "function" && routine.name != "" && len(routine.parameters) == 0 && len(routine.returns) == 1 {
			getter = routine.name + "()"
		}
		queue := []int64{routineID}
		seen := make(map[int64]struct{})
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if _, visited := seen[current]; visited {
				continue
			}
			seen[current] = struct{}{}
			if _, allowed := relevant[current]; !allowed {
				continue
			}
			reachable[current] = struct{}{}
			if getter != "" {
				if getterContext[current] == nil {
					getterContext[current] = make(map[string]int)
				}
				getterContext[current][getter] = 1
			}
			queue = append(queue, index.routines[current].edges...)
		}
	}
	return reachable, getterContext
}

func analyzeCWIARoutine(
	index *cwiaASTIndex,
	routine *cwiaASTRoutine,
	getters map[string]int,
	accesses map[string]*cwiaASTAccess,
) string {
	reason := ""
	completed := walkCWIAAST(routine.node, func(node map[string]any, ancestors []string) bool {
		if reason != "" || astString(node, "nodeType") != "FunctionCall" {
			return reason == ""
		}
		reference, ok := astReferencedDeclaration(node["expression"])
		helper, supported := index.helperByID[reference]
		if !ok || !supported {
			return true
		}
		access, valid := parseCWIAHelperAccess(index, routine, helper, node)
		if !valid {
			reason = cwiaargs.ReasonUnsupported
			return false
		}
		if access.names == nil {
			access.names = make(map[string]int)
			access.getters = make(map[string]struct{})
		}
		directReturn := cwiaContainsString(ancestors, "Return")
		for getter := range getters {
			name := strings.TrimSuffix(getter, "()")
			rank := 1
			if directReturn {
				rank = 0
			}
			if current, exists := access.names[name]; !exists || rank < current {
				access.names[name] = rank
			}
			access.getters[getter] = struct{}{}
		}
		key := accessKey(access)
		if current := accesses[key]; current != nil {
			mergeCWIAAccess(current, access)
		} else {
			accesses[key] = access
		}
		return true
	})
	if !completed && reason == "" {
		return cwiaargs.ReasonLimit
	}
	return reason
}

func parseCWIAHelperAccess(
	index *cwiaASTIndex,
	routine *cwiaASTRoutine,
	helper cwiaASTHelper,
	node map[string]any,
) (*cwiaASTAccess, bool) {
	arguments, _ := node["arguments"].([]any)
	if helper.whole {
		if len(arguments) != 0 {
			return nil, false
		}
		return &cwiaASTAccess{offset: 0, valueType: "bytes", remaining: true, whole: true}, true
	}
	if len(arguments) != helper.arguments {
		return nil, false
	}
	offsetValue, ok := evalCWIAConstant(index, routine, arguments[0])
	if !ok || !offsetValue.IsInt64() || offsetValue.Sign() < 0 || offsetValue.Int64() > cwiaargs.MaxArguments {
		return nil, false
	}
	access := &cwiaASTAccess{offset: int(offsetValue.Int64()), valueType: helper.valueType}
	if helper.fixedBytes > 0 {
		width := helper.fixedBytes
		access.fixedBytes = &width
		return access, true
	}
	if helper.arguments != 2 {
		return nil, false
	}
	if sizeValue, fixed := evalCWIAConstant(index, routine, arguments[1]); fixed {
		if !sizeValue.IsInt64() || sizeValue.Sign() < 0 || sizeValue.Int64() > cwiaargs.MaxArguments {
			return nil, false
		}
		count := sizeValue.Int64()
		if count > int64(cwiaargs.MaxArguments/helper.multiplier) {
			return nil, false
		}
		width := int(count) * helper.multiplier
		access.fixedBytes = &width
		return access, true
	}
	lengthAccess, valid := resolveCWIALengthAccess(index, routine, arguments[1])
	if !valid {
		return nil, false
	}
	access.lengthKey = accessKey(lengthAccess)
	access.multiplier = helper.multiplier
	return access, true
}

func resolveCWIALengthAccess(
	index *cwiaASTIndex,
	routine *cwiaASTRoutine,
	expression any,
) (*cwiaASTAccess, bool) {
	node, ok := expression.(map[string]any)
	if !ok {
		return nil, false
	}
	for astString(node, "nodeType") == "FunctionCall" && astString(node, "kind") == "typeConversion" {
		arguments, _ := node["arguments"].([]any)
		if len(arguments) != 1 {
			return nil, false
		}
		node, ok = arguments[0].(map[string]any)
		if !ok {
			return nil, false
		}
	}
	if astString(node, "nodeType") == "Identifier" {
		reference, valid := astInt64(node["referencedDeclaration"])
		if !valid || routine.mutations[reference] != 0 {
			return nil, false
		}
		initial := routine.locals[reference]
		if initial == nil {
			return nil, false
		}
		node, ok = initial.(map[string]any)
		if !ok {
			return nil, false
		}
	}
	if astString(node, "nodeType") != "FunctionCall" {
		return nil, false
	}
	reference, ok := astReferencedDeclaration(node["expression"])
	helper, exists := index.helperByID[reference]
	if !ok || !exists || !strings.HasPrefix(helper.valueType, "uint") || helper.arguments != 1 {
		return nil, false
	}
	return parseCWIAHelperAccess(index, routine, helper, node)
}

func finalizeCWIAAccesses(accessMap map[string]*cwiaASTAccess) ([]cwiaargs.Field, string) {
	accesses := make([]*cwiaASTAccess, 0, len(accessMap))
	for _, access := range accessMap {
		accesses = append(accesses, access)
	}
	if len(accesses) > 1 {
		filtered := accesses[:0]
		for _, access := range accesses {
			if !access.whole {
				filtered = append(filtered, access)
			}
		}
		accesses = filtered
	}
	if len(accesses) == 0 || len(accesses) > cwiaargs.MaxFields {
		return nil, cwiaargs.ReasonLimit
	}
	byKey := make(map[string]*cwiaASTAccess, len(accesses))
	for _, access := range accesses {
		byKey[accessKey(access)] = access
	}
	for _, access := range accesses {
		if access.lengthKey == "" {
			continue
		}
		length := byKey[access.lengthKey]
		if length == nil || length.fixedBytes == nil || !strings.HasPrefix(length.valueType, "uint") {
			return nil, cwiaargs.ReasonUnsupported
		}
		length.role = cwiaargs.FieldRoleLength
		length.consumerKey = append(length.consumerKey, accessKey(access))
	}
	for _, access := range accesses {
		if access.role == "" {
			access.role = cwiaargs.FieldRoleValue
		}
		access.resolved = preferredCWIAName(access)
	}
	for _, access := range accesses {
		if access.role != cwiaargs.FieldRoleLength || len(access.consumerKey) == 0 {
			continue
		}
		sort.Strings(access.consumerKey)
		consumer := byKey[access.consumerKey[0]]
		suffix := "_length"
		if consumer != nil && strings.HasSuffix(consumer.valueType, "[]") {
			suffix = "_count"
		}
		if consumer != nil {
			access.resolved = consumer.resolved + suffix
		}
	}
	usedNames := make(map[string]struct{}, len(accesses))
	sort.Slice(accesses, func(i, j int) bool {
		if accesses[i].offset != accesses[j].offset {
			return accesses[i].offset < accesses[j].offset
		}
		return accessKey(accesses[i]) < accessKey(accesses[j])
	})
	for _, access := range accesses {
		name := normalizeCWIAFieldName(access.resolved)
		if name == "" {
			name = fmt.Sprintf("arg_%d", access.offset)
		}
		if _, duplicate := usedNames[name]; duplicate {
			name = fmt.Sprintf("%s_%d", name, access.offset)
		}
		if _, duplicate := usedNames[name]; duplicate || !cwiaargs.ValidateFieldName(name) {
			return nil, cwiaargs.ReasonAmbiguous
		}
		usedNames[name] = struct{}{}
		access.fieldName = name
	}
	cursor := 0
	fields := make([]cwiaargs.Field, len(accesses))
	for index, access := range accesses {
		if access.offset != cursor {
			return nil, cwiaargs.ReasonIncomplete
		}
		var size cwiaargs.Size
		switch {
		case access.fixedBytes != nil:
			size = cwiaargs.FixedSize(*access.fixedBytes)
			cursor += *access.fixedBytes
		case access.remaining:
			if index != len(accesses)-1 {
				return nil, cwiaargs.ReasonAmbiguous
			}
			size = cwiaargs.RemainingSize()
		case access.lengthKey != "":
			if index != len(accesses)-1 {
				return nil, cwiaargs.ReasonUnsupported
			}
			lengthField := byKey[access.lengthKey]
			if lengthField == nil || lengthField.fieldName == "" {
				return nil, cwiaargs.ReasonMalformed
			}
			size = cwiaargs.FieldSize(lengthField.fieldName, access.multiplier)
		default:
			return nil, cwiaargs.ReasonMalformed
		}
		getters := make([]string, 0, len(access.getters))
		for getter := range access.getters {
			getters = append(getters, getter)
		}
		getters = cwiaargs.SortAndUniqueStrings(getters)
		if len(getters) > cwiaargs.MaxGetters {
			getters = getters[:cwiaargs.MaxGetters]
		}
		fields[index] = cwiaargs.Field{
			Name: access.fieldName, Type: access.valueType, Offset: access.offset,
			Role: access.role, Getters: getters, Size: size,
		}
	}
	return fields, ""
}

func preferredCWIAName(access *cwiaASTAccess) string {
	candidates := make([]cwiaASTName, 0, len(access.names))
	for value, rank := range access.names {
		candidates = append(candidates, cwiaASTName{value: value, rank: rank})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].value < candidates[j].value
	})
	if len(candidates) > 0 {
		return candidates[0].value
	}
	return fmt.Sprintf("arg_%d", access.offset)
}

func mergeCWIAAccess(target, source *cwiaASTAccess) {
	for name, rank := range source.names {
		if current, exists := target.names[name]; !exists || rank < current {
			target.names[name] = rank
		}
	}
	for getter := range source.getters {
		if len(target.getters) < cwiaASTMaximumAliases {
			target.getters[getter] = struct{}{}
		}
	}
}

func accessKey(access *cwiaASTAccess) string {
	fixed := "dynamic"
	if access.fixedBytes != nil {
		fixed = strconv.Itoa(*access.fixedBytes)
	} else if access.remaining {
		fixed = "remaining"
	} else if access.lengthKey != "" {
		fixed = access.lengthKey + "*" + strconv.Itoa(access.multiplier)
	}
	return fmt.Sprintf("%d:%s:%s", access.offset, access.valueType, fixed)
}

func evalCWIAConstant(index *cwiaASTIndex, routine *cwiaASTRoutine, value any) (*big.Int, bool) {
	nodes := 0
	seen := make(map[int64]struct{})
	return evalCWIAConstantNode(index, routine, value, 0, &nodes, seen)
}

func evalCWIAConstantNode(
	index *cwiaASTIndex,
	routine *cwiaASTRoutine,
	value any,
	depth int,
	nodes *int,
	seen map[int64]struct{},
) (*big.Int, bool) {
	*nodes++
	if depth > cwiaASTMaxConstDepth || *nodes > cwiaASTMaxConstNodes {
		return nil, false
	}
	node, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	switch astString(node, "nodeType") {
	case "Literal":
		if astString(node, "kind") != "number" {
			return nil, false
		}
		raw := astString(node, "value")
		base := 10
		if hexValue, exists := strings.CutPrefix(raw, "0x"); exists {
			base, raw = 16, hexValue
		}
		result, valid := new(big.Int).SetString(raw, base)
		return boundedCWIAInteger(result, valid)
	case "Identifier":
		reference, valid := astInt64(node["referencedDeclaration"])
		if !valid || routine.mutations[reference] != 0 {
			return nil, false
		}
		if _, cycle := seen[reference]; cycle {
			return nil, false
		}
		initial := routine.locals[reference]
		if initial == nil && index.constantDecl[reference] {
			initial = index.declarationInit[reference]
		}
		if initial == nil {
			return nil, false
		}
		seen[reference] = struct{}{}
		result, ok := evalCWIAConstantNode(index, routine, initial, depth+1, nodes, seen)
		delete(seen, reference)
		return result, ok
	case "UnaryOperation":
		operand, valid := evalCWIAConstantNode(index, routine, node["subExpression"], depth+1, nodes, seen)
		if !valid {
			return nil, false
		}
		result := new(big.Int).Set(operand)
		switch astString(node, "operator") {
		case "+":
		case "-":
			result.Neg(result)
		case "~":
			result.Not(result)
		default:
			return nil, false
		}
		return boundedCWIAInteger(result, true)
	case "BinaryOperation":
		left, leftOK := evalCWIAConstantNode(index, routine, node["leftExpression"], depth+1, nodes, seen)
		right, rightOK := evalCWIAConstantNode(index, routine, node["rightExpression"], depth+1, nodes, seen)
		if !leftOK || !rightOK {
			return nil, false
		}
		result := new(big.Int)
		switch astString(node, "operator") {
		case "+":
			result.Add(left, right)
		case "-":
			result.Sub(left, right)
		case "*":
			result.Mul(left, right)
		case "/":
			if right.Sign() == 0 {
				return nil, false
			}
			result.Quo(left, right)
		case "%":
			if right.Sign() == 0 {
				return nil, false
			}
			result.Rem(left, right)
		case "<<", ">>":
			if !right.IsUint64() || right.Uint64() > 256 {
				return nil, false
			}
			if astString(node, "operator") == "<<" {
				result.Lsh(left, uint(right.Uint64()))
			} else {
				result.Rsh(left, uint(right.Uint64()))
			}
		case "&":
			result.And(left, right)
		case "|":
			result.Or(left, right)
		case "^":
			result.Xor(left, right)
		case "**":
			if !right.IsUint64() || right.Uint64() > 256 {
				return nil, false
			}
			result.Exp(left, right, nil)
		default:
			return nil, false
		}
		return boundedCWIAInteger(result, true)
	case "FunctionCall":
		if astString(node, "kind") != "typeConversion" {
			return nil, false
		}
		arguments, _ := node["arguments"].([]any)
		if len(arguments) != 1 {
			return nil, false
		}
		return evalCWIAConstantNode(index, routine, arguments[0], depth+1, nodes, seen)
	default:
		return nil, false
	}
}

func boundedCWIAInteger(value *big.Int, valid bool) (*big.Int, bool) {
	if !valid || value == nil || value.BitLen() > 256 {
		return nil, false
	}
	return value, true
}

func decodeCWIAJSONObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errCompilerOutputMalformed
	}
	return result, nil
}

func walkCWIAAST(value any, visitor func(map[string]any, []string) bool) bool {
	return walkCWIAASTDepth(value, nil, visitor, 0)
}

func walkCWIAASTDepth(
	value any,
	ancestors []string,
	visitor func(map[string]any, []string) bool,
	depth int,
) bool {
	if depth > cwiaASTMaxDepth {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		nodeType := astString(typed, "nodeType")
		if nodeType != "" {
			if !visitor(typed, ancestors) {
				return false
			}
			ancestors = append(ancestors, nodeType)
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !walkCWIAASTDepth(typed[key], ancestors, visitor, depth+1) {
				return false
			}
		}
	case []any:
		for _, item := range typed {
			if !walkCWIAASTDepth(item, ancestors, visitor, depth+1) {
				return false
			}
		}
	}
	return true
}

func astString(node map[string]any, key string) string {
	value, _ := node[key].(string)
	return value
}

func astBool(node map[string]any, key string) bool {
	value, _ := node[key].(bool)
	return value
}

func astInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func astInt64Slice(value any) []int64 {
	items, _ := value.([]any)
	result := make([]int64, 0, len(items))
	for _, item := range items {
		if parsed, ok := astInt64(item); ok {
			result = append(result, parsed)
		}
	}
	return result
}

func astParameterList(value any) []any {
	node, _ := value.(map[string]any)
	parameters, _ := node["parameters"].([]any)
	return parameters
}

func astReferencedDeclaration(value any) (int64, bool) {
	node, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	if reference, ok := astInt64(node["referencedDeclaration"]); ok {
		return reference, true
	}
	return astReferencedDeclaration(node["expression"])
}

func astExpressionReferences(value any) []int64 {
	result := []int64{}
	walkCWIAAST(value, func(node map[string]any, _ []string) bool {
		if reference, ok := astInt64(node["referencedDeclaration"]); ok {
			result = append(result, reference)
		}
		return true
	})
	return uniqueInt64s(result)
}

func uniqueInt64s(values []int64) []int64 {
	slices.Sort(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func inheritsCanonicalCWIA(linearized []int64, canonical map[int64]struct{}) bool {
	for _, id := range linearized {
		if _, ok := canonical[id]; ok {
			return true
		}
	}
	return false
}

func normalizeCWIAFieldName(value string) string {
	var builder strings.Builder
	for index, character := range value {
		valid := character == '_' || character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() >= 64 {
			break
		}
	}
	return strings.TrimRight(builder.String(), "_")
}

func cwiaContainsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
