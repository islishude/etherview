package enrich

import (
	"encoding/binary"
	"testing"
)

func TestConstructorArgumentsRejectExcessiveDecodedBytes(t *testing.T) {
	const length = (1 << 20) + 1
	arguments := make([]byte, 64+(length+31)/32*32)
	binary.BigEndian.PutUint64(arguments[24:32], 32)
	binary.BigEndian.PutUint64(arguments[56:64], length)
	document := []byte(`[{"type":"constructor","inputs":[{"name":"value","type":"bytes"}]}]`)
	if err := validateConstructorArguments(document, arguments); err == nil {
		t.Fatal("constructor projection accepted a value beyond the verifier decoding budget")
	}
}
