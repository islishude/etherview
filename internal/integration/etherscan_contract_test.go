//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
)

func TestEtherscanVerifiedContractFollowsCanonicalCodeHash(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	genesis := testBundle(0, testHash(7_000), testHash(0), testHash(8_000), "genesis")
	oldBlock := testBundle(1, testHash(7_001), testHash(7_000), testHash(8_001), "old-code")
	currentBlock := testBundle(2, testHash(7_002), testHash(7_001), testHash(8_002), "current-code")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, oldBlock)
	commitCanonical(t, ctx, repository, currentBlock)

	address := testAddress(700)
	addressBytes := mustBytes(t, address)
	oldCodeHash := mustBytes(t, testHash(9_001))
	currentCodeHash := mustBytes(t, testHash(9_002))
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES
			(1, $1, 1, $2, $3, $4, TRUE),
			(1, $1, 2, $5, $6, $7, TRUE)`,
		addressBytes,
		mustBytes(t, testHash(7_001)), oldCodeHash, []byte{0x60, 0x01},
		mustBytes(t, testHash(7_002)), currentCodeHash, []byte{0x60, 0x02},
	)
	insertEtherscanVerifiedContractFixture(t, ctx, db, addressBytes, oldCodeHash, 1,
		"Stale", `[{"type":"function","name":"stale","inputs":[]}]`,
		`{"Stale.sol":{"content":"contract Stale{}"}}`)

	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1})
	if err != nil {
		t.Fatalf("create Etherscan backend: %v", err)
	}
	unknownValues := url.Values{"address": {testAddress(701).String()}}
	if result, err := backend.Execute(ctx, etherscan.Request{Module: "contract", Action: "getabi", Values: unknownValues}); result != "" || !errors.Is(err, etherscan.ErrStateUnavailable) {
		t.Fatalf("unknown current code result=%#v error=%v, want state unavailable", result, err)
	}

	values := url.Values{"address": {address.String()}}
	for _, action := range []string{"getabi", "getsourcecode"} {
		result, err := backend.Execute(ctx, etherscan.Request{Module: "contract", Action: action, Values: values})
		if !errors.Is(err, etherscan.ErrContractUnverified) {
			t.Fatalf("%s with only stale verification result=%#v error=%v, want contract unverified", action, result, err)
		}
	}

	insertEtherscanVerifiedContractFixture(t, ctx, db, addressBytes, currentCodeHash, 2,
		"Current", `[{"type":"function","name":"current","inputs":[]}]`,
		`{"Current.sol":{"content":"contract Current{}"}}`)
	abi, err := backend.Execute(ctx, etherscan.Request{Module: "contract", Action: "getabi", Values: values})
	if err != nil {
		t.Fatalf("get current ABI: %v", err)
	}
	if abi != `[{"inputs":[],"name":"current","type":"function"}]` {
		t.Fatalf("current ABI = %#v", abi)
	}

	source, err := backend.Execute(ctx, etherscan.Request{Module: "contract", Action: "getsourcecode", Values: values})
	if err != nil {
		t.Fatalf("get current source: %v", err)
	}
	payload, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal current source result: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(payload, &rows); err != nil {
		t.Fatalf("decode current source result: %v", err)
	}
	if len(rows) != 1 || rows[0]["ContractName"] != "Current" {
		t.Fatalf("current source result = %s", payload)
	}
	sourceCode, _ := rows[0]["SourceCode"].(string)
	if !strings.Contains(sourceCode, "contract Current{}") || strings.Contains(sourceCode, "Stale") {
		t.Fatalf("current source code = %q", sourceCode)
	}
}

func TestEtherscanContractCreationIncludesFactoryTraceFacts(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatalf("create PostgreSQL repository: %v", err)
	}
	genesis := testBundle(0, testHash(17_000), testHash(0), testHash(18_000), "genesis")
	createdAt := testBundle(1, testHash(17_001), testHash(17_000), testHash(18_001), "factory-create")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, createdAt)

	created := testAddress(1_700)
	factory := testAddress(2)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash,
			transaction_index, trace_path, parent_path, depth, call_type,
			from_address, to_address, created_address, value, gas, gas_used,
			input, output, error, reverted, canonical
		) VALUES (
			1, 1, $1, $2, 0, '0', '', 1, 'CREATE2',
			$3, NULL, $4, 0, 50000, 21000,
			$5, $6, NULL, FALSE, TRUE
		)`,
		mustBytes(t, testHash(17_001)), mustBytes(t, testHash(18_001)),
		mustBytes(t, factory), mustBytes(t, created), []byte{0x60, 0x00}, []byte{0x60, 0x01},
	)

	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{ChainID: 1})
	if err != nil {
		t.Fatalf("create Etherscan backend: %v", err)
	}
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "getcontractcreation",
		Values: url.Values{"contractaddresses": {created.String()}},
	})
	if err != nil {
		t.Fatalf("query factory contract creation: %v", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal factory contract creation: %v", err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(payload, &rows); err != nil {
		t.Fatalf("decode factory contract creation: %v", err)
	}
	if len(rows) != 1 || !strings.EqualFold(rows[0]["contractAddress"], created.String()) ||
		!strings.EqualFold(rows[0]["contractCreator"], testAddress(1).String()) ||
		!strings.EqualFold(rows[0]["contractFactory"], factory.String()) ||
		rows[0]["txHash"] != testHash(18_001).String() ||
		rows[0]["blockNumber"] != "1" || rows[0]["timestamp"] != "1700000001" ||
		rows[0]["creationBytecode"] != "0x6000" {
		t.Fatalf("factory contract creation = %s", payload)
	}
}

func insertEtherscanVerifiedContractFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	address, codeHash []byte,
	validFrom uint64,
	contractName, abi, sources string,
) {
	t.Helper()
	insertVerifiedContractFixture(
		t, ctx, db, address, codeHash, validFrom, nil,
		"v0.8.30+commit.73712a01", contractName, abi, sources,
		`{"optimizer":{"enabled":false,"runs":0}}`,
	)
}
