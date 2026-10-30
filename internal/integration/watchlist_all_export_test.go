//go:build integration

package integration_test

import (
	"encoding/csv"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
)

func TestWatchlistAllExport(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	address := common.Address{19: 2}
	zero := common.Address{}
	topics := func(signature string, from, to common.Address) []common.Hash {
		return []common.Hash{crypto.Keccak256Hash([]byte(signature)), common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())}
	}
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	logs := []*types.Log{
		{Address: common.Address{19: 20}, Topics: topics("Transfer(address,address,uint256)", zero, address), Data: maximum.Bytes()},
		{Address: common.Address{19: 21}, Topics: append(topics("Transfer(address,address,uint256)", address, address), common.BigToHash(maximum))},
		{Address: common.Address{19: 22}, Topics: append([]common.Hash{crypto.Keccak256Hash([]byte("TransferSingle(address,address,address,uint256,uint256)")), common.BytesToHash(address.Bytes())}, common.BytesToHash(address.Bytes()), common.BytesToHash(zero.Bytes())), Data: append(common.LeftPadBytes([]byte{8}, 32), maximum.Bytes()...)},
	}
	block, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &address, Logs: logs}}})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repo, block)
	execFixture(t, ctx, db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,1)`)
	service, _ := watchlist.New(db, 1)
	request := gen.AddressExportRequest{Address: address.Hex(), Kind: "all", Direction: "both", From: time.Unix(int64(stamp), 0), To: time.Unix(int64(stamp+11), 0)}
	export := func(input gen.AddressExportRequest) (watchlist.Export, error) {
		// Independent accounts keep these semantic cases separate from admission tests.
		user := uuid.NewString()
		execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,$2,'user','active',now(),now())`, user, common.HexToAddress(strings.ReplaceAll(user, "-", "")).Bytes())
		return service.Export(ctx, user, input)
	}
	if out, err := export(request); !errors.Is(err, watchlist.ErrUnavailable) || len(out.Bytes) != 0 {
		t.Fatalf("missing Token coverage: %v", err)
	}
	markTokenStageComplete(t, ctx, db, genesis)
	markTokenStageComplete(t, ctx, db, block)
	for _, test := range []struct {
		direction gen.AddressExportRequestDirection
		kinds     []string
	}{
		{"both", []string{"erc20", "erc721", "erc1155", "transaction"}},
		{"in", []string{"erc20", "erc721", "transaction"}},
		{"out", []string{"erc721", "erc1155"}},
	} {
		t.Run(string(test.direction), func(t *testing.T) {
			input := request
			input.Direction = test.direction
			out, err := export(input)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(strings.NewReader(string(out.Bytes))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			kinds := make([]string, 0, len(rows)-1)
			for _, row := range rows[1:] {
				kinds = append(kinds, row[8])
				if row[2] != block.Block.Hash().Hex() || row[4] != block.Block.Hash().Hex() {
					t.Fatalf("mixed snapshot: %v", row)
				}
				if row[8] == "erc721" && (row[9] != "self" || row[18] != maximum.String()) {
					t.Fatalf("self NFT/precision: %v", row)
				}
				if (row[8] == "erc20" || row[8] == "erc1155") && row[19] != maximum.String() {
					t.Fatalf("amount precision: %v", row)
				}
			}
			if !reflect.DeepEqual(kinds, test.kinds) {
				t.Fatalf("kinds=%v want=%v", kinds, test.kinds)
			}
		})
	}
	for _, test := range []struct {
		from, to int64
		rows     int
	}{{int64(stamp), int64(stamp + 10), 1}, {int64(stamp + 10), int64(stamp + 11), 5}} {
		input := request
		input.From = time.Unix(test.from, 0)
		input.To = time.Unix(test.to, 0)
		out, err := export(input)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(out.Bytes))).ReadAll()
		if err != nil || len(rows) != test.rows {
			t.Fatalf("time boundary rows=%d error=%v", len(rows), err)
		}
	}
	// Each individual kind is below the cap: the combined export must enforce it.
	execFixture(t, ctx, db, `INSERT INTO transactions(chain_id,hash,raw) SELECT 1,decode(md5(i::text)||md5(i::text),'hex'),'{}'::jsonb FROM generate_series(1,9997) AS i`)
	execFixture(t, ctx, db, `INSERT INTO transaction_inclusions(chain_id,block_number,block_hash,tx_index,tx_hash,raw) SELECT 1,1,$1,i,decode(md5(i::text)||md5(i::text),'hex'),jsonb_build_object('from',$2::text,'to',$2::text,'value','0x1') FROM generate_series(1,9997) AS i`, block.Block.Hash().Bytes(), strings.ToLower(address.Hex()))
	execFixture(t, ctx, db, `INSERT INTO receipts(chain_id,block_number,block_hash,tx_index,tx_hash,raw) SELECT chain_id,block_number,block_hash,tx_index,tx_hash,'{"status":"0x1"}'::jsonb FROM transaction_inclusions WHERE block_number=1 AND tx_index BETWEEN 1 AND 9996`)
	if out, err := export(request); err != nil || strings.Count(string(out.Bytes), "\n") != 10001 {
		t.Fatalf("10000 combined rows size=%d error=%v", len(out.Bytes), err)
	}
	execFixture(t, ctx, db, `INSERT INTO receipts(chain_id,block_number,block_hash,tx_index,tx_hash,raw) SELECT chain_id,block_number,block_hash,tx_index,tx_hash,'{"status":"0x1"}'::jsonb FROM transaction_inclusions WHERE block_number=1 AND tx_index=9997`)
	if out, err := export(request); !errors.Is(err, watchlist.ErrExportLimit) || len(out.Bytes) != 0 {
		t.Fatalf("10001 combined rows size=%d error=%v", len(out.Bytes), err)
	}
}
