package genesis

import (
	"database/sql"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/islishude/etherview/internal/config"
)

const genesisFixture = `{
  "config":{"chainId":777,"homesteadBlock":0,"eip150Block":0,"eip155Block":0,"eip158Block":0,"byzantiumBlock":0,"constantinopleBlock":0,"petersburgBlock":0,"istanbulBlock":0,"berlinBlock":0,"londonBlock":0},
  "nonce":"0x0",
  "timestamp":"0x0",
  "extraData":"0x",
  "gasLimit":"0x1c9c380",
  "difficulty":"0x1",
  "mixHash":"0x0000000000000000000000000000000000000000000000000000000000000000",
  "coinbase":"0x0000000000000000000000000000000000000000",
  "alloc":{
    "1000000000000000000000000000000000000001":{"balance":"0x2a"},
    "2000000000000000000000000000000000000002":{"balance":"1000000000000000000","nonce":"0x3","code":"0x6001600055","storage":{"0x00":"0x07","0x02":"0x09"}}
  }
}`

func TestNewImporterRejectsGenesisFileAboveBlockZero(t *testing.T) {
	_, err := NewImporter(&sql.DB{}, config.ChainConfig{
		ID: 777, StartBlock: 1, GenesisFile: "/tmp/genesis.json",
	}, nil, 0)
	if err == nil || err.Error() != "genesis importer requires indexing from block zero" {
		t.Fatalf("NewImporter error = %v", err)
	}
}

func TestNewImporterRejectsAmbiguousOrNonzeroRemoteSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		chain config.ChainConfig
		want  string
	}{
		{
			name: "file and URL",
			chain: config.ChainConfig{
				ID: 777, GenesisFile: "/tmp/genesis.json",
				GenesisURL: "https://genesis.example/genesis.json",
			},
			want: "genesis importer file and URL are mutually exclusive",
		},
		{
			name: "remote above block zero",
			chain: config.ChainConfig{
				ID: 777, StartBlock: 1,
				GenesisURL:          "https://genesis.example/genesis.json",
				GenesisFetchTimeout: time.Second,
			},
			want: "genesis importer requires indexing from block zero",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewImporter(&sql.DB{}, test.chain, nil, 0)
			if err == nil || err.Error() != test.want {
				t.Fatalf("NewImporter error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseDocumentAuthenticatesAccountsAndStorage(t *testing.T) {
	spec, block, err := parseDocument([]byte(genesisFixture), 777)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Alloc) != 2 {
		t.Fatalf("allocation count = %d", len(spec.Alloc))
	}
	contractAddress := common.HexToAddress("0x2000000000000000000000000000000000000002")
	contract := spec.Alloc[contractAddress]
	root, err := storageRoot(contract.Storage)
	if err != nil {
		t.Fatal(err)
	}
	if root == types.EmptyRootHash {
		t.Fatal("non-empty storage produced the empty root")
	}
	if got, want := block.Root().Hex(), "0x1ed58eaa9fa5ebfe410f6f13d27380e59ba5fbf03bc4f7f6276921721558c102"; got != want {
		t.Fatalf("state root = %s, want %s", got, want)
	}
	if got, want := block.Hash().Hex(), "0x01ea13d00d2698ff2d67208c43b4f0bfd2051a1b5af8566c395831a57b47a414"; got != want {
		t.Fatalf("block hash = %s, want %s", got, want)
	}
	if got, want := root.Hex(), "0xb90c8ee1bb68a060ea6a37fcf4228864e811e165821133a98c0cfa8997c2b651"; got != want {
		t.Fatalf("storage root = %s, want %s", got, want)
	}
}

func TestParseDocumentAuthenticatesAmsterdamGenesisHeader(t *testing.T) {
	document := strings.Replace(
		genesisFixture,
		`"londonBlock":0}`,
		`"londonBlock":0,"shanghaiTime":0,"cancunTime":0,"pragueTime":0,"amsterdamTime":0,`+
			`"blobSchedule":{`+
			`"cancun":{"target":3,"max":6,"baseFeeUpdateFraction":3338477},`+
			`"prague":{"target":6,"max":9,"baseFeeUpdateFraction":5007716},`+
			`"amsterdam":{"target":6,"max":9,"baseFeeUpdateFraction":5007716}}}`,
		1,
	)
	document = strings.Replace(document, `"alloc":{`, `"slotNumber":7,"alloc":{`, 1)
	_, block, err := parseDocument([]byte(document), 777)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := block.Hash().Hex(), "0x9dc01b4e711aba36c6fdfdc248a5f6c3ad36ab401f420888a1676b522e38a4bf"; got != want {
		t.Fatalf("Amsterdam block hash = %s, want %s", got, want)
	}
}

func TestCoreGenesisAccountRootMatchesReferenceVector(t *testing.T) {
	alloc := make(types.GenesisAlloc)
	for index := uint64(1); index <= 100; index++ {
		var accountAddress common.Address
		binary.BigEndian.PutUint64(accountAddress[12:], index*7919)
		account := types.Account{
			Balance: new(big.Int).SetUint64(index * index),
			Nonce:   index % 17,
			Code:    make([]byte, index%41),
			Storage: make(map[common.Hash]common.Hash),
		}
		for position := range account.Code {
			account.Code[position] = byte(index + uint64(position))
		}
		for slot := uint64(0); slot < index%7; slot++ {
			var key, value common.Hash
			binary.BigEndian.PutUint64(key[24:], index*101+slot)
			binary.BigEndian.PutUint64(value[24:], index*1009+slot)
			account.Storage[key] = value
		}
		alloc[accountAddress] = account
	}
	root := (&core.Genesis{
		Config:     &params.ChainConfig{ChainID: big.NewInt(777)},
		GasLimit:   1,
		Difficulty: big.NewInt(1),
		Alloc:      alloc,
	}).ToBlock().Root()
	if got, want := root.Hex(), "0x299f1f4add7451ed9276bd7fbd85be1b3d8cde3d229bb73bba3f02525644fc35"; got != want {
		t.Fatalf("account trie root = %s, want %s", got, want)
	}
}

func TestParseDocumentUsesCoreGenesisDefaultGasLimit(t *testing.T) {
	document := strings.Replace(
		genesisFixture,
		`"gasLimit":"0x1c9c380"`,
		`"gasLimit":"0x0"`,
		1,
	)
	spec, block, err := parseDocument([]byte(document), 777)
	if err != nil {
		t.Fatal(err)
	}
	if spec.GasLimit != 0 {
		t.Fatalf("genesis gas limit = %d, want zero source value", spec.GasLimit)
	}
	if got := block.GasLimit(); got != params.GenesisGasLimit {
		t.Fatalf("block gas limit = %d, want geth default %d", got, params.GenesisGasLimit)
	}
	if got, want := block.Hash().Hex(), "0x502254e8fe5f09afb6c8f3123d2ae6ba98d317151b27db6d214f5d9b2bf4118a"; got != want {
		t.Fatalf("zero-gas block hash = %s, want %s", got, want)
	}
}

func TestParseDocumentRejectsHostileIdentityAndJSON(t *testing.T) {
	tooLargeBalance := new(big.Int).Lsh(big.NewInt(1), 256).String()
	tests := []struct {
		name      string
		document  string
		chainID   uint64
		wantError string
	}{
		{name: "duplicate", document: `{"config":{"chainId":1,"chainId":2},"gasLimit":"0x1","difficulty":"0x1","alloc":{}}`, chainID: 1},
		{name: "wrong chain", document: genesisFixture, chainID: 778},
		{
			name: "invalid fork order",
			document: `{"config":{"chainId":777,"homesteadBlock":1,"eip150Block":0},` +
				`"gasLimit":"0x1","difficulty":"0x1","alloc":{}}`,
			chainID: 777,
		},
		{
			name: "semantic duplicate address",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1"},` +
				`"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA":{"balance":"0x2"}}}`,
			chainID:   777,
			wantError: "genesis allocation contains a duplicate address",
		},
		{
			name: "semantic duplicate address prefix",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1"},` +
				`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x2"}}}`,
			chainID:   777,
			wantError: "genesis allocation contains a duplicate address",
		},
		{
			name: "semantic duplicate storage key",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1","storage":{` +
				`"0x0":"0x1","0x00":"0x2"}}}}`,
			chainID:   777,
			wantError: "genesis account storage contains a duplicate key",
		},
		{
			name: "balance exceeds uint256",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"` + tooLargeBalance + `"}}}`,
			chainID: 777,
		},
		{
			name: "negative balance",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"-1"}}}`,
			chainID:   777,
			wantError: "genesis account balance is outside uint256",
		},
		{
			name: "fork block exceeds uint256",
			document: `{"config":{"chainId":777,"homesteadBlock":` + tooLargeBalance + `},` +
				`"gasLimit":"0x1","difficulty":"0x1","alloc":{}}`,
			chainID:   777,
			wantError: "genesis chain config contains a value outside uint256",
		},
		{
			name:      "negative difficulty",
			document:  `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"-1","alloc":{}}`,
			chainID:   777,
			wantError: "genesis difficulty is outside uint256",
		},
		{
			name: "odd length code",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1","code":"0x1"}}}`,
			chainID: 777,
		},
		{name: "trailing", document: genesisFixture + `{}`, chainID: 777},
		{name: "missing allocation", document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1"}`, chainID: 777},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseDocument([]byte(test.document), test.chainID)
			if err == nil {
				t.Fatal("expected parse failure")
			}
			if test.wantError != "" && err.Error() != test.wantError {
				t.Fatalf("parse error = %q, want %q", err, test.wantError)
			}
		})
	}
}

