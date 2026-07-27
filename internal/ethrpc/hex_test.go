package ethrpc

import (
	"errors"
	"math"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

func TestParseQuantityReturnsStandardBigIntAndRejectsNonCanonicalForms(t *testing.T) {
	t.Parallel()
	const maximum = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	value, err := ParseQuantity(maximum)
	if err != nil {
		t.Fatal(err)
	}
	if value.BitLen() != 256 {
		t.Fatalf("bit length = %d", value.BitLen())
	}
	for _, input := range []string{"0x", "0x00", "0X1", "0xA", "1"} {
		if _, err := ParseQuantity(input); !errors.Is(err, ErrInvalidQuantity) {
			t.Errorf("ParseQuantity(%q) error = %v", input, err)
		}
	}
	if value := QuantityFromUint64(math.MaxUint64); value != hexutil.Uint64(math.MaxUint64) {
		t.Fatalf("QuantityFromUint64() = %v", value)
	}
}

func TestParseDataHashAndAddressReturnGethTypes(t *testing.T) {
	t.Parallel()
	data, err := ParseData("0xABcd")
	if err != nil {
		t.Fatal(err)
	}
	if data.String() != "0xabcd" {
		t.Fatalf("data = %s", data.String())
	}
	hash, err := ParseHash("0x" + repeat("00", common.HashLength))
	if err != nil {
		t.Fatal(err)
	}
	if hash != (common.Hash{}) {
		t.Fatalf("hash = %s", hash)
	}
	address, err := ParseAddress("0x" + repeat("00", common.AddressLength))
	if err != nil {
		t.Fatal(err)
	}
	if address != (common.Address{}) {
		t.Fatalf("address = %s", address)
	}
	for _, input := range []string{"0X00", "0x0", "0xzz"} {
		if _, err := ParseData(input); !errors.Is(err, ErrInvalidData) {
			t.Errorf("ParseData(%q) error = %v", input, err)
		}
	}
}

func repeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
