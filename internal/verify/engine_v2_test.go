package verify

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestLocateAuxdataFindsMultipleAuthenticatedRanges(t *testing.T) {
	t.Parallel()
	first := solidityAuxdata(t, map[string]any{"ipfs": []byte{1, 2, 3}})
	firstModified := solidityAuxdata(t, map[string]any{"ipfs": []byte{4, 5, 6}})
	second := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 30}})
	secondModified := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 31}})
	original := append([]byte{0x60, 0x00}, first...)
	original = append(original, 0x5b)
	original = append(original, second...)
	modified := append([]byte{0x60, 0x00}, firstModified...)
	modified = append(modified, 0x5b)
	modified = append(modified, secondModified...)

	auxdata, err := LocateAuxdata(original, modified, LanguageSolidity)
	if err != nil {
		t.Fatalf("locate auxdata: %v", err)
	}
	if len(auxdata) != 2 {
		t.Fatalf("auxdata count = %d, want 2", len(auxdata))
	}
	if auxdata["1"].Offset != 2 || auxdata["2"].Offset != uint64(3+len(first)) {
		t.Fatalf("unexpected offsets: %#v", auxdata)
	}

	modified[0] ^= 1
	if _, err := LocateAuxdata(original, modified, LanguageSolidity); err == nil {
		t.Fatal("expected non-auxdata difference rejection")
	}
}

func TestTransformedCodeMatchesLibrariesImmutablesAndConstructor(t *testing.T) {
	t.Parallel()
	aux := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 30}})
	compiled := make([]byte, 96)
	copy(compiled[2:], aux)
	deployed := append([]byte(nil), compiled...)
	library := []byte("01234567890123456789")
	copy(deployed[32:52], library)
	copy(deployed[60:62], []byte{0xaa, 0xbb})
	copy(deployed[70:72], []byte{0xaa, 0xbb})
	argument := make([]byte, 32)
	big.NewInt(42).FillBytes(argument)
	deployed = append(deployed, argument...)

	match, err := matchTransformedCode(deployed, compiled, codeTransformationInput{
		Auxdata: map[string]AuxdataValue{
			"1": {Offset: 2, Value: "0x" + hex.EncodeToString(aux)},
		},
		LinkReferences: map[string]map[string][]bytecodeRange{
			"lib.sol": {"L": {{Start: 32, Length: 20}}},
		},
		ImmutableReferences: map[string][]bytecodeRange{
			"7": {{Start: 60, Length: 2}, {Start: 70, Length: 2}},
		},
		ABI: json.RawMessage(`[
			{"type":"constructor","inputs":[{"name":"value","type":"uint256"}]}
		]`),
		Creation: true,
	})
	if err != nil {
		t.Fatalf("match transformed creation: %v", err)
	}
	if match == nil || match.MatchType != VerificationMatchFull {
		t.Fatalf("match = %#v", match)
	}
	if match.Values.Libraries["lib.sol:L"] != "0x"+hex.EncodeToString(library) {
		t.Fatalf("library values = %#v", match.Values.Libraries)
	}
	if match.Values.Immutables["7"] != "0xaabb" {
		t.Fatalf("immutable values = %#v", match.Values.Immutables)
	}
	if match.Values.ConstructorArguments != "0x"+hex.EncodeToString(argument) {
		t.Fatalf("constructor = %q", match.Values.ConstructorArguments)
	}
	if len(match.Transformations) != 4 {
		t.Fatalf("transformations = %#v", match.Transformations)
	}

	deployed[70] = 0xcc
	if _, err := matchTransformedCode(deployed, compiled, codeTransformationInput{
		ImmutableReferences: map[string][]bytecodeRange{
			"7": {{Start: 60, Length: 2}, {Start: 70, Length: 2}},
		},
		Creation: true,
	}); err == nil {
		t.Fatal("expected repeated immutable conflict")
	}
}

