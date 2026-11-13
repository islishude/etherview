// previewdefimanifest authenticates local demonstration deployments against the
// final execution genesis. It never invents genesis Transfer evidence.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/defi"
	"github.com/islishude/etherview/internal/deficontract"
)

type inventory struct {
	ChainID   uint64 `json:"chainId"`
	Contracts map[string]struct {
		Address  common.Address `json:"address"`
		CodeHash common.Hash    `json:"codeHash"`
	} `json:"contracts"`
	Deployments []struct {
		Protocol  deficontract.Protocol `json:"protocol"`
		Contracts map[string]string     `json:"contracts"`
		Receipts  []string              `json:"receipts"`
		Pools     []string              `json:"pools"`
	} `json:"defiDeployments"`
}

func build(genesisRaw, inventoryRaw []byte) (defi.Manifest, error) {
	var g core.Genesis
	var inv inventory
	var m defi.Manifest
	bad := errors.New("preview DeFi deployment identity mismatch")
	if json.Unmarshal(genesisRaw, &g) != nil || json.Unmarshal(inventoryRaw, &inv) != nil || g.Config == nil || g.Config.ChainID == nil || !g.Config.ChainID.IsUint64() || g.Config.ChainID.Uint64() != inv.ChainID {
		return m, bad
	}
	m = defi.Manifest{Version: 1, Revision: 1, ChainID: g.Config.ChainID.String(), Genesis: g.ToBlock().Hash()}
	contract := func(name, role string) (defi.Contract, error) {
		c, ok := inv.Contracts[name]
		if !ok || len(g.Alloc[c.Address].Code) == 0 || crypto.Keccak256Hash(g.Alloc[c.Address].Code) != c.CodeHash {
			return defi.Contract{}, bad
		}
		return defi.Contract{Role: role, Address: c.Address, CodeHash: c.CodeHash}, nil
	}
	seen := map[deficontract.Protocol]bool{}
	for _, source := range inv.Deployments {
		if seen[source.Protocol] {
			return m, bad
		}
		seen[source.Protocol] = true
		d := defi.Deployment{ID: "preview-" + string(source.Protocol), Protocol: source.Protocol, StartHash: m.Genesis}
		// Stable role ordering makes the digest independent of Go map iteration.
		for _, role := range []string{"factory", "position_manager", "pool_manager", "state_view", "pool", "data_provider", "metaregistry", "steth", "wsteth", "withdrawal_queue"} {
			if name, ok := source.Contracts[role]; ok {
				c, e := contract(name, role)
				if e != nil {
					return m, e
				}
				d.Contracts = append(d.Contracts, c)
			}
		}
		if len(d.Contracts) != len(source.Contracts) {
			return m, bad
		}
		for _, name := range source.Receipts {
			c, e := contract(name, "receipt")
			if e != nil {
				return m, e
			}
			d.ReceiptContracts = append(d.ReceiptContracts, c)
		}
		for _, name := range source.Pools {
			c, e := contract(name, "pool")
			if e != nil {
				return m, e
			}
			d.PoolCodeHashes = append(d.PoolCodeHashes, c.CodeHash)
		}
		m.Deployments = append(m.Deployments, d)
	}
	if len(seen) != 6 {
		return m, bad
	}
	return m, m.Validate()
}
func run() error {
	genesis := flag.String("genesis", "", "final execution genesis")
	inventory := flag.String("inventory", "preview/contracts/manifest.json", "reviewed Preview inventory")
	output := flag.String("output", "", "output manifest")
	flag.Parse()
	g, e := os.ReadFile(*genesis)
	if e != nil {
		return e
	}
	i, e := os.ReadFile(*inventory)
	if e != nil {
		return e
	}
	m, e := build(g, i)
	if e != nil {
		return e
	}
	raw, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(*output, append(raw, '\n'), 0644)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
