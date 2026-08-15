package enrich

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"unicode/utf8"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const tokenABI = `[
  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"amount","type":"uint256"}]},
  {"type":"error","name":"Unauthorized","inputs":[{"name":"caller","type":"address"}]}
]`

func TestSignatureHashUsesEthereumKeccak(t *testing.T) {
	t.Parallel()
	selector := SignatureSelector("transfer(address,uint256)")
	if got := hex.EncodeToString(selector[:]); got != "a9059cbb" {
		t.Fatalf("selector=%s", got)
	}
	if got := SignatureHash("Transfer(address,address,uint256)").String(); got != "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef" {
		t.Fatalf("topic=%s", got)
	}
}

func TestTruncateUTF8BytesPreservesPostgresTextBoundary(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("a", 4095) + "界" + "tail"
	truncated := truncateUTF8Bytes(value, 4096)
	if !utf8.ValidString(truncated) {
		t.Fatalf("truncated warning is not valid UTF-8: %q", truncated)
	}
	if len(truncated) != 4095 || truncated != strings.Repeat("a", 4095) {
		t.Fatalf("truncated warning length/value = %d/%q", len(truncated), truncated)
	}

	invalid := truncateUTF8Bytes("ok\xfftail", 64)
	if !utf8.ValidString(invalid) || invalid != "ok\uFFFDtail" {
		t.Fatalf("invalid warning was not normalized: %q", invalid)
	}
}

func TestABIRegistryDecodesCalldataLogAndRevertWithConfidence(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(10, 100, 1000)
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceSignatureDatabase), []byte(tokenABI)); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(tokenABI)); err != nil {
		t.Fatal(err)
	}
	address := testAddress(0x42)
	calldata := append(selectorBytes("transfer(address,uint256)"), wordBytes(addressWord(address))...)
	calldata = append(calldata, wordBytes(uintWord(123))...)
	decoded := registry.DecodeCalldata(identity, calldata)
	if decoded.Status != DecodeDecoded || decoded.Signature != "transfer(address,uint256)" || decoded.Confidence != ConfidenceVerified || decoded.Source != ABISourceVerified {
		t.Fatalf("decoded=%+v", decoded)
	}
	if decoded.Arguments[0].Value != address.String() || decoded.Arguments[1].Value != "123" {
		t.Fatalf("arguments=%+v", decoded.Arguments)
	}

	topics := []common.Hash{SignatureHash("Transfer(address,address,uint256)"), addressWord(testAddress(1)), addressWord(testAddress(2))}
	logResult := registry.DecodeLog(identity, topics, wordBytes(uintWord(99)))
	if logResult.Status != DecodeDecoded || logResult.Arguments[2].Value != "99" || logResult.Arguments[0].Hashed {
		t.Fatalf("log=%+v", logResult)
	}

	revert := append(selectorBytes("Unauthorized(address)"), wordBytes(addressWord(address))...)
	revertResult := registry.DecodeRevert(identity, revert)
	if revertResult.Status != DecodeDecoded || revertResult.Name != "Unauthorized" || revertResult.Arguments[0].Value != address.String() {
		t.Fatalf("revert=%+v", revertResult)
	}
}

