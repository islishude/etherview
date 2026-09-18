package ens

import (
	"context"
	"errors"
	"math/big"
	"strings"
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

func TestNamehashENSIP15Unicode(t *testing.T) {
	normalizer, err := NewNormalizer()
	if err != nil {
		t.Fatal(err)
	}
	// Fixed vectors cross-checked with the SPA's independent Viem implementation.
	for _, test := range []struct {
		input      string
		normalized string
		hash       string
	}{
		{"RaFFY🚴‍♂️.eTh", "raffy🚴‍♂.eth", "0x032e9ae610699ada5784570823091a972d06b003c9070bb7732f3ee793d29e05"},
		{"۰۱۲۳۷۸۹.eth", "٠١٢٣٧٨٩.eth", "0xff4b07401acb2d0f048c20f84a6ab57f6d66a0a16d81d13bbba87e8cfacd6594"},
	} {
		t.Run(test.input, func(t *testing.T) {
			name, err := normalizer.Normalize(t.Context(), test.input)
			if err != nil || name != test.normalized {
				t.Fatalf("Normalize = %q, %v; want %q", name, err, test.normalized)
			}
			hash, err := Namehash(name)
			if err != nil || hash.Hex() != test.hash {
				t.Fatalf("Namehash = %s, %v; want %s", hash.Hex(), err, test.hash)
			}
			label := strings.TrimSuffix(test.normalized, ".eth")
			wantWire := append([]byte{byte(len(label))}, []byte(label)...)
			wantWire = append(wantWire, 3, 'e', 't', 'h', 0)
			wire, err := DNSWireFormat(name)
			if err != nil || string(wire) != string(wantWire) {
				t.Fatalf("DNSWireFormat = %x, %v; want %x", wire, err, wantWire)
			}
		})
	}
}

func TestNamehashRejectsENSIP15Violations(t *testing.T) {
	for _, input := range []string{"te--st.eth", "a_b.eth", "nı̇ck.eth", "a/b.eth"} {
		if hash, err := Namehash(input); !errors.Is(err, ErrInvalidName) || hash != ([32]byte{}) {
			t.Fatalf("Namehash(%q) = %x, %v; want zero hash and ErrInvalidName", input, hash, err)
		}
	}
}

func TestNameEncodingBounds(t *testing.T) {
	normalizer, err := NewNormalizer()
	if err != nil {
		t.Fatal(err)
	}
	// 249 label bytes plus the .eth suffix consume exactly 255 wire bytes.
	limit := strings.Repeat("a", 249) + ".eth"
	if name, err := normalizer.Normalize(t.Context(), limit); err != nil || name != limit {
		t.Fatalf("Normalize at limit = %q, %v", name, err)
	}
	if wire, err := DNSWireFormat(limit); err != nil || len(wire) != 255 || wire[len(wire)-1] != 0 {
		t.Fatalf("DNSWireFormat at limit = %x, %v", wire, err)
	}
	if _, err := Namehash(limit); err != nil {
		t.Fatalf("Namehash at limit: %v", err)
	}
	for _, input := range []string{
		"", ".eth", "foo.eth.", "foo..eth", string([]byte{0xff}),
		strings.Repeat("a", 250) + ".eth", strings.Repeat("a", 256), strings.Repeat("a", 1025),
		strings.Repeat("é", 125) + ".eth",
	} {
		if _, err := normalizer.Normalize(t.Context(), input); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Normalize(%q) error = %v", input, err)
		}
		if wire, err := DNSWireFormat(input); !errors.Is(err, ErrInvalidName) || wire != nil {
			t.Fatalf("DNSWireFormat(%q) = %x, %v", input, wire, err)
		}
		if hash, err := Namehash(input); !errors.Is(err, ErrInvalidName) || hash != ([32]byte{}) {
			t.Fatalf("Namehash(%q) = %x, %v", input, hash, err)
		}
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
