package abicalldata

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/islishude/etherview/internal/abibounds"
)

func TestValidateConstructor(t *testing.T) {
	word := make([]byte, 32)
	word[31] = 2
	for _, test := range []struct {
		name, document string
		arguments      []byte
		want           error
	}{
		{name: "absent constructor", document: `[]`},
		{name: "empty constructor", document: `[{"type":"constructor","inputs":[]}]`},
		{name: "canonical integer", document: `[{"type":"constructor","inputs":[{"type":"uint256"}]}]`, arguments: word},
		{name: "invalid ABI", document: `{}`, want: ErrInvalidABI},
		{name: "missing constructor", document: `[]`, arguments: word, want: ErrMissingConstructor},
		{name: "empty constructor with payload", document: `[{"type":"constructor","inputs":[]}]`, arguments: word, want: ErrNonCanonical},
		{name: "truncated integer", document: `[{"type":"constructor","inputs":[{"type":"uint256"}]}]`, arguments: word[:31], want: ErrArgumentLimit},
		{name: "invalid bool", document: `[{"type":"constructor","inputs":[{"type":"bool"}]}]`, arguments: word, want: ErrInvalidArguments},
		{name: "trailing word", document: `[{"type":"constructor","inputs":[{"type":"uint256"}]}]`, arguments: append(append([]byte{}, word...), word...), want: ErrNonCanonical},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateConstructor([]byte(test.document), test.arguments); !errors.Is(err, test.want) {
				t.Fatalf("validation error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestConstructorAndFunctionRetainSeparateByteBudgets(t *testing.T) {
	entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"bytes"}]}`)
	method := testFunction(t, entry)
	for _, test := range []struct {
		name   string
		length int
		match  bool
	}{
		{"above constructor budget", (1 << 20) + 1, true},
		{"function byte boundary", 4 << 20, true},
		{"above function byte budget", (4 << 20) + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, err := method.Inputs.Pack(make([]byte, test.length))
			if err != nil {
				t.Fatal(err)
			}
			calldata := append(append([]byte{}, method.ID...), payload...)
			signature, match := DecodeVerifiedFunction(entry, calldata)
			if match != test.match || match && signature != "f(bytes)" {
				t.Fatalf("function match = %q, %t, want %t", signature, match, test.match)
			}
			constructor := []byte(`[{"type":"constructor","inputs":[{"name":"value","type":"bytes"}]}]`)
			if err := ValidateConstructor(constructor, payload); !errors.Is(err, ErrArgumentLimit) {
				t.Fatalf("constructor byte bound error = %v", err)
			}
		})
	}
}

func TestVerifiedFunctionBoundsCanonicalNodeExpansion(t *testing.T) {
	entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"uint256[][]"}]}`)
	method := testFunction(t, entry)
	for _, count := range []int{15, 16} {
		values := make([][]*big.Int, count)
		for index := range values {
			values[index] = make([]*big.Int, 4096)
			for item := range values[index] {
				values[index][item] = big.NewInt(1)
			}
		}
		payload, err := method.Inputs.Pack(values)
		if err != nil {
			t.Fatal(err)
		}
		err = validateCanonicalArguments(method.Inputs, payload, verifiedFunctionLimits())
		if count == 15 && err != nil || count == 16 && !errors.Is(err, ErrArgumentLimit) {
			t.Fatalf("%d arrays of 4096 elements: %v", count, err)
		}
		if _, match := DecodeVerifiedFunction(entry, append(method.ID, payload...)); match != (count == 15) {
			t.Fatalf("%d arrays of 4096 elements matched = %t", count, match)
		}
	}
}

func TestVerifiedFunctionRejectsAliasedExpansionBeforeUnpack(t *testing.T) {
	entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"uint256[][][][][]"}]}`)
	method := testFunction(t, entry)
	payload := abiWord(10)
	for range 10 {
		payload = append(payload, abiWord(1)...)
	}
	for range 4 {
		parent := abiWord(10)
		for range 10 {
			parent = append(parent, abiWord(10*32)...)
		}
		payload = append(parent, payload...)
	}
	payload = append(abiWord(32), payload...)
	if err := validateCanonicalArguments(method.Inputs, payload, verifiedFunctionLimits()); !errors.Is(err, ErrArgumentLimit) {
		t.Fatalf("aliased expansion error = %v, want preflight rejection", err)
	}
}

func TestVerifiedFunctionBoundsTypeDepthAndExpandedArrays(t *testing.T) {
	for _, typ := range []string{"uint256" + strings.Repeat("[]", 16), "uint256[4096][4096]"} {
		entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"` + typ + `"}]}`)
		if _, err := abibounds.ParseEntry(entry, verifiedFunctionLimits()); !errors.Is(err, abibounds.ErrLimit) {
			t.Fatalf("type %s error = %v, want preflight rejection", typ, err)
		}
		if _, match := DecodeVerifiedFunction(entry, make([]byte, 4)); match {
			t.Fatalf("type %s matched", typ)
		}
	}
	entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"uint256` + strings.Repeat("[]", 15) + `"}]}`)
	method := testFunction(t, entry)
	if _, match := DecodeVerifiedFunction(entry, append(append(method.ID, abiWord(32)...), abiWord(0)...)); !match {
		t.Fatal("canonical empty array at the normalized type-depth boundary did not match")
	}
}

func TestVerifiedFunctionBoundsTupleArguments(t *testing.T) {
	for _, count := range []int{256, 257} {
		parameters := make([]string, count)
		for index := range parameters {
			parameters[index] = fmt.Sprintf(`{"name":"p%d","type":"uint256"}`, index)
		}
		for _, nested := range []bool{false, true} {
			inputs := strings.Join(parameters, ",")
			if nested {
				inputs = `{"name":"value","type":"tuple","components":[` + inputs + `]}`
			}
			entry := []byte(`{"type":"function","name":"f","inputs":[` + inputs + `]}`)
			_, err := abibounds.ParseEntry(entry, verifiedFunctionLimits())
			if count == 256 && err != nil || count == 257 && !errors.Is(err, abibounds.ErrLimit) {
				t.Fatalf("%d arguments, nested %t: %v", count, nested, err)
			}
		}
	}
}

func TestVerifiedFunctionRejectsNonCanonicalDynamicPayload(t *testing.T) {
	entry := []byte(`{"type":"function","name":"f","inputs":[{"name":"value","type":"bytes"}]}`)
	method := testFunction(t, entry)
	payload, err := method.Inputs.Pack([]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] = 1
	if err := validateCanonicalArguments(method.Inputs, payload, verifiedFunctionLimits()); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("nonzero padding error = %v", err)
	}
	if _, match := DecodeVerifiedFunction(entry, append(method.ID, payload...)); match {
		t.Fatal("noncanonical padding matched")
	}
}

func testFunction(t *testing.T, entry []byte) abi.Method {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader("[" + string(entry) + "]"))
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Methods["f"]
}

func abiWord(value uint64) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint64(word[24:], value)
	return word
}