func TestTransformedCodeClassifiesAuxdataReplacementAsPartial(t *testing.T) {
	t.Parallel()
	compiledAux := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 30}})
	deployedAux := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 31}})
	compiled := append([]byte{0x60, 0x00}, compiledAux...)
	deployed := append([]byte{0x60, 0x00}, deployedAux...)
	match, err := matchTransformedCode(deployed, compiled, codeTransformationInput{
		Auxdata: map[string]AuxdataValue{
			"1": {Offset: 2, Value: "0x" + hex.EncodeToString(compiledAux)},
		},
	})
	if err != nil || match == nil {
		t.Fatalf("match=%#v err=%v", match, err)
	}
	if match.MatchType != VerificationMatchPartial ||
		len(match.Transformations) != 1 ||
		match.Transformations[0].Reason != "cborAuxdata" {
		t.Fatalf("unexpected partial match: %#v", match)
	}
}

func TestTransformedCodeRejectsOverlappingRangesAndABIJunk(t *testing.T) {
	t.Parallel()
	if _, err := matchTransformedCode(make([]byte, 40), make([]byte, 40), codeTransformationInput{
		LinkReferences: map[string]map[string][]bytecodeRange{
			"a": {"L": {{Start: 0, Length: 20}}},
		},
		ImmutableReferences: map[string][]bytecodeRange{
			"1": {{Start: 10, Length: 4}},
		},
	}); err == nil {
		t.Fatal("expected overlapping range rejection")
	}
	compiled := []byte{0x60, 0x00}
	deployed := append(append([]byte(nil), compiled...), make([]byte, 33)...)
	match, err := matchTransformedCode(deployed, compiled, codeTransformationInput{
		ABI: json.RawMessage(`[
			{"type":"constructor","inputs":[{"name":"value","type":"uint256"}]}
		]`),
		Creation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if match != nil {
		t.Fatal("non-canonical constructor tail must not match")
	}
}

func TestParseBlueprint(t *testing.T) {
	t.Parallel()
	blueprint, ok, err := ParseBlueprint([]byte{0xfe, 0x71, 0x01, 0x02, 0xaa, 0xbb, 0x60, 0x00})
	if err != nil || !ok {
		t.Fatalf("blueprint=%#v ok=%v err=%v", blueprint, ok, err)
	}
	if blueprint.Version != 0 || !reflect.DeepEqual(blueprint.Data, []byte{0xaa, 0xbb}) ||
		!reflect.DeepEqual(blueprint.Initcode, []byte{0x60, 0x00}) {
		t.Fatalf("unexpected blueprint: %#v", blueprint)
	}
	if _, ok, err := ParseBlueprint([]byte{0x60, 0x00}); err != nil || ok {
		t.Fatalf("regular bytecode ok=%v err=%v", ok, err)
	}
	for _, malformed := range [][]byte{
		{0xfe, 0x71},
		{0xfe, 0x71, 0x03},
		{0xfe, 0x71, 0x01},
		{0xfe, 0x71, 0x00},
	} {
		if _, ok, err := ParseBlueprint(malformed); err == nil || !ok {
			t.Fatalf("expected malformed blueprint rejection for %x", malformed)
		}
	}
}

func TestCandidateOrderingIsDeterministic(t *testing.T) {
	t.Parallel()
	full := &VerificationMatchDetails{MatchType: VerificationMatchFull}
	partial := &VerificationMatchDetails{MatchType: VerificationMatchPartial}
	candidates := []RankedCandidate{
		{FullyQualifiedName: "z.sol:Z", Creation: partial, Runtime: partial},
		{FullyQualifiedName: "b.sol:B", Creation: full, Runtime: partial},
		{FullyQualifiedName: "a.sol:A", Creation: full, Runtime: partial, Hint: true},
		{FullyQualifiedName: "c.sol:C", Runtime: full},
		{FullyQualifiedName: "d.sol:D", Creation: full, Runtime: full},
		{FullyQualifiedName: "creation-only", Creation: full},
	}
	got := SortCandidates(candidates, true)
	names := make([]string, len(got))
	for index := range got {
		names[index] = got[index].FullyQualifiedName
	}
	want := []string{"d.sol:D", "a.sol:A", "b.sol:B", "c.sol:C", "z.sol:Z"}
	if !reflect.DeepEqual(gotNames(names), want) {
		t.Fatalf("order=%v want=%v", names, want)
	}
}

func solidityAuxdata(t *testing.T, value map[string]any) []byte {
	t.Helper()
	payload, err := cbor.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result := append([]byte(nil), payload...)
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(payload)))
	return append(result, length[:]...)
}

func gotNames(values []string) []string { return values }
