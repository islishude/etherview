// previewensmanifest binds the reviewed Preview contract inventory to the exact
// generated genesis without changing its allocation or pretending it emitted logs.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enssync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	genesisPath := flag.String("genesis", "", "generated execution genesis")
	inventoryPath := flag.String("inventory", "preview/contracts/manifest.json", "reviewed contracts")
	output := flag.String("output", "", "output manifest")
	flag.Parse()
	raw, e := os.ReadFile(*genesisPath)
	if e != nil {
		return e
	}
	var genesis core.Genesis
	if e = json.Unmarshal(raw, &genesis); e != nil {
		return e
	}
	raw, e = os.ReadFile(*inventoryPath)
	if e != nil {
		return e
	}
	var inventory struct {
		ChainID   uint64 `json:"chainId"`
		CoinType  string `json:"coinType"`
		TLD       string `json:"tld"`
		Contracts map[string]struct {
			Address  common.Address `json:"address"`
			CodeHash common.Hash    `json:"codeHash"`
		} `json:"contracts"`
	}
	if e = json.Unmarshal(raw, &inventory); e != nil {
		return e
	}
	if genesis.Config == nil || genesis.Config.ChainID == nil || genesis.Config.ChainID.String() != strconv.FormatUint(inventory.ChainID, 10) {
		return fmt.Errorf("preview chain identity mismatch")
	}
	hash := genesis.ToBlock().Hash()
	m := enssync.Manifest{TargetChainID: genesis.Config.ChainID.String(), SourceChainID: genesis.Config.ChainID.String(), Genesis: hash, StartHash: hash, ParentName: inventory.TLD, CoinType: inventory.CoinType, Policy: crypto.Keccak256Hash([]byte("etherview-preview-ens-policy-v1")), Registry: inventory.Contracts["ENSRegistry"].Address, UniversalResolver: inventory.Contracts["UniversalResolver"].Address}
	for _, entry := range []struct{ name, profile string }{{"ENSRegistry", "registry"}, {"BaseRegistrarImplementation", "registrar"}, {"PreviewRegistrarController", "controller"}, {"ReverseRegistrar", "reverse"}, {"DefaultReverseRegistrar", "default_reverse"}, {"PublicResolver", "resolver"}, {"UniversalResolver", "universal"}} {
		contract, ok := inventory.Contracts[entry.name]
		if !ok || crypto.Keccak256Hash(genesis.Alloc[contract.Address].Code) != contract.CodeHash {
			return fmt.Errorf("preview deployment evidence mismatch: %s", entry.name)
		}
		m.Deployments = append(m.Deployments, enssync.Deployment{Address: contract.Address, Profile: entry.profile, Hash: hash, CodeHash: contract.CodeHash})
	}
	raw, e = json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if _, e = enssync.ReadManifest(bytes.NewReader(raw)); e != nil {
		return e
	}
	return os.WriteFile(*output, append(raw, '\n'), 0644)
}
