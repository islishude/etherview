package abibounds

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestParseRejectsUnboundedTypes(t *testing.T) {
	for _, typ := range []string{"uint256" + strings.Repeat("[]", 8000), "uint256[999999999999999999999]", "uint256[4096][4096]"} {
		t.Run(typ[:min(40, len(typ))], func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`[{"type":"constructor","inputs":[{"name":"a","type":%q}]}]`, typ))
			if _, err := Parse(raw); err == nil {
				t.Fatal("unbounded type accepted")
			}
		})
	}
	component := `{"name":"a","type":"uint256"}`
	for range 20 {
		component = `{"name":"a","type":"tuple","components":[` + component + `]}`
	}
	if _, err := Parse([]byte(`[{"type":"constructor","inputs":[` + component + `]}]`)); err == nil {
		t.Fatal("deep tuple accepted")
	}
}

func TestCheckValuesAcceptsCanonicalNestedValues(t *testing.T) {
	tests := []struct {
		typ   string
		value any
	}{
		{"uint256", big.NewInt(42)},
		{"uint256[][]", [][]*big.Int{{big.NewInt(1), big.NewInt(2)}, {big.NewInt(3)}}},
		{"string[2]", [2]string{"hello", "world"}},
		{"bytes[]", [][]byte{{1, 2}, {3, 4, 5}}},
		{"uint256[2][]", [][2]*big.Int{{big.NewInt(1), big.NewInt(2)}}},
	}
	for _, test := range tests {
		t.Run(test.typ, func(t *testing.T) {
			parsed, err := Parse([]byte(fmt.Sprintf(`[{"type":"constructor","inputs":[{"name":"a","type":%q}]}]`, test.typ)))
			if err != nil {
				t.Fatal(err)
			}
			data, err := parsed.Constructor.Inputs.Pack(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckValues(parsed.Constructor.Inputs, data); err != nil {
				t.Fatal(err)
			}
			if err := CheckValues(parsed.Constructor.Inputs, data[:len(data)-32]); err == nil {
				t.Fatal("truncated value accepted")
			}
		})
	}
}

func TestCheckValuesBoundsAliasedOffsets(t *testing.T) {
	parsed, err := Parse([]byte(`[{"type":"constructor","inputs":[{"name":"a","type":"uint256[][][][][]"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	args := append(word(32), aliasedArray(5, 10)...)
	if len(args) != 1792 {
		t.Fatal(len(args))
	}
	if err := CheckValues(parsed.Constructor.Inputs, args); err == nil {
		t.Fatal("exponential expansion accepted")
	}
}

func word(n uint64) []byte { b := make([]byte, 32); binary.BigEndian.PutUint64(b[24:], n); return b }
func aliasedArray(depth, width int) []byte {
	b := word(uint64(width))
	for range width {
		if depth == 1 {
			b = append(b, word(1)...)
		} else {
			b = append(b, word(uint64(width*32))...)
		}
	}
	if depth > 1 {
		b = append(b, aliasedArray(depth-1, width)...)
	}
	return b
}

func TestCheckValuesBoundsRepeatedBytePayloads(t *testing.T) {
	parsed, err := Parse([]byte(`[{"type":"constructor","inputs":[{"name":"a","type":"bytes[]"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	data := append(word(32), word(32)...)
	for range 32 {
		data = append(data, word(32*32)...)
	}
	data = append(data, word(65536)...)
	data = append(data, make([]byte, 65536)...)
	if err := CheckValues(parsed.Constructor.Inputs, data); err == nil {
		t.Fatal("byte expansion accepted")
	}
}

func FuzzCheckValues(f *testing.F) {
	parsed, err := Parse([]byte(`[{"type":"constructor","inputs":[{"name":"a","type":"bytes[][]"}]}]`))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(word(32), word(0)...))
	f.Add(append(word(32), aliasedArray(2, 10)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		if CheckValues(parsed.Constructor.Inputs, data) != nil {
			return
		}
		values, err := parsed.Constructor.Inputs.Unpack(data)
		if err == nil {
			if _, err := parsed.Constructor.Inputs.Pack(values...); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestCheckValuesAcceptsNestedTuples(t *testing.T) {
	parsed, err := Parse([]byte(`[{"type":"constructor","inputs":[{"name":"a","type":"tuple[]","components":[{"name":"b","type":"tuple","components":[{"name":"x","type":"uint256[2]"},{"name":"y","type":"string"}]}]}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	type inner struct {
		X [2]*big.Int
		Y string
	}
	type outer struct{ B inner }
	data, err := parsed.Constructor.Inputs.Pack([]outer{{B: inner{X: [2]*big.Int{big.NewInt(1), big.NewInt(2)}, Y: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckValues(parsed.Constructor.Inputs, data); err != nil {
		t.Fatal(err)
	}
}
