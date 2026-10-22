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
