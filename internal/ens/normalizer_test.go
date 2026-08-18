package ens

import (
	"context"
	"errors"
	"math/big"
	"testing"
)

func TestENSIP15NormalizerAndEncoding(t *testing.T) {
	normalizer, err := NewNormalizer()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: " NaMe.EtH ", want: "name.eth"},
		{input: "RaFFY🚴‍♂️.eTh", want: "raffy🚴‍♂.eth"},
		{input: "_ETH.EtH", want: "_eth.eth"},
	} {
		got, normalizeErr := normalizer.Normalize(t.Context(), test.input)
		if normalizeErr != nil || got != test.want {
			t.Fatalf("Normalize(%q) = %q, %v; want %q", test.input, got, normalizeErr, test.want)
		}
	}
	for _, input := range []string{"", "foo..eth", "nı̇ck.eth", "a/b.eth", string([]byte{0xff})} {
		if _, err := normalizer.Normalize(t.Context(), input); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Normalize(%q) error = %v", input, err)
		}
	}
	wire, err := DNSWireFormat("my.name.eth")
	if err != nil {
		t.Fatal(err)
	}
	wantWire := []byte{2, 'm', 'y', 4, 'n', 'a', 'm', 'e', 3, 'e', 't', 'h', 0}
	if string(wire) != string(wantWire) {
		t.Fatalf("DNSWireFormat = %x, want %x", wire, wantWire)
	}
	hash, err := Namehash("ens.eth")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hash.Hex(), "0x4e34d3a81dc3a20f71bbdf2160492ddaa17ee7e5523757d47153379c13cb46df"; got != want {
		t.Fatalf("Namehash = %s, want %s", got, want)
	}
}

func TestNormalizerHonorsCancellation(t *testing.T) {
	normalizer, err := NewNormalizer()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := normalizer.Normalize(ctx, "name.eth"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled normalize error = %v", err)
	}
}

func TestEVMCoinType(t *testing.T) {
	for _, test := range []struct {
		chain uint64
		want  *big.Int
		ok    bool
	}{
		{chain: 1, want: big.NewInt(60), ok: true},
		{chain: 8453, want: new(big.Int).SetUint64(0x80002105), ok: true},
		{chain: 1 << 31, want: new(big.Int).SetUint64(1 << 31), ok: true},
	} {
		got, ok := EVMCoinType(test.chain)
		if ok != test.ok || ok && got.Cmp(test.want) != 0 {
			t.Fatalf("EVMCoinType(%d) = %v, %t; want %v, %t", test.chain, got, ok, test.want, test.ok)
		}
	}
}