func TestABIRegistryDecodesReceiveAndFallbackEntries(t *testing.T) {
	t.Parallel()
	identity := testABIIdentity(14, 104, 1004)
	registry := NewABIRegistry()
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(`[
		{"type":"receive","stateMutability":"payable"},
		{"type":"fallback","stateMutability":"payable"}
	]`)); err != nil {
		t.Fatal(err)
	}

	receive := registry.DecodeCall(identity, nil, nil, false)
	if receive.Input.Status != DecodeDecoded || receive.Input.Kind != ABIKindFunction ||
		receive.Input.Name != "receive" || receive.Input.Signature != "receive()" ||
		receive.Input.Source != ABISourceVerified || receive.ReturnStatus != ReturnEmpty ||
		len(receive.Input.Arguments) != 0 || len(receive.Returns) != 0 {
		t.Fatalf("receive decoding=%+v", receive)
	}

	for _, input := range [][]byte{{0x01}, selectorBytes("missing()")} {
		fallback := registry.DecodeCall(identity, input, nil, false)
		if fallback.Input.Status != DecodeDecoded || fallback.Input.Name != "fallback" ||
			fallback.Input.Signature != "fallback()" || fallback.Input.Source != ABISourceVerified ||
			fallback.ReturnStatus != ReturnUnavailable || len(fallback.Input.Arguments) != 0 {
			t.Fatalf("fallback input=%x decoding=%+v", input, fallback)
		}
	}

	fallbackOnly := NewABIRegistry()
	if err := fallbackOnly.RegisterJSON(testABIBinding(identity, ABISourceCodeHash), []byte(`[
		{"type":"fallback","stateMutability":"payable"}
	]`)); err != nil {
		t.Fatal(err)
	}
	decoded := fallbackOnly.DecodeCalldata(identity, nil)
	if decoded.Status != DecodeDecoded || decoded.Name != "fallback" || decoded.Signature != "fallback()" {
		t.Fatalf("fallback-only empty calldata=%+v", decoded)
	}
}

func TestABIRegistryDynamicBuiltInAndMalformed(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(11, 101, 1001)
	body := encodeDynamicBytes([]byte("denied"))
	data := append(selectorBytes("Error(string)"), body...)
	result := registry.DecodeRevert(identity, data)
	if result.Status != DecodeDecoded || result.Source != ABISourceBuiltin || result.Arguments[0].Value != "denied" {
		t.Fatalf("result=%+v", result)
	}

	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(tokenABI)); err != nil {
		t.Fatal(err)
	}
	malformed := registry.DecodeCalldata(identity, selectorBytes("transfer(address,uint256)"))
	if malformed.Status != DecodeMalformed || len(malformed.Candidates) != 1 {
		t.Fatalf("malformed=%+v", malformed)
	}
	unknown := registry.DecodeCalldata(identity, []byte{1, 2, 3, 4})
	if unknown.Status != DecodeUnknown {
		t.Fatalf("unknown=%+v", unknown)
	}
}

func TestABIRegistryDecodesOutputsBoundToSelectedFunction(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(13, 103, 1003)
	binding := testABIBinding(identity, ABISourceCodeHash)
	abi := `[{
	  "type":"function","name":"inspect","inputs":[],
	  "outputs":[{"name":"count","type":"uint256"},{"name":"owner","type":"address"}]
	},{"type":"function","name":"touch","inputs":[],"outputs":[]},
	  {"type":"function","name":"legacy","inputs":[]},
	  {"type":"function","name":"message","inputs":[],"outputs":[{"name":"value","type":"string"}]}
	]`
	if err := registry.RegisterJSON(binding, []byte(abi)); err != nil {
		t.Fatal(err)
	}
	output := append(wordBytes(uintWord(7)), wordBytes(addressWord(testAddress(9)))...)
	decoded := registry.DecodeCall(identity, selectorBytes("inspect()"), output, false)
	if decoded.Input.Status != DecodeDecoded || decoded.ReturnStatus != ReturnDecoded ||
		len(decoded.Returns) != 2 || decoded.Returns[0].Value != "7" ||
		decoded.Returns[1].Value != testAddress(9).String() ||
		decoded.Input.SourceAddress != binding.SourceAddress ||
		decoded.Input.SourceCodeHash != binding.SourceCodeHash {
		t.Fatalf("decoded call=%+v", decoded)
	}
	if empty := registry.DecodeCall(identity, selectorBytes("touch()"), nil, false); empty.ReturnStatus != ReturnEmpty {
		t.Fatalf("explicit empty outputs=%+v", empty)
	}
	if unavailable := registry.DecodeCall(identity, selectorBytes("legacy()"), nil, false); unavailable.ReturnStatus != ReturnUnavailable {
		t.Fatalf("missing outputs=%+v", unavailable)
	}
	if malformed := registry.DecodeCall(identity, selectorBytes("message()"), wordBytes(uintWord(4096)), false); malformed.ReturnStatus != ReturnMalformed {
		t.Fatalf("malformed dynamic output=%+v", malformed)
	}
	if reverted := registry.DecodeCall(identity, selectorBytes("inspect()"), output, true); reverted.Input.Status != DecodeDecoded || reverted.ReturnStatus != ReturnNotApplicable || len(reverted.Returns) != 0 {
		t.Fatalf("direct revert call=%+v", reverted)
	}
}

