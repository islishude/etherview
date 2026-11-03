//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/testpgx"
)

func TestAddressLogsTopicPaginationAndReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	var blocks []chainbundle.Bundle
	parent := testHash(0)
	address := testAddress(30)
	for n := range uint64(3) {
		b := derivedTokenBundle(t, n, testHash(330000+n), parent, testHash(331000+n), "address-log", address, testAddress(31), testAddress(32), 10+n)
		if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{b}); err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, b)
		parent = b.Block.Hash()
	}
	reader, _ := catalog.NewPostgres(db, catalog.Options{})

	for _, test := range []struct {
		from, to string
		count    int
		first    string
	}{{"1", "1", 1, "1"}, {"1", "", 2, "2"}, {"", "1", 2, "1"}, {"2", "9999", 1, "2"}, {"3", "", 0, ""}, {"9007199254740993", "", 0, ""}} {
		page, err := reader.AddressLogs(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), FromBlock: test.from, ToBlock: test.to})
		if err != nil || len(page.Items) != test.count {
			t.Fatalf("range %+v: %+v %v", test, page, err)
		}
		if test.count > 0 && page.Items[0].BlockNumber != test.first {
			t.Fatal(page)
		}
		if test.count == 0 && page.Snapshot.Browsing.State != "pending" {
			t.Fatal("future range falsely complete", page)
		}
	}
	r := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), Limit: 1}
	first, err := reader.AddressLogs(ctx, r)
	if err != nil || len(first.Items) != 1 || first.Items[0].BlockNumber != "2" || first.NextCursor == "" || first.Snapshot.Browsing.State != "complete" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	changed := r
	changed.Cursor = first.NextCursor
	changed.FromBlock = "1"
	if _, err := reader.AddressLogs(ctx, changed); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatal("cursor accepted changed range", err)
	}
	r.Cursor = first.NextCursor
	second, err := reader.AddressLogs(ctx, r)
	if err != nil || len(second.Items) != 1 || second.Items[0].BlockNumber != "1" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	for i, topic := range first.Items[0].Log.Topics {
		r.Cursor = ""
		r.TopicIndex = &i
		r.Topic = topic
		page, err := reader.AddressLogs(ctx, r)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("topic%d=%+v err=%v", i, page, err)
		}
	}
	third := 3
	r.TopicIndex = &third
	r.Topic = testHash(9).Hex()
	page, err := reader.AddressLogs(ctx, r)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("absent topic3=%+v err=%v", page, err)
	}
	r.Topic = ""
	r.TopicIndex = nil
	r.Address = testAddress(99).Hex()
	page, err = reader.AddressLogs(ctx, r)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("other emitter=%+v err=%v", page, err)
	}
	r.Address = address.Hex()
	r.Cursor = first.NextCursor
	replacement := derivedTokenBundle(t, 2, testHash(340002), blocks[1].Block.Hash(), testHash(341002), "replacement", address, testAddress(31), testAddress(32), 99)
	applyDerivedReorg(t, ctx, repository, blocks[1], []chainbundle.Bundle{blocks[2]}, []chainbundle.Bundle{replacement}, "address log fork")
	if _, err = reader.AddressLogs(ctx, r); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("reorg cursor=%v", err)
	}
}

func TestAddressLogsQueryPlansAcrossPartitions(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, _ := store.NewPostgresRepository(db)
	address := testAddress(30)
	parent := testHash(0)
	for n := uint64(999999); n < 1000002; n++ {
		b := derivedTokenBundle(t, n, testHash(430000+n), parent, testHash(431000+n), "log-plan", address, testAddress(31), testAddress(32), 10)
		commitCanonical(t, ctx, repository, b)
		parent = b.Block.Hash()
		writeStart := time.Now()
		execFixture(t, ctx, db, `INSERT INTO logs(chain_id,block_number,block_hash,log_index,tx_index,tx_hash,address,raw)
    SELECT l.chain_id,l.block_number,l.block_hash,g,l.tx_index,l.tx_hash,l.address,
      jsonb_set(l.raw,'{topics}',jsonb_build_array(l.raw->'topics'->0,l.raw->'topics'->1,l.raw->'topics'->2,
        CASE WHEN g%1000=0 THEN '0x'||repeat('ab',32) ELSE '0x'||lpad(to_hex(g),64,'0') END))
    FROM logs l CROSS JOIN generate_series(1,10000) g WHERE l.block_hash=$1 AND l.log_index=0`, b.Block.Hash().Bytes())
		t.Logf("10000 indexed log insert duration=%s", time.Since(writeStart))
	}
	execFixture(t, ctx, db, `ANALYZE logs`)
	for _, test := range []struct {
		name  string
		args  []any
		index string
	}{
		{"AddressLogsAll", []any{"1", address.Bytes(), "1000001", "1000000", "1000001", int64(1 << 62), int64(1 << 62), int32(26)}, "address"},
		{"AddressLogsTopic3", []any{"1", address.Bytes(), "1000001", "1000000", "1000001", int64(1 << 62), int64(1 << 62), common.HexToHash("0x" + strings.Repeat("ab", 32)).Bytes(), int32(26)}, "topic3"},
	} {
		rows, err := db.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+testpgx.Statement(test.name), test.args...)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(lines, "\n")
		t.Logf("%s\n%s", test.name, plan)
		if !strings.Contains(plan, test.index) || (test.name == "AddressLogsAll" && !strings.Contains(plan, "address_block_number")) {
			t.Fatalf("missing indexed access: %s", plan)
		}
	}
	var size int64
	if err := db.QueryRow(ctx, `SELECT sum(pg_indexes_size(relid))::bigint FROM pg_partition_tree('logs') WHERE isleaf`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	t.Logf("30003 logs, index bytes=%d", size)
}

func TestAddressLogsPublishedDirectoryAndReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, _ := store.NewPostgresRepository(db)
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	address := testAddress(730)
	runtime := []byte{0x60, 0x00}
	code := crypto.Keccak256Hash(runtime)
	abiJSON := `[
 {"type":"event","name":"Changed","inputs":[{"name":"value","type":"uint256"}]},
 {"type":"event","name":"Changed","inputs":[{"name":"value","type":"bytes32"}]},
 {"type":"event","name":"Hidden","anonymous":true,"inputs":[{"name":"value","type":"uint256"}]},
 {"type":"event","name":"Four","inputs":[{"name":"a","type":"bytes32","indexed":true},{"name":"b","type":"bytes32","indexed":true},{"name":"c","type":"bytes32","indexed":true}]}]`
	var blocks []chainbundle.Bundle
	parent := testHash(0)
	processor, _ := enrich.NewPostgresABIProcessor(db)
	for n := range uint64(2) {
		logs := []*types.Log{
			{Address: address, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Changed(uint256)"))}, Data: abiUintWord(1)},
			{Address: address, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Changed(bytes32)"))}, Data: abiUintWord(2)},
			{Address: address, Topics: []common.Hash{}, Data: abiUintWord(3)},
			{Address: address, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Four(bytes32,bytes32,bytes32)")), testHash(41), testHash(42), testHash(43)}, Data: []byte{}},
		}
		for range 250 {
			logs = append(logs, &types.Log{Address: address, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Changed(uint256)"))}, Data: abiUintWord(7)})
		}
		b, err := newIntegrationBundle(integrationBundleOptions{Number: n, ParentHash: parent, ExtraData: []byte("address-event-directory"), Transactions: []integrationTransactionOptions{{To: &address, Logs: logs}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{b}); err != nil {
			t.Fatal(err)
		}
		ref := mustBlockRef(t, b)
		insertABICodeObservation(t, ctx, db, ref, address, code)
		if n == 0 {
			insertVerifiedContractFixture(t, ctx, db, address.Bytes(), code.Bytes(), 0, nil, "0.8.30", "Events", abiJSON, `{}`, `{}`)
		}
		publishABIProxyStage(t, ctx, db, ref, map[string]proxyContractState{address.Hex(): {code: runtime}})
		publishABIStage(t, ctx, db, processor, ref)
		blocks = append(blocks, b)
		parent = b.Block.Hash()
	}
	reader, _ := catalog.NewPostgres(db, catalog.Options{})
	request := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), Name: "ch", Limit: 1}
	first, err := reader.AddressLogEvents(ctx, request)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first names=%+v err=%v", first, err)
	}
	later := testBundle(2, testHash(930002), blocks[1].Block.Hash(), testHash(931002), "later-directory-head")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{later}); err != nil {
		t.Fatal(err)
	}
	request.Cursor = first.NextCursor
	second, err := reader.AddressLogEvents(ctx, request)
	if err != nil || len(second.Items) != 1 || second.Items[0].Name != "Changed" || second.Items[0].Signature == first.Items[0].Signature {
		t.Fatalf("overload names=%+v err=%v", second, err)
	}
	hiddenRequest := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), EventSignature: "Hidden(uint256)", Limit: 1}
	hidden, err := reader.AddressLogs(ctx, hiddenRequest)
	if err != nil || len(hidden.Items) != 1 || len(hidden.Items[0].Log.Topics) != 0 || hidden.NextCursor == "" {
		t.Fatalf("anonymous logs=%+v err=%v", hidden, err)
	}
	position := 3
	topicPage, err := reader.AddressLogs(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), TopicIndex: &position, Topic: testHash(43).Hex()})
	if err != nil || len(topicPage.Items) != 2 {
		t.Fatalf("topic3=%+v err=%v", topicPage, err)
	}
	rawRequest := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), Limit: 1}
	raw, err := reader.AddressLogs(ctx, rawRequest)
	if err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `ANALYZE address_event_logs; ANALYZE address_event_blocks; ANALYZE address_event_directory`)
	for _, test := range []struct {
		name string
		args []any
	}{
		{"AddressLogEvents", []any{"1", address.Bytes(), "%", int64(0), "2", "0", int32(25)}},
		{"AddressLogsSignature", []any{"1", address.Bytes(), "2", "0", "2", int64(1 << 62), int64(1 << 62), "Changed(uint256)", int32(26)}},
	} {
		rows, err := db.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+testpgx.Statement(test.name), test.args...)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(lines, "\n")
		t.Logf("%s (508 logs, four directory entries)\n%s", test.name, plan)
		if test.name == "AddressLogEvents" && (strings.Contains(plan, " on logs_") || strings.Contains(plan, " on abi_decodings")) {
			t.Fatalf("directory scanned historical log bodies: %s", plan)
		}
		if test.name == "AddressLogsSignature" && !strings.Contains(plan, "Index Scan using address_event_logs_p_0_1000000_lookup_idx") {
			t.Fatalf("signature page lost ordered projection index: %s", plan)
		}
	}
	rangedNames := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), FromBlock: "1", ToBlock: "1", Limit: 1}
	namesBefore, err := reader.AddressLogEvents(ctx, rangedNames)
	if err != nil || namesBefore.NextCursor == "" {
		t.Fatal(namesBefore, err)
	}
	rangedNames.Cursor = namesBefore.NextCursor
	rangedLogs := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), FromBlock: "1", ToBlock: "1", EventSignature: "Changed(uint256)", Limit: 1}
	logsBefore, err := reader.AddressLogs(ctx, rangedLogs)
	if err != nil || logsBefore.NextCursor == "" {
		t.Fatal(logsBefore, err)
	}
	rangedLogs.Cursor = logsBefore.NextCursor
	for position, topic := range topicPage.Items[0].Log.Topics {
		page, err := reader.AddressLogs(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), FromBlock: "1", ToBlock: "1", TopicIndex: &position, Topic: topic})
		if err != nil || len(page.Items) != 1 || page.Items[0].BlockNumber != "1" {
			t.Fatal("ranged topic", position, page, err)
		}
	}
	emptyNames, err := reader.AddressLogEvents(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), FromBlock: "2", ToBlock: "2"})
	if err != nil || len(emptyNames.Items) != 0 {
		t.Fatal("range directory leaked historical names", emptyNames, err)
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	word, _ := enrich.ParseWord(blocks[0].Block.Hash().Hex())
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{ChainID: "1", Stage: enrich.ABIStage, BlockNumber: 0, BlockHash: word, Replay: enrich.ReplaySource{Kind: "address-event-test", Key: "replay"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AddressLogEvents(ctx, rangedNames); err != nil {
		t.Fatal("out-of-range replay invalidated directory", err)
	}
	if _, err := reader.AddressLogs(ctx, rangedLogs); err != nil {
		t.Fatal("out-of-range replay invalidated logs", err)
	}
	if _, err := reader.AddressLogEvents(ctx, request); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("directory replay cursor=%v", err)
	}
	hiddenRequest.Cursor = hidden.NextCursor
	if _, err := reader.AddressLogs(ctx, hiddenRequest); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("signature replay cursor=%v", err)
	}
	rawRequest.Cursor = raw.NextCursor
	if _, err := reader.AddressLogs(ctx, rawRequest); err != nil {
		t.Fatalf("ABI replay invalidated raw logs: %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT sum(log_count)::bigint FROM address_event_directory WHERE chain_id=1 AND address=$1`, 254, address.Bytes())
	publishABIStage(t, ctx, db, processor, mustBlockRef(t, blocks[0]))
	assertRowCount(t, ctx, db, `SELECT sum(log_count)::bigint FROM address_event_directory WHERE chain_id=1 AND address=$1`, 508, address.Bytes())
	replacement := testBundle(1, testHash(940001), blocks[0].Block.Hash(), testHash(941001), "event-directory-detach")
	applyDerivedReorg(t, ctx, repository, blocks[0], []chainbundle.Bundle{blocks[1], later}, []chainbundle.Bundle{replacement}, "event directory detach")
	assertRowCount(t, ctx, db, `SELECT sum(log_count)::bigint FROM address_event_directory WHERE chain_id=1 AND address=$1`, 254, address.Bytes())
	applyDerivedReorg(t, ctx, repository, blocks[0], []chainbundle.Bundle{replacement}, []chainbundle.Bundle{blocks[1], later}, "event directory reattach")
	assertRowCount(t, ctx, db, `SELECT sum(log_count)::bigint FROM address_event_directory WHERE chain_id=1 AND address=$1`, 254, address.Bytes())
	ref := mustBlockRef(t, blocks[1])
	reattachedWord, _ := enrich.ParseWord(ref.Hash.Hex())
	for _, stage := range []enrich.StageID{enrich.ProxyStage, enrich.ABIStage} {
		if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{ChainID: "1", Stage: stage, BlockNumber: ref.Number, BlockHash: reattachedWord,
			Replay: enrich.ReplaySource{Kind: "address-event-test", Key: "reattach"}}); err != nil {
			t.Fatal(err)
		}
	}
	publishABIProxyStage(t, ctx, db, ref, map[string]proxyContractState{address.Hex(): {code: runtime}})
	publishABIStage(t, ctx, db, processor, ref)
	assertRowCount(t, ctx, db, `SELECT sum(log_count)::bigint FROM address_event_directory WHERE chain_id=1 AND address=$1`, 508, address.Bytes())
}
