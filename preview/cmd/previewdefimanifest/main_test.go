package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/islishude/etherview/internal/defi"
)

func TestPreviewManifestBindsFinalGenesis(t *testing.T) {
	g, err := os.ReadFile("../../genesis.json")
	if err != nil {
		t.Fatal(err)
	}
	i, err := os.ReadFile("../../contracts/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	first, err := build(g, i)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(first)
	if _, err = defi.ReadManifest(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		next, e := build(g, i)
		if e != nil || next.Digest() != first.Digest() {
			t.Fatal("nondeterministic manifest", e)
		}
	}
	var genesis core.Genesis
	if err = json.Unmarshal(g, &genesis); err != nil {
		t.Fatal(err)
	}
	genesis.Timestamp++
	changed, _ := json.Marshal(genesis)
	next, err := build(changed, i)
	if err != nil {
		t.Fatal(err)
	}
	if next.Genesis == first.Genesis || next.Digest() == first.Digest() {
		t.Fatal("final genesis ignored")
	}
	for _, d := range next.Deployments {
		if d.Start != 0 || d.StartHash != next.Genesis {
			t.Fatal("wrong deployment anchor")
		}
	}
	for _, name := range []string{"chain", "code", "receipt", "pool", "missing", "role"} {
		t.Run(name, func(t *testing.T) {
			var inv inventory
			if err := json.Unmarshal(i, &inv); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "chain":
				inv.ChainID++
			case "code", "receipt", "pool":
				key := map[string]string{"code": "PreviewDeFiV2Factory", "receipt": "PreviewDeFiDebt", "pool": "PreviewDeFiCurveLP"}[name]
				c := inv.Contracts[key]
				c.CodeHash = common.HexToHash("0x1234")
				inv.Contracts[key] = c
			case "missing":
				inv.Deployments = inv.Deployments[:5]
			case "role":
				delete(inv.Deployments[0].Contracts, "factory")
			}
			bad, _ := json.Marshal(inv)
			if _, err := build(g, bad); err == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
}
