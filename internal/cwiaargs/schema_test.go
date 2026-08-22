package cwiaargs

import (
	"bytes"
	"math/big"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

func TestDecodeDynamicSoladyLegacyCWIAArguments(t *testing.T) {
	t.Parallel()
	schema := finalizedSchema(t, []Field{
		{Name: "owner", Type: "address", Offset: 0, Role: FieldRoleValue, Getters: []string{"owner()"}, Size: FixedSize(20)},
		{Name: "number", Type: "uint256", Offset: 20, Role: FieldRoleValue, Getters: []string{"number()"}, Size: FixedSize(32)},
		{Name: "data_length", Type: "uint16", Offset: 52, Role: FieldRoleLength, Getters: []string{"data()"}, Size: FixedSize(2)},
		{Name: "data", Type: "bytes", Offset: 54, Role: FieldRoleValue, Getters: []string{"data()"}, Size: FieldSize("data_length", 1)},
	})
	analysis, err := DerivedAnalysis(schema)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalAnalysis(analysis)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAnalysis(encoded)
	if err != nil {
		t.Fatal(err)
	}
	owner := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	number := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(42))
	payload := []byte("hello,world")
	raw := append(owner.Bytes(), number.FillBytes(make([]byte, 32))...)
	raw = append(raw, 0, byte(len(payload)))
	raw = append(raw, payload...)
	decoded := Decode(hexutil.Encode(raw), parsed, ResolutionCodeHash)
	if decoded.Status != StatusDecoded || decoded.Reason != "" || decoded.Schema == nil ||
		decoded.SchemaResolution != ResolutionCodeHash || len(decoded.Arguments) != 4 {
		t.Fatalf("decoded=%+v", decoded)
	}
	want := []any{owner.Hex(), number.String(), "11", hexutil.Encode(payload)}
	for index, argument := range decoded.Arguments {
		if argument.Value != want[index] {
			t.Fatalf("argument %d=%+v want=%v", index, argument, want[index])
		}
	}
}

func TestDecodeCanonicalArrayAndRemainingHelpers(t *testing.T) {
	t.Parallel()
	arraySchema := finalizedSchema(t, []Field{
		{Name: "items_count", Type: "uint8", Offset: 0, Role: FieldRoleLength, Size: FixedSize(1)},
		{Name: "items", Type: "uint256[]", Offset: 1, Role: FieldRoleValue, Getters: []string{"items()"}, Size: FieldSize("items_count", 32)},
	})
	arrayAnalysis, _ := DerivedAnalysis(arraySchema)
	raw := append([]byte{2}, make([]byte, 64)...)
	raw[32], raw[64] = 1, 2
	decoded := Decode(hexutil.Encode(raw), arrayAnalysis, ResolutionExactAddress)
	values, ok := decoded.Arguments[1].Value.([]string)
	if decoded.Status != StatusDecoded || !ok || len(values) != 2 || values[0] != "1" || values[1] != "2" {
		t.Fatalf("array decoded=%+v", decoded)
	}

	remainingSchema := finalizedSchema(t, []Field{{
		Name: "rawArgs", Type: "bytes", Offset: 0, Role: FieldRoleValue,
		Getters: []string{"rawArgs()"}, Size: RemainingSize(),
	}})
	remainingAnalysis, _ := DerivedAnalysis(remainingSchema)
	remaining := Decode("0x", remainingAnalysis, ResolutionCodeHash)
	if remaining.Status != StatusDecoded || len(remaining.Arguments) != 1 ||
		remaining.Arguments[0].Length != 0 || remaining.Arguments[0].Value != "0x" {
		t.Fatalf("remaining=%+v", remaining)
	}
}

func TestDecodeEveryCanonicalUintWidthAndBytes32Array(t *testing.T) {
	t.Parallel()
	for bits := 8; bits <= 256; bits += 8 {
		valueType := "uint" + strconv.Itoa(bits)
		width := bits / 8
		schema := finalizedSchema(t, []Field{{
			Name: "value", Type: valueType, Offset: 0, Role: FieldRoleValue,
			Size: FixedSize(width),
		}})
		analysis, _ := DerivedAnalysis(schema)
		raw := bytes.Repeat([]byte{0xff}, width)
		decoded := Decode(hexutil.Encode(raw), analysis, ResolutionExactAddress)
		want := new(big.Int).SetBytes(raw).String()
		if decoded.Status != StatusDecoded || decoded.Arguments[0].Value != want {
			t.Fatalf("%s decoded=%+v want=%s", valueType, decoded, want)
		}
	}

	schema := finalizedSchema(t, []Field{
		{Name: "hashes_count", Type: "uint8", Offset: 0, Role: FieldRoleLength, Size: FixedSize(1)},
		{Name: "hashes", Type: "bytes32[]", Offset: 1, Role: FieldRoleValue, Size: FieldSize("hashes_count", 32)},
	})
	analysis, _ := DerivedAnalysis(schema)
	raw := append([]byte{1}, bytes.Repeat([]byte{0xab}, 32)...)
	decoded := Decode(hexutil.Encode(raw), analysis, ResolutionCodeHash)
	values, ok := decoded.Arguments[1].Value.([]string)
	if decoded.Status != StatusDecoded || !ok || len(values) != 1 || values[0] != hexutil.Encode(raw[1:]) {
		t.Fatalf("bytes32 array=%+v", decoded)
	}
}

func TestDecodeCWIAArgumentsFailsClosed(t *testing.T) {
	t.Parallel()
	schema := finalizedSchema(t, []Field{{
		Name: "owner", Type: "address", Offset: 0, Role: FieldRoleValue, Size: FixedSize(20),
	}})
	analysis, _ := DerivedAnalysis(schema)
	for name, test := range map[string]struct {
		raw    string
		status string
		reason string
	}{
		"short": {raw: "0x01", status: StatusDataInvalid, reason: ReasonLengthMismatch},
		"long":  {raw: "0x" + string(bytes.Repeat([]byte{'0'}, 42)), status: StatusDataInvalid, reason: ReasonLengthMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			decoded := Decode(test.raw, analysis, ResolutionExactAddress)
			if decoded.Status != test.status || decoded.Reason != test.reason || len(decoded.Arguments) != 0 {
				t.Fatalf("decoded=%+v", decoded)
			}
		})
	}
	unavailable := Decode("0x", UnavailableAnalysis(), "")
	if unavailable.Status != StatusSchemaUnavailable || unavailable.Reason != ReasonASTUnavailable {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}

func finalizedSchema(t *testing.T, fields []Field) Schema {
	t.Helper()
	schema, err := FinalizeSchema(fields)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func FuzzParseAnalysisNeverPanics(f *testing.F) {
	schema, err := FinalizeSchema([]Field{{
		Name: "owner", Type: "address", Offset: 0, Role: FieldRoleValue, Size: FixedSize(20),
	}})
	if err != nil {
		f.Fatal(err)
	}
	analysis, err := DerivedAnalysis(schema)
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := MarshalAnalysis(analysis)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{"analysis_version":1,"status":"invalid","reason":"unsupported_access"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(_ *testing.T, raw []byte) {
		_, _ = ParseAnalysis(raw)
	})
}