func TestABIRegistryDecodesExactConstructorArguments(t *testing.T) {
	t.Parallel()
	identity := testABIIdentity(14, 140, 141)
	registry := NewABIRegistry()
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(`[
		{"type":"constructor","inputs":[{"name":"initialOwner","type":"address"}]},
		{"type":"function","name":"owner","inputs":[],"outputs":[{"name":"","type":"address"}]}
	]`)); err != nil {
		t.Fatal(err)
	}
	owner := testAddress(0x77)
	encodedOwner := addressWord(owner)
	result := registry.DecodeConstructor(identity, encodedOwner[:])
	if result.Status != DecodeDecoded || result.Kind != ABIKindConstructor ||
		result.Signature != "constructor(address)" || len(result.Arguments) != 1 ||
		result.Arguments[0].Name != "initialOwner" || result.Arguments[0].Value != owner.Hex() {
		t.Fatalf("constructor decoding = %+v", result)
	}
	malformed := registry.DecodeConstructor(identity, []byte{1})
	if malformed.Status != DecodeMalformed || malformed.Kind != ABIKindConstructor {
		t.Fatalf("malformed constructor decoding = %+v", malformed)
	}
}

func TestABIRegistryHashesIndexedDynamicValues(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(12, 102, 1002)
	abi := `[{"type":"event","name":"Message","inputs":[{"name":"value","type":"string","indexed":true}]}]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceProxyImplementation), []byte(abi)); err != nil {
		t.Fatal(err)
	}
	hashed := SignatureHash("hello")
	result := registry.DecodeLog(identity, []common.Hash{SignatureHash("Message(string)"), hashed}, nil)
	if result.Status != DecodeDecoded || !result.Arguments[0].Hashed || result.Arguments[0].Value != hashed.String() {
		t.Fatalf("result=%+v", result)
	}
}

func TestABIRegistryDecodesAnonymousEventWithoutSignatureTopic(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(10, 100, 1000)
	abi := []byte(`[{"type":"event","name":"AnonymousValue","anonymous":true,"inputs":[{"name":"owner","type":"address","indexed":true},{"name":"value","type":"uint256"}]}]`)
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), abi); err != nil {
		t.Fatal(err)
	}
	owner := testAddress(0x42)
	result := registry.DecodeLog(identity, []common.Hash{addressWord(owner)}, wordBytes(uintWord(9)))
	if result.Status != DecodeDecoded || result.Signature != "AnonymousValue(address,uint256)" ||
		len(result.Arguments) != 2 || result.Arguments[0].Value != owner.String() ||
		result.Arguments[1].Value != "9" {
		t.Fatalf("anonymous result=%+v", result)
	}
}

func TestABIRegistryIsolatesTargetCodeRangeAndFork(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(20, 200, 2000)
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(tokenABI)); err != nil {
		t.Fatal(err)
	}
	calldata := append(selectorBytes("transfer(address,uint256)"), wordBytes(addressWord(testAddress(1)))...)
	calldata = append(calldata, wordBytes(uintWord(1))...)

	variants := []ABIIdentity{
		{ChainID: "2", Address: identity.Address, CodeHash: identity.CodeHash, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash},
		{ChainID: identity.ChainID, Address: testAddress(21), CodeHash: identity.CodeHash, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash},
		{ChainID: identity.ChainID, Address: identity.Address, CodeHash: uintWord(201), BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash},
		{ChainID: identity.ChainID, Address: identity.Address, CodeHash: identity.CodeHash, BlockNumber: identity.BlockNumber, BlockHash: uintWord(2001)},
		{ChainID: identity.ChainID, Address: identity.Address, CodeHash: identity.CodeHash, BlockNumber: identity.BlockNumber + 1, BlockHash: identity.BlockHash},
	}
	for _, other := range variants {
		if result := registry.DecodeCalldata(other, calldata); result.Status != DecodeUnknown {
			t.Fatalf("identity %+v leaked result %+v", other, result)
		}
	}
	end := identity.BlockNumber - 1
	invalid := testABIBinding(identity, ABISourceVerified)
	invalid.ValidToBlock = &end
	if err := registry.RegisterJSON(invalid, []byte(tokenABI)); err == nil {
		t.Fatal("out-of-range binding unexpectedly registered")
	}
	guess := testABIBinding(identity, ABISourceSignatureDatabase)
	guess.SourceCodeHash = uintWord(999)
	if err := registry.RegisterJSON(guess, []byte(tokenABI)); err == nil {
		t.Fatal("signature binding with foreign source identity unexpectedly registered")
	}
}

func TestABIRegistryReportsEqualConfidenceSelectorCollision(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(30, 300, 3000)
	// These real signatures share selector 0x42966c68.
	collisionABI := `[
	  {"type":"function","name":"burn","inputs":[{"name":"value","type":"uint256"}]},
	  {"type":"function","name":"collate_propagate_storage","inputs":[{"name":"value","type":"bytes16"}]}
	]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceSignatureDatabase), []byte(collisionABI)); err != nil {
		t.Fatal(err)
	}
	if SignatureSelector("burn(uint256)") != SignatureSelector("collate_propagate_storage(bytes16)") {
		t.Fatal("collision fixture no longer collides")
	}
	calldata := append(selectorBytes("burn(uint256)"), make([]byte, 32)...)
	result := registry.DecodeCalldata(identity, calldata)
	if result.Status != DecodeAmbiguous || result.Confidence != ConfidenceGuess || len(result.Candidates) != 2 {
		t.Fatalf("collision result=%+v", result)
	}
}

