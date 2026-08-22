package verify

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/islishude/etherview/internal/cwiaargs"
)

func TestAnalyzeCanonicalCWIAASTDerivesDynamicLayout(t *testing.T) {
	t.Parallel()
	index, contract := dynamicCWIAASTFixture()
	analysis := analyzeCWIAContract(index, contract)
	if analysis.Status != cwiaargs.AnalysisStatusDerived || analysis.Schema == nil {
		t.Fatalf("analysis=%+v", analysis)
	}
	fields := analysis.Schema.Fields
	if len(fields) != 4 {
		t.Fatalf("fields=%+v", fields)
	}
	wantNames := []string{"owner", "number", "data_length", "data"}
	wantTypes := []string{"address", "uint256", "uint16", "bytes"}
	wantOffsets := []int{0, 20, 52, 54}
	for index, field := range fields {
		if field.Name != wantNames[index] || field.Type != wantTypes[index] || field.Offset != wantOffsets[index] {
			t.Fatalf("field %d=%+v", index, field)
		}
	}
	if fields[2].Role != cwiaargs.FieldRoleLength || fields[3].Size.Kind != cwiaargs.SizeKindField ||
		fields[3].Size.Field != "data_length" || fields[3].Size.Multiplier != 1 {
		t.Fatalf("dynamic fields=%+v", fields[2:])
	}
}

func TestAnalyzeCanonicalCWIAASTIgnoresDeadAndForgedCalls(t *testing.T) {
	t.Parallel()
	index, contract := dynamicCWIAASTFixture()
	dead := astFunction(40, "dead", "private", []any{
		astReturn(astCall(100, astLiteral(7))),
	})
	forged := astFunction(50, "forged", "external", []any{
		astReturn(astCall(9999, astLiteral(7))),
	})
	index.routines[40] = mustCWIAASTRoutine(t, 2, dead)
	index.routines[50] = mustCWIAASTRoutine(t, 2, forged)
	contract.routines = append(contract.routines, 40, 50)
	index.indexRoutine(index.routines[40])
	index.indexRoutine(index.routines[50])
	analysis := analyzeCWIAContract(index, contract)
	if analysis.Status != cwiaargs.AnalysisStatusDerived || analysis.Schema == nil || len(analysis.Schema.Fields) != 4 {
		t.Fatalf("analysis=%+v", analysis)
	}
}

func TestAnalyzeCanonicalCWIAASTRejectsUnprovableLengthAndIncompleteCoverage(t *testing.T) {
	t.Parallel()
	index, contract := dynamicCWIAASTFixture()
	data := index.routines[30]
	body := data.node["body"].(map[string]any)
	statements := body["statements"].([]any)
	returnNode := statements[1].(map[string]any)
	call := returnNode["expression"].(map[string]any)
	call["arguments"] = []any{astLiteral(54), map[string]any{
		"nodeType": "Identifier", "name": "runtimeLength", "referencedDeclaration": int64(999),
	}}
	index.indexRoutine(data)
	analysis := analyzeCWIAContract(index, contract)
	if analysis.Status != cwiaargs.AnalysisStatusInvalid || analysis.Reason != cwiaargs.ReasonUnsupported {
		t.Fatalf("analysis=%+v", analysis)
	}

	index, contract = dynamicCWIAASTFixture()
	owner := index.routines[10].node["body"].(map[string]any)["statements"].([]any)[0].(map[string]any)
	owner["expression"].(map[string]any)["arguments"] = []any{astLiteral(1)}
	analysis = analyzeCWIAContract(index, contract)
	if analysis.Status != cwiaargs.AnalysisStatusInvalid || analysis.Reason != cwiaargs.ReasonIncomplete {
		t.Fatalf("analysis=%+v", analysis)
	}
}

func TestNormalizeSolidityAnalysisVersionIsServerOwned(t *testing.T) {
	t.Parallel()
	request := SubmissionV2{Kind: JobAddress, Language: LanguageSolidity, SolidityAnalysis: 99}
	normalizeSolidityAnalysisVersion(&request)
	if request.SolidityAnalysis != cwiaargs.AnalysisVersion {
		t.Fatalf("analysis version=%d", request.SolidityAnalysis)
	}
	request = SubmissionV2{Kind: JobAddress, Language: LanguageYul, SolidityAnalysis: 99}
	normalizeSolidityAnalysisVersion(&request)
	if request.SolidityAnalysis != 0 {
		t.Fatalf("Yul analysis version=%d", request.SolidityAnalysis)
	}
}

func TestCanonicalCWIAHelperSurfaceIsExact(t *testing.T) {
	t.Parallel()
	names := []struct {
		name       string
		parameters int
	}{
		{"_getArgBytes", 0}, {"_getArgBytes", 2}, {"_getArgAddress", 1},
		{"_getArgUint256Array", 2}, {"_getArgBytes32Array", 2}, {"_getArgBytes32", 1},
	}
	for bits := 8; bits <= 256; bits += 8 {
		names = append(names, struct {
			name       string
			parameters int
		}{"_getArgUint" + strconv.Itoa(bits), 1})
	}
	if len(names) != 38 {
		t.Fatalf("helper count=%d", len(names))
	}
	for index, test := range names {
		routine := &cwiaASTRoutine{id: int64(index + 1), name: test.name, parameters: make([]any, test.parameters)}
		if _, supported := canonicalCWIAHelper(routine); !supported {
			t.Fatalf("helper %s/%d is unsupported", test.name, test.parameters)
		}
	}
	if _, supported := canonicalCWIAHelper(&cwiaASTRoutine{name: "_getArgUint7", parameters: []any{nil}}); supported {
		t.Fatal("forged helper was accepted")
	}
}

