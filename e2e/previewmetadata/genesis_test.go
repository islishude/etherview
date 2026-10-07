package previewmetadatae2e

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/core"
)

// The custom Preview genesis must include the system contracts installed by
// the pinned Geth developer genesis; enabling forks alone cannot deploy them.
func TestPreviewGenesisSystemContracts(t *testing.T) {
	data, err := os.ReadFile("../../deploy/preview.genesis.json")
	if err != nil {
		t.Fatal(err)
	}
	var genesis core.Genesis
	if err := json.Unmarshal(data, &genesis); err != nil {
		t.Fatal(err)
	}
	for address, want := range core.SystemContractAllocs() {
		got, ok := genesis.Alloc[address]
		if !ok {
			t.Errorf("missing system contract %s", address)
			continue
		}
		if !bytes.Equal(got.Code, want.Code) || got.Nonce != want.Nonce || got.Balance == nil || got.Balance.Cmp(want.Balance) != 0 {
			t.Errorf("system contract %s differs from pinned Geth developer allocation", address)
		}
	}
}