func TestABIRegistryDecodesTupleAndDynamicArray(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(40, 400, 4000)
	abi := `[{"type":"function","name":"mix","inputs":[
	  {"name":"pair","type":"tuple","components":[{"name":"count","type":"uint256"},{"name":"owner","type":"address"}]},
	  {"name":"values","type":"uint256[]"}
	]}]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(abi)); err != nil {
		t.Fatal(err)
	}
	payload := append(wordBytes(uintWord(7)), wordBytes(addressWord(testAddress(9)))...)
	payload = append(payload, wordBytes(uintWord(96))...)
	payload = append(payload, wordBytes(uintWord(2))...)
	payload = append(payload, wordBytes(uintWord(11))...)
	payload = append(payload, wordBytes(uintWord(12))...)
	result := registry.DecodeCalldata(identity, append(selectorBytes("mix((uint256,address),uint256[])"), payload...))
	if result.Status != DecodeDecoded || len(result.Arguments) != 2 {
		t.Fatalf("tuple/array result=%+v", result)
	}
}

func TestABIRegistryRetainsValidatedCalldataParameterStructure(t *testing.T) {
	t.Parallel()
	identity := testABIIdentity(43, 403, 4003)
	registry := NewABIRegistry()
	abiJSON := `[{
	  "type":"function","name":"configure","inputs":[
	    {"name":"config","type":"tuple","internalType":"struct Fixture.Config","components":[
	      {"name":"owner","type":"address"},
	      {"name":"rules","type":"tuple[]","internalType":"struct Fixture.Rule[]","components":[
	        {"name":"threshold","type":"uint16"},{"name":"","type":"bool"}
	      ]}
	    ]},
	    {"name":"matrix","type":"uint256[][2]"},
	    {"name":"batches","type":"tuple[]","internalType":"struct Fixture.Batch[]","components":[
	      {"name":"recipient","type":"address"},{"name":"amount","type":"int32"}
	    ]}
	  ],"outputs":[]
	}]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(abiJSON)); err != nil {
		t.Fatal(err)
	}
	packingABI := strings.Replace(abiJSON, `{"name":"","type":"bool"}`, `{"name":"enabled","type":"bool"}`, 1)
	parsed, err := gethabi.JSON(strings.NewReader(packingABI))
	if err != nil {
		t.Fatal(err)
	}
	type rule struct {
		Threshold uint16
		Enabled   bool
	}
	type config struct {
		Owner common.Address
		Rules []rule
	}
	type batch struct {
		Recipient common.Address
		Amount    int32
	}
	payload, err := parsed.Methods["configure"].Inputs.Pack(
		config{Owner: testAddress(1), Rules: []rule{{Threshold: 7, Enabled: true}}},
		[2][]*big.Int{{}, {big.NewInt(9), big.NewInt(10)}},
		[]batch{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := registry.DecodeCalldata(identity, append(parsed.Methods["configure"].ID, payload...))
	if result.Status != DecodeDecoded || len(result.Arguments) != 3 {
		t.Fatalf("decoded=%+v", result)
	}
	configArgument := result.Arguments[0]
	if configArgument.InternalType != "struct Fixture.Config" || len(configArgument.Components) != 2 ||
		configArgument.Components[0].Name != "owner" || configArgument.Components[0].Components == nil ||
		configArgument.Components[1].Type != "tuple[]" ||
		configArgument.Components[1].InternalType != "struct Fixture.Rule[]" ||
		len(configArgument.Components[1].Components) != 2 ||
		configArgument.Components[1].Components[1].Name != "" {
		t.Fatalf("config shape=%+v", configArgument)
	}
	if result.Arguments[1].Type != "uint256[][2]" || result.Arguments[1].Components == nil ||
		len(result.Arguments[1].Components) != 0 || result.Arguments[2].InternalType != "struct Fixture.Batch[]" ||
		len(result.Arguments[2].Components) != 2 {
		t.Fatalf("array shapes=%+v", result.Arguments)
	}
	values, ok := result.Arguments[1].Value.([]any)
	if !ok || len(values) != 2 || len(values[0].([]any)) != 0 || len(values[1].([]any)) != 2 {
		t.Fatalf("matrix value=%#v", result.Arguments[1].Value)
	}
	stored, err := json.Marshal(result.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("internalType")) || bytes.Contains(stored, []byte("components")) {
		t.Fatalf("abi@3 persisted value shape changed: %s", stored)
	}
}

func TestABIRegistryRejectsUnboundedOrContradictoryParameterMetadata(t *testing.T) {
	t.Parallel()
	identity := testABIIdentity(44, 404, 4004)
	for _, abiJSON := range []string{
		`[{"type":"function","name":"bad","inputs":[{"name":"value","type":"uint256","components":[{"name":"nested","type":"uint8"}]}]}]`,
		`[{"type":"function","name":"bad","inputs":[{"name":"value","type":"uint256","internalType":" uint256"}]}]`,
		`[{"type":"function","name":"bad","inputs":[{"name":"value","type":"uint256","internalType":"` + strings.Repeat("x", DefaultDecodeLimits().MaxSignatureBytes+1) + `"}]}]`,
	} {
		registry := NewABIRegistry()
		if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(abiJSON)); err == nil {
			t.Fatalf("accepted invalid parameter metadata: %.120s", abiJSON)
		}
	}
}