func TestCWIAConstantEvaluatorSupportsBoundedExpressionsAndRejectsMutation(t *testing.T) {
	t.Parallel()
	index := &cwiaASTIndex{declarationInit: map[int64]any{}, constantDecl: map[int64]bool{}}
	routine := &cwiaASTRoutine{locals: map[int64]any{}, mutations: map[int64]int{}}
	expression := map[string]any{
		"nodeType": "BinaryOperation", "operator": "+",
		"leftExpression": astLiteral(20), "rightExpression": astLiteral(32),
	}
	value, ok := evalCWIAConstant(index, routine, expression)
	if !ok || value.Int64() != 52 {
		t.Fatalf("constant=%v ok=%t", value, ok)
	}
	routine.locals[7] = expression
	identifier := map[string]any{"nodeType": "Identifier", "referencedDeclaration": int64(7)}
	value, ok = evalCWIAConstant(index, routine, identifier)
	if !ok || value.Int64() != 52 {
		t.Fatalf("local constant=%v ok=%t", value, ok)
	}
	routine.mutations[7] = 1
	if _, ok := evalCWIAConstant(index, routine, identifier); ok {
		t.Fatal("mutated local was accepted")
	}
}

func dynamicCWIAASTFixture() (*cwiaASTIndex, *cwiaASTContract) {
	index := &cwiaASTIndex{
		contracts: map[int64]*cwiaASTContract{}, routines: map[int64]*cwiaASTRoutine{},
		declarationInit: map[int64]any{}, constantDecl: map[int64]bool{},
		helperByID: map[int64]cwiaASTHelper{
			100: {name: "_getArgAddress", valueType: "address", arguments: 1, fixedBytes: 20},
			101: {name: "_getArgUint256", valueType: "uint256", arguments: 1, fixedBytes: 32},
			102: {name: "_getArgUint16", valueType: "uint16", arguments: 1, fixedBytes: 2},
			103: {name: "_getArgBytes", valueType: "bytes", arguments: 2, multiplier: 1},
		},
		canonicalIDs: map[int64]struct{}{1: {}},
	}
	index.contracts[1] = &cwiaASTContract{id: 1, name: "CWIA", source: "CWIA.sol", linearized: []int64{1}}
	contract := &cwiaASTContract{id: 2, name: "Account", source: "Account.sol", linearized: []int64{2, 1}}
	index.contracts[2] = contract
	owner := astFunction(10, "owner", "public", []any{astReturn(astCall(100, astLiteral(0)))})
	number := astFunction(20, "number", "public", []any{astReturn(astCall(101, astLiteral(20)))})
	data := astFunction(30, "data", "public", []any{
		map[string]any{
			"nodeType":     "VariableDeclarationStatement",
			"declarations": []any{map[string]any{"nodeType": "VariableDeclaration", "id": int64(301), "name": "length"}},
			"initialValue": astCall(102, astLiteral(52)),
		},
		astReturn(astCall(103, astLiteral(54), map[string]any{
			"nodeType": "Identifier", "name": "length", "referencedDeclaration": int64(301),
		})),
	})
	for _, node := range []map[string]any{owner, number, data} {
		routine, _ := newCWIAASTRoutine(2, node)
		index.routines[routine.id] = routine
		contract.routines = append(contract.routines, routine.id)
		index.indexRoutine(routine)
	}
	return index, contract
}

func astFunction(id int64, name, visibility string, statements []any) map[string]any {
	return map[string]any{
		"nodeType": "FunctionDefinition", "id": id, "name": name,
		"kind": "function", "visibility": visibility,
		"parameters":       map[string]any{"parameters": []any{}},
		"returnParameters": map[string]any{"parameters": []any{map[string]any{"nodeType": "VariableDeclaration"}}},
		"body":             map[string]any{"nodeType": "Block", "statements": statements},
	}
}

func astReturn(expression map[string]any) map[string]any {
	return map[string]any{"nodeType": "Return", "expression": expression}
}

func astCall(reference int64, arguments ...any) map[string]any {
	return map[string]any{
		"nodeType": "FunctionCall", "kind": "functionCall",
		"expression": map[string]any{
			"nodeType": "Identifier", "name": "helper", "referencedDeclaration": reference,
		},
		"arguments": arguments,
	}
}

func astLiteral(value int64) map[string]any {
	return map[string]any{
		"nodeType": "Literal", "kind": "number", "value": strconv.FormatInt(value, 10),
	}
}

func mustCWIAASTRoutine(t *testing.T, contractID int64, node map[string]any) *cwiaASTRoutine {
	t.Helper()
	routine, err := newCWIAASTRoutine(contractID, node)
	if err != nil {
		t.Fatal(err)
	}
	return routine
}

func FuzzCWIAASTWalkerNeverPanics(f *testing.F) {
	f.Add([]byte(`{"nodeType":"Literal","kind":"number","value":"0"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(_ *testing.T, raw []byte) {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return
		}
		walkCWIAAST(value, func(map[string]any, []string) bool { return true })
	})
}