func TestParseDocumentUsesCoreGenesisJSONAuthority(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		document string
	}{
		{
			name: "quoted chain config timestamp",
			document: `{"config":{"chainId":777,"shanghaiTime":"0x0"},` +
				`"gasLimit":"0x1","difficulty":"0x1","alloc":{}}`,
		},
		{
			name: "quoted slot number",
			document: `{"config":{"chainId":777},"slotNumber":"0x7",` +
				`"gasLimit":"0x1","difficulty":"0x1","alloc":{}}`,
		},
		{
			name: "odd length storage key",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1","storage":{` +
				`"0x0":"0x01"}}}}`,
		},
		{
			name: "odd length storage value",
			document: `{"config":{"chainId":777},"gasLimit":"0x1","difficulty":"0x1","alloc":{` +
				`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"balance":"0x1","storage":{` +
				`"0x00":"0x1"}}}}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := parseDocument([]byte(test.document), 777); err == nil {
				t.Fatal("expected geth Genesis decoder to reject the document")
			}
		})
	}
}

func TestEthereumPrimitiveConstantsAndEmptyTrie(t *testing.T) {
	t.Parallel()
	root, err := storageRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if root != types.EmptyRootHash ||
		root.Hex() != "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421" {
		t.Fatalf("empty trie root = %s", root.Hex())
	}
	if got := crypto.Keccak256Hash(nil).Hex(); got != types.EmptyCodeHash.Hex() {
		t.Fatalf("empty code hash = %s", got)
	}
}
