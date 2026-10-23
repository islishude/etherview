package verify

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func constructorWord(n uint64) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint64(b[24:], n)
	return b
}
func aliasedConstructorArray(depth, width int) []byte {
	b := constructorWord(uint64(width))
	if depth == 1 {
		for range width {
			b = append(b, constructorWord(1)...)
		}
		return b
	}
	for range width {
		b = append(b, constructorWord(uint64(width*32))...)
	}
	return append(b, aliasedConstructorArray(depth-1, width)...)
}
func TestConstructorRejectsExpansionBeforeUnpack(t *testing.T) {
	raw := json.RawMessage(`[{"type":"constructor","inputs":[{"name":"a","type":"uint256[][][][][]"}]}]`)
	args := append(constructorWord(32), aliasedConstructorArray(5, 10)...)
	err := validateConstructorArguments(raw, args)
	if err == nil || err.Error() != "constructor arguments exceed decoding bounds" {
		t.Fatalf("expected preflight rejection, got %v", err)
	}
}

func TestConstructorValidationPreservesFailureCategories(t *testing.T) {
	for _, test := range []struct {
		name, document string
		arguments      []byte
		message        string
	}{
		{name: "no ABI and no arguments"},
		{name: "missing ABI", arguments: constructorWord(1), message: "constructor arguments exist without an ABI"},
		{name: "invalid ABI", document: `[{"type":"constructor","inputs":[{"type":"invalid"}]}]`, message: "compiled contract ABI is invalid"},
		{name: "missing constructor", document: `[]`, arguments: constructorWord(1), message: "constructor arguments exist without a constructor"},
		{name: "truncated value", document: `[{"type":"constructor","inputs":[{"type":"uint256"}]}]`, message: "constructor arguments exceed decoding bounds"},
		{name: "invalid value", document: `[{"type":"constructor","inputs":[{"type":"bool"}]}]`, arguments: constructorWord(2), message: "constructor arguments do not decode"},
		{name: "noncanonical value", document: `[{"type":"constructor","inputs":[{"type":"address"}]}]`, arguments: append([]byte{1}, make([]byte, 31)...), message: "constructor arguments are not canonically encoded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateConstructorArguments(json.RawMessage(test.document), test.arguments)
			if test.message == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.message {
				t.Fatalf("constructor error = %v, want %s", err, test.message)
			}
		})
	}
}
