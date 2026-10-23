//go:build integration

package integration_test

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPushFailedCreateDoesNotDropActivityPage(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := testfixture.New(testfixture.Options{Number: 0})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repo, genesis)
	recipient := common.Address{19: 2}
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number: 1, ParentHash: genesis.Block.Hash(),
		Transactions: []integrationTransactionOptions{
			{Type: types.LegacyTxType, ContractCreation: true, Failed: true},
			{Type: types.LegacyTxType, To: &recipient},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	from, err := types.Sender(types.LatestSignerForChainID(block.Block.Transactions()[0].ChainId()), block.Block.Transactions()[0])
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,$2,'user','active',now(),now())`, user, from.Bytes())
	service, err := watchlist.New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, watch := range []gen.WatchInput{
		{Address: from.Hex(), Label: "Sender", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "out", Enabled: true},
		{Address: recipient.Hex(), Label: "Recipient", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "in", Enabled: true},
		{Address: (common.Address{}).Hex(), Label: "Zero", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "in", Enabled: true},
	} {
		if _, err := service.Save(ctx, user, "", watch); err != nil {
			t.Fatal(err)
		}
	}
	sender := startPushWorker(t, db, user)
	commitCanonical(t, ctx, repo, block)
	var chain pgtype.Numeric
	if err := chain.Scan("1"); err != nil {
		t.Fatal(err)
	}
	rows, err := dbgen.New(db).PushActivity(ctx, dbgen.PushActivityParams{ChainID: chain, BlockHash: block.Block.Hash().Bytes()})
	if err != nil || len(rows) != 2 {
		t.Fatalf("mixed activity page: rows=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		if row.SourceKey == "tx:"+common.Bytes2Hex(block.Block.Transactions()[0].Hash().Bytes()) && row.ToAddress != nil {
			t.Fatalf("failed CREATE has a recipient: %x", row.ToAddress)
		}
	}
	waitPushCount(t, sender, 3)
	assertPushQuiet(t, sender, 3)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	got := make(map[string]int)
	for _, raw := range sender.payloads {
		var payload struct{ Body, Path string }
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		got[payload.Path+" "+payload.Body]++
	}
	want := []string{
		"/tx/" + block.Block.Transactions()[0].Hash().Hex() + " Sender · transaction · out",
		"/tx/" + block.Block.Transactions()[1].Hash().Hex() + " Sender · transaction · out",
		"/tx/" + block.Block.Transactions()[1].Hash().Hex() + " Recipient · transaction · in",
	}
	for _, key := range want {
		if got[key] != 1 {
			t.Fatalf("missing or repeated notification %q: %v", key, got)
		}
	}
}
