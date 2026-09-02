package erc4337

import (
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
)

func TestRegistryDigestIsOrderIndependentAndRangeBound(t *testing.T) {
	t.Parallel()
	end := uint64(12)
	left, err := NewRegistry(config.ERC4337Config{EntryPoints: []config.ERC4337EntryPointConfig{
		{Address: "0x433709009B8330FDa32311DF1C2AFA402eD8D009", Version: "0.9", FromBlock: 13},
		{Address: "0x0000000071727De22E5E9d8BAf0edAc6f37da032", Version: "0.7", FromBlock: 4, ToBlock: &end},
	}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRegistry(config.ERC4337Config{EntryPoints: []config.ERC4337EntryPointConfig{
		{Address: "0x0000000071727de22e5e9d8baf0edac6f37da032", Version: "0.7", FromBlock: 4, ToBlock: &end},
		{Address: "0x433709009b8330fda32311df1c2afa402ed8d009", Version: "0.9", FromBlock: 13},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if left.Digest() != right.Digest() {
		t.Fatal("normalized registries have different digests")
	}
	entry, ok := left.Match(common.HexToAddress("0x0000000071727De22E5E9d8BAf0edAc6f37da032"), 12)
	if !ok || entry.Version != Version07 {
		t.Fatalf("match = %#v, %v", entry, ok)
	}
	if _, ok := left.Match(entry.Address, 13); ok {
		t.Fatal("expired EntryPoint range matched")
	}
	if left.StartBlock(6) != 6 {
		t.Fatalf("start = %d", left.StartBlock(6))
	}
}

func TestRegistryRejectsUnboundedEntryPointLists(t *testing.T) {
	t.Parallel()
	entries := make([]config.ERC4337EntryPointConfig, maximumEntryPoints+1)
	for index := range entries {
		entries[index] = config.ERC4337EntryPointConfig{
			Address: fmt.Sprintf("0x%040x", index+1), Version: "0.9",
		}
	}
	if _, err := NewRegistry(config.ERC4337Config{EntryPoints: entries}); err == nil {
		t.Fatal("oversized EntryPoint registry was accepted")
	}
}