func TestABIDecodeBudgetIsGlobalAcrossAliasedDynamicOffsets(t *testing.T) {
	t.Parallel()
	valueType, err := parseABIType(abiParameter{Type: "bytes[][]"}, 1, DefaultDecodeLimits().MaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	payload := encodeAliasedNestedDynamicBytes(8, 8, []byte("x"))
	for _, test := range []struct {
		name   string
		change func(*DecodeLimits)
	}{
		{name: "nodes", change: func(limits *DecodeLimits) { limits.MaxDecodeNodes = 32 }},
		{name: "work", change: func(limits *DecodeLimits) { limits.MaxDecodeWork = 64 }},
		{name: "bytes", change: func(limits *DecodeLimits) { limits.MaxDecodedBytes = 1024 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limits := DefaultDecodeLimits()
			test.change(&limits)
			if _, err := decodeABIValues([]*abiType{valueType}, payload, limits); !errors.Is(err, ErrABIDecodeLimit) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestABIDecodeBudgetIsSharedAcrossSelectorCandidates(t *testing.T) {
	t.Parallel()
	limits := DefaultDecodeLimits()
	limits.MaxDecodeNodes = 1
	registry, err := NewABIRegistryWithLimits(limits)
	if err != nil {
		t.Fatal(err)
	}
	identity := testABIIdentity(41, 401, 4001)
	collisionABI := `[
	  {"type":"function","name":"burn","inputs":[{"name":"value","type":"uint256"}]},
	  {"type":"function","name":"collate_propagate_storage","inputs":[{"name":"value","type":"bytes16"}]}
	]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceSignatureDatabase), []byte(collisionABI)); err != nil {
		t.Fatal(err)
	}
	result := registry.DecodeCalldata(identity, append(selectorBytes("burn(uint256)"), make([]byte, 32)...))
	if result.Status != DecodeMalformed || !strings.Contains(result.Warning, ErrABIDecodeLimit.Error()) {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuiltinErrorsRemainDecoderLocal(t *testing.T) {
	t.Parallel()
	identity := testABIIdentity(42, 402, 4002)
	registry := NewABIRegistry()
	material := `[
	  {"type":"error","name":"Error","inputs":[{"name":"message","type":"string"}]},
	  {"type":"error","name":"Panic","inputs":[{"name":"code","type":"uint256"}]},
	  {"type":"error","name":"Custom","inputs":[{"name":"code","type":"uint256"}]}
	]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceSignatureDatabase), []byte(material)); err != nil {
		t.Fatal(err)
	}
	result := registry.DecodeRevert(identity, append(selectorBytes("Error(string)"), encodeDynamicBytes([]byte("local"))...))
	if result.Status != DecodeDecoded || result.Source != ABISourceBuiltin || result.Signature != "Error(string)" {
		t.Fatalf("result=%+v", result)
	}
	observations := []abiObservation{
		{objectKind: abiObjectTraceRevert, input: append(selectorBytes("Error(string)"), make([]byte, 32)...)},
		{objectKind: abiObjectTraceRevert, input: append(selectorBytes("Panic(uint256)"), make([]byte, 32)...)},
		{objectKind: abiObjectTraceRevert, input: append(selectorBytes("Custom(uint256)"), make([]byte, 32)...)},
	}
	identifiers := observedABIIdentifiers(observations)
	if len(identifiers) != 1 || identifiers[0].identifier != "0x"+hex.EncodeToString(selectorBytes("Custom(uint256)")) {
		t.Fatalf("identifiers=%+v", identifiers)
	}
	for _, signature := range []string{"Error(string)", "Panic(uint256)"} {
		selector := selectorBytes(signature)
		name, inputType := "Error", "string"
		if signature == "Panic(uint256)" {
			name, inputType = "Panic", "uint256"
		}
		entry := []byte(`{"type":"error","name":"` + name + `","inputs":[{"name":"value","type":"` + inputType + `"}]}`)
		identifier := abiIdentifier{kind: ABIKindError, identifier: "0x" + hex.EncodeToString(selector), bytes: selector}
		if validSignatureCandidate(identifier, signature, entry, DefaultDecodeLimits()) {
			t.Fatalf("accepted decoder-local builtin %s as a signature candidate", signature)
		}
	}
}

func FuzzABIRegistryBoundedMalformed(f *testing.F) {
	identity := testABIIdentity(50, 500, 5000)
	f.Add([]byte{})
	f.Add(selectorBytes("transfer(address,uint256)"))
	f.Add(append(selectorBytes("Error(string)"), make([]byte, 32)...))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 8192 {
			payload = payload[:8192]
		}
		registry := NewABIRegistry()
		if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(tokenABI)); err != nil {
			t.Fatal(err)
		}
		_ = registry.DecodeCalldata(identity, payload)
		_ = registry.DecodeRevert(identity, payload)
		var topic common.Hash
		copy(topic[:], payload)
		_ = registry.DecodeLog(identity, []common.Hash{topic}, payload)
	})
}

