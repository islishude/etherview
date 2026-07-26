package query

import (
	"bytes"
	"errors"
	"testing"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestGenesisCursorBindsChainBlockAndAddress(t *testing.T) {
	t.Parallel()
	blockHash := bytes.Repeat([]byte{0x11}, 32)
	if _, after, err := decodeGenesisCursor("", "777", blockHash); err != nil || after == nil || len(after) != 0 {
		t.Fatalf("empty cursor after=%v err=%v", after, err)
	}
	cursor, err := httpapi.EncodeCursor(genesisCursor{
		ChainID:   "777",
		BlockHash: "0x1111111111111111111111111111111111111111111111111111111111111111",
		After:     "0x2000000000000000000000000000000000000002",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := decodeGenesisCursor(cursor, "777", blockHash)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(after); got != 20 || after[0] != 0x20 || after[19] != 0x02 {
		t.Fatalf("decoded address = %x", after)
	}
	if _, _, err := decodeGenesisCursor(cursor, "778", blockHash); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("wrong-chain cursor error = %v", err)
	}
	blockHash[0] = 0x22
	if _, _, err := decodeGenesisCursor(cursor, "777", blockHash); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("wrong-block cursor error = %v", err)
	}
}

func TestGenesisAccountModelKeepsStringQuantitiesAndKind(t *testing.T) {
	t.Parallel()
	row := dbgen.ListGenesisAccountsRow{
		Address: bytes.Repeat([]byte{0x22}, 20), Balance: "1000000000000000000", Nonce: "3",
		CodeHash: bytes.Repeat([]byte{0x33}, 32), StorageRoot: bytes.Repeat([]byte{0x44}, 32),
		BlockHash: bytes.Repeat([]byte{0x55}, 32), Contract: true,
	}
	model, err := genesisAccountModel(row)
	if err != nil {
		t.Fatal(err)
	}
	if model.Balance != row.Balance || model.Nonce != row.Nonce ||
		model.Type != "contract" || model.Address != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("model=%+v", model)
	}
	row.Balance = "01"
	if _, err := genesisAccountModel(row); err == nil {
		t.Fatal("accepted non-canonical balance")
	}
}
