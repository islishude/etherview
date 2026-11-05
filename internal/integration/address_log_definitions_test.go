//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

func TestAddressLogsSolidityIndexedDefinitions(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/contracts/fixtures/indexed-events.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ABI   json.RawMessage `json:"abi"`
		Cases []struct {
			Name   string        `json:"name"`
			Topics []common.Hash `json:"topics"`
			Data   string        `json:"data"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, _ := store.NewPostgresRepository(db)
	if err = repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	address := testAddress(820)
	code := []byte{0x60, 0x00}
	hash := crypto.Keccak256Hash(code)
	var logs []*types.Log
	for _, vector := range fixture.Cases {
		logs = append(logs, &types.Log{Address: address, Topics: vector.Topics, Data: common.FromHex(vector.Data)})
	}
	b, err := newIntegrationBundle(integrationBundleOptions{Number: 0, ParentHash: testHash(0), ExtraData: []byte("indexed-solidity"), Transactions: []integrationTransactionOptions{{To: &address, Logs: logs}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{b}); err != nil {
		t.Fatal(err)
	}
	ref := mustBlockRef(t, b)
	insertABICodeObservation(t, ctx, db, ref, address, hash)
	insertVerifiedContractFixture(t, ctx, db, address.Bytes(), hash.Bytes(), 0, nil, "0.8.37", "Events", string(fixture.ABI), `{}`, `{}`)
	publishABIProxyStage(t, ctx, db, ref, map[string]proxyContractState{address.Hex(): {code: code}})
	processor, _ := enrich.NewPostgresABIProcessor(db)
	publishABIStage(t, ctx, db, processor, ref)
	reader, _ := catalog.NewPostgres(db, catalog.Options{})
	directory, err := reader.AddressLogEvents(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Items) != 6 {
		t.Fatalf("directory %+v", directory)
	}
	for _, vector := range fixture.Cases {
		var event catalog.AddressLogEvent
		for _, item := range directory.Items {
			if item.Name == vector.Name {
				event = item
			}
		}
		if event.EventID == "" {
			t.Fatal("missing definition", vector.Name)
		}
		request := catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), EventID: event.EventID}
		for _, field := range event.IndexedFields {
			request.Topics[field.TopicIndex] = vector.Topics[field.TopicIndex].Hex()
		}
		page, err := reader.AddressLogs(ctx, request)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s page=%+v err=%v", vector.Name, page, err)
		}
		if vector.Name == "Example" {
			invalid := request
			invalid.Topics[3] = testHash(999).Hex()
			if _, err := reader.AddressLogs(ctx, invalid); !errors.Is(err, catalog.ErrInvalidInput) {
				t.Fatalf("non-indexed position accepted: %v", err)
			}
			request.Topics[1] = "" // second alone
			page, err = reader.AddressLogs(ctx, request)
			if err != nil || len(page.Items) != 1 {
				t.Fatal(page, err)
			}
			request.Topics[2] = testHash(999).Hex()
			page, err = reader.AddressLogs(ctx, request)
			if err != nil || len(page.Items) != 0 {
				t.Fatal(page, err)
			}
		}
		if vector.Name == "Nested" && (len(event.IndexedFields[0].Components) != 4 || event.IndexedFields[0].Components[0].Name != "label") {
			t.Fatalf("lost tuple shape: %+v", event)
		}
		exact, err := reader.AddressLogEvents(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), EventID: event.EventID})
		if err != nil || len(exact.Items) != 1 {
			t.Fatal(exact, err)
		}
	}

	// Another code version reuses the signature with a different indexed layout.
	changedABI := `[{"type":"event","name":"Example","inputs":[{"name":"first","type":"address"},{"name":"second","type":"address","indexed":true},{"name":"third","type":"uint256","indexed":true}]}]`
	nextCode := []byte{0x60, 0x01}
	nextHash := crypto.Keccak256Hash(nextCode)
	changedLog := &types.Log{Address: address, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Example(address,address,uint256)")), common.BytesToHash(testAddress(0x22).Bytes()), common.HexToHash("0x05")}, Data: common.LeftPadBytes(testAddress(0x11).Bytes(), 32)}
	next, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: b.Block.Hash(), ExtraData: []byte("changed-indexed-layout"), Transactions: []integrationTransactionOptions{{To: &address, Logs: []*types.Log{changedLog}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{next}); err != nil {
		t.Fatal(err)
	}
	nextRef := mustBlockRef(t, next)
	insertABICodeObservation(t, ctx, db, nextRef, address, nextHash)
	insertVerifiedContractFixture(t, ctx, db, address.Bytes(), nextHash.Bytes(), 1, nil, "0.8.37", "EventsV2", changedABI, `{}`, `{}`)
	publishABIProxyStage(t, ctx, db, nextRef, map[string]proxyContractState{address.Hex(): {code: nextCode}})
	publishABIStage(t, ctx, db, processor, nextRef)
	definitions, err := reader.AddressLogEvents(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), Name: "Example"})
	if err != nil || len(definitions.Items) != 2 {
		t.Fatal(definitions, err)
	}
	if definitions.Items[0].EventID == definitions.Items[1].EventID {
		t.Fatal("different layouts share identity")
	}
	for _, definition := range definitions.Items {
		page, err := reader.AddressLogs(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), EventID: definition.EventID})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		if (definition.IndexedFields[0].Name == "first") != (page.Items[0].BlockNumber == "0") {
			t.Fatal("layout leaked across code versions")
		}
	}
	bySignature, err := reader.AddressLogs(ctx, catalog.AddressLogRequest{ChainID: "1", Address: address.Hex(), EventSignature: "Example(address,address,uint256)"})
	if err != nil || len(bySignature.Items) != 2 {
		t.Fatal(bySignature, err)
	}
}