func FuzzABIRegistryTupleArrayAndError(f *testing.F) {
	identity := testABIIdentity(60, 600, 6000)
	abi := `[
	  {"type":"function","name":"nested","inputs":[{"name":"items","type":"tuple[]","components":[{"name":"value","type":"bytes"}]}]},
	  {"type":"error","name":"NestedFailure","inputs":[{"name":"values","type":"uint256[]"}]}
	]`
	f.Add(selectorBytes("nested((bytes)[])"), []byte{})
	f.Add(selectorBytes("NestedFailure(uint256[])"), make([]byte, 64))
	f.Fuzz(func(t *testing.T, selector, body []byte) {
		if len(selector) > 4 {
			selector = selector[:4]
		}
		if len(body) > 8192 {
			body = body[:8192]
		}
		payload := append(append([]byte(nil), selector...), body...)
		registry := NewABIRegistry()
		if err := registry.RegisterJSON(testABIBinding(identity, ABISourceVerified), []byte(abi)); err != nil {
			t.Fatal(err)
		}
		_ = registry.DecodeCalldata(identity, payload)
		_ = registry.DecodeRevert(identity, payload)
	})
}

func FuzzABIDecodeAliasedOffsetsBudget(f *testing.F) {
	f.Add(uint8(8), uint8(8), []byte("x"))
	f.Fuzz(func(t *testing.T, outerByte, innerByte uint8, value []byte) {
		outer := int(outerByte%32) + 1
		inner := int(innerByte%32) + 1
		if len(value) > 64 {
			value = value[:64]
		}
		valueType, err := parseABIType(abiParameter{Type: "bytes[][]"}, 1, DefaultDecodeLimits().MaxDepth)
		if err != nil {
			t.Fatal(err)
		}
		limits := DefaultDecodeLimits()
		limits.MaxArrayElements = 32
		limits.MaxDecodeNodes = 256
		limits.MaxDecodeWork = 1024
		limits.MaxDecodedBytes = 64 << 10
		_, _ = decodeABIValues([]*abiType{valueType}, encodeAliasedNestedDynamicBytes(outer, inner, value), limits)
	})
}

