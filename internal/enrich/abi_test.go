package enrich

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
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

	topics := []Word{SignatureHash("Transfer(address,address,uint256)"), addressWord(testAddress(1)), addressWord(testAddress(2))}
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

func TestABIRegistryHashesIndexedDynamicValues(t *testing.T) {
	t.Parallel()
	registry := NewABIRegistry()
	identity := testABIIdentity(12, 102, 1002)
	abi := `[{"type":"event","name":"Message","inputs":[{"name":"value","type":"string","indexed":true}]}]`
	if err := registry.RegisterJSON(testABIBinding(identity, ABISourceProxyImplementation), []byte(abi)); err != nil {
		t.Fatal(err)
	}
	hashed := SignatureHash("hello")
	result := registry.DecodeLog(identity, []Word{SignatureHash("Message(string)"), hashed}, nil)
	if result.Status != DecodeDecoded || !result.Arguments[0].Hashed || result.Arguments[0].Value != hashed.String() {
		t.Fatalf("result=%+v", result)
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
		var topic Word
		copy(topic[:], payload)
		_ = registry.DecodeLog(identity, []Word{topic}, payload)
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

func testAddress(last byte) Address {
	var address Address
	address[19] = last
	return address
}

func addressWord(address Address) Word {
	var word Word
	copy(word[12:], address[:])
	return word
}

func uintWord(value uint64) Word {
	var word Word
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
	if source == ABISourceProxyImplementation {
		binding.SourceAddress = testAddress(0xee)
		binding.SourceCodeHash = uintWord(0xee)
	}
	return binding
}