func encodeDynamicBytes(value []byte) []byte {
	result := append([]byte(nil), wordBytes(uintWord(32))...)
	result = append(result, wordBytes(uintWord(uint64(len(value))))...)
	result = append(result, value...)
	result = append(result, bytes.Repeat([]byte{0}, paddedLength(len(value))-len(value))...)
	return result
}

// encodeAliasedNestedDynamicBytes encodes one bytes[][] argument whose outer
// elements all point at the same inner array and whose inner elements all point
// at the same bytes value. Its wire size is linear while a decoder that resets
// limits per branch can perform outer*inner work.
func encodeAliasedNestedDynamicBytes(outer, inner int, value []byte) []byte {
	result := append([]byte(nil), wordBytes(uintWord(32))...)
	result = append(result, wordBytes(uintWord(uint64(outer)))...)
	for range outer {
		result = append(result, wordBytes(uintWord(uint64(outer*32)))...)
	}
	result = append(result, wordBytes(uintWord(uint64(inner)))...)
	for range inner {
		result = append(result, wordBytes(uintWord(uint64(inner*32)))...)
	}
	result = append(result, wordBytes(uintWord(uint64(len(value))))...)
	result = append(result, value...)
	return append(result, bytes.Repeat([]byte{0}, paddedLength(len(value))-len(value))...)
}

func testAddress(last byte) common.Address {
	var address common.Address
	address[19] = last
	return address
}

func addressWord(address common.Address) common.Hash {
	var word common.Hash
	copy(word[12:], address[:])
	return word
}

func uintWord(value uint64) common.Hash {
	var word common.Hash
	for index := range 8 {
		word[31-index] = byte(value)
		value >>= 8
	}
	return word
}

func testABIIdentity(block, codeHash, blockHash uint64) ABIIdentity {
	return ABIIdentity{
		ChainID: "1", Address: testAddress(byte(block)), CodeHash: uintWord(codeHash),
		BlockNumber: block, BlockHash: uintWord(blockHash),
	}
}

func testABIBinding(identity ABIIdentity, source ABISource) ABIBinding {
	binding := ABIBinding{
		Identity: identity, Source: source, SourceAddress: identity.Address,
		SourceCodeHash: identity.CodeHash, ValidFromBlock: identity.BlockNumber,
	}
	if source == ABISourceProxyImplementation || source == ABISourceCodeHash {
		binding.SourceAddress = testAddress(0xee)
		binding.SourceCodeHash = uintWord(0xee)
	}
	return binding
}
