//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

const integrationTokenABI = `[
  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"amount","type":"uint256"}]},
  {"type":"error","name":"Unauthorized","inputs":[{"name":"caller","type":"address"}]}
]`

func TestABIStageBindsPriorityRangeAndForkIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresABIProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(70_000), testHash(0), testHash(71_000), "abi-genesis")
	commitCanonical(t, ctx, repository, genesis)
	direct, proxy, implementation := testAddress(700), testAddress(701), testAddress(702)
	recipient, caller := testAddress(703), testAddress(704)
	block := abiFixtureBundle(t, direct, proxy, recipient, caller)
	commitCanonical(t, ctx, repository, block)
	reference := mustBlockRef(t, block)
	directCode, proxyCode, implementationCode := testHash(72_000), testHash(72_001), testHash(72_002)
	insertABICodeObservation(t, ctx, db, reference, direct, directCode)
	insertABICodeObservation(t, ctx, db, reference, proxy, proxyCode)
	insertABIVerifiedContract(t, ctx, db, direct, directCode)
	insertABIVerifiedContract(t, ctx, db, implementation, implementationCode)
	insertABIProxyObservation(t, ctx, db, reference, proxy, proxyCode, implementation, implementationCode)
	insertABISignatureCandidates(t, ctx, db)
	insertABITrace(t, ctx, db, reference, block, proxy, recipient, caller)

	job := abiIntegrationJob(t, reference)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := processor.Process(ctx, job)
		if err != nil {
			t.Fatalf("process ABI stage attempt %d: %v", attempt+1, err)
		}
		if result.State != enrich.ResultComplete || result.Details["decoded"] != "5" || result.Details["bindings"] != "4" {
			t.Fatalf("ABI stage attempt %d result=%+v", attempt+1, result)
		}
	}

	assertABIBinding(t, ctx, db, reference, direct, directCode, "verified", "verified", direct, directCode)
	assertABIBinding(t, ctx, db, reference, direct, directCode, "signature_database", "guess", direct, directCode)
	assertABIBinding(t, ctx, db, reference, proxy, proxyCode, "proxy_implementation", "high", implementation, implementationCode)
	assertABIBinding(t, ctx, db, reference, proxy, proxyCode, "signature_database", "guess", proxy, proxyCode)
	assertABIDecodingSources(t, ctx, db, reference, map[string]string{
		"transaction_calldata:": "verified:verified",
		"log:0":                 "proxy_implementation:high",
		"trace_calldata:0":      "proxy_implementation:high",
		"trace_revert:0":        "proxy_implementation:high",
		"trace_revert:1":        "builtin:high",
	})
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND source = 'signature_database'
		  AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(abi) AS entry
			WHERE entry->>'type' = 'error' AND entry->>'name' IN ('Error', 'Panic')
		  )`,
		0, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_journals
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi@1' AND canonical`, 1, mustBytes(t, reference.Hash))

	assertSignatureGuessCannotBeVerified(t, ctx, db, reference, direct, directCode)

	replacement := testBundle(1, testHash(80_001), testHash(70_000), testHash(81_001), "abi-replacement")
	ancestor := mustBlockRef(t, genesis)
	replacementRef := mustBlockRef(t, replacement)
	if err := repository.ApplyReorg(ctx, "1", store.Reorg{
		Ancestor: ancestor, Detached: []store.BlockRef{reference}, Attached: []ethrpc.Bundle{replacement},
		Checkpoint: store.NewCoreCheckpoint(replacementRef), Reason: "ABI fork isolation fixture",
	}); err != nil {
		t.Fatalf("apply ABI fixture reorg: %v", err)
	}
	for _, table := range []string{"contract_abis", "abi_decodings"} {
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1 AND canonical`, table),
			0, mustBytes(t, reference.Hash),
		)
		assertRowCount(t, ctx, db,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE chain_id = 1 AND block_hash = $1 AND NOT canonical`, table),
			map[string]int{"contract_abis": 4, "abi_decodings": 5}[table], mustBytes(t, reference.Hash),
		)
	}
}

func abiFixtureBundle(t *testing.T, direct, proxy, recipient, caller ethrpc.Address) ethrpc.Bundle {
	t.Helper()
	block := testBundle(1, testHash(70_001), testHash(70_000), testHash(71_001), "abi-block")
	transaction := block.Block.Transactions[0].Transaction
	transaction.To = &direct
	transaction.Input = ethrpc.DataFromBytes(abiTransferCalldata(t, recipient, 17))
	log := &block.Receipts[0].Logs[0]
	log.Address = proxy
	transferTopic := enrich.SignatureHash("Transfer(address,address,uint256)")
	log.Topics = []ethrpc.Hash{
		mustRPCWord(t, transferTopic[:]),
		mustRPCWord(t, abiAddressWord(t, caller)),
		mustRPCWord(t, abiAddressWord(t, recipient)),
	}
	log.Data = ethrpc.DataFromBytes(abiUintWord(17))
	return block
}

func insertABICodeObservation(t *testing.T, ctx context.Context, db *sql.DB, block store.BlockRef, address ethrpc.Address, codeHash ethrpc.Hash) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)`,
		mustBytes(t, address), fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, codeHash), []byte{0x60, 0x00})
}

func insertABIVerifiedContract(t *testing.T, ctx context.Context, db *sql.DB, address ethrpc.Address, codeHash ethrpc.Hash) {
	t.Helper()
	insertVerifiedContractFixture(
		t, ctx, db, mustBytes(t, address), mustBytes(t, codeHash), 0, nil,
		"0.8.30", "Token", integrationTokenABI, `{}`, `{}`,
	)
}

func insertABIProxyObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy ethrpc.Address,
	proxyCode ethrpc.Hash,
	implementation ethrpc.Address,
	implementationCode ethrpc.Hash,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, proxy_code_hash,
			proxy_kind, implementation_address, implementation_code_hash,
			confidence, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, 'eip1967', $5, $6, 'high', TRUE)`,
		mustBytes(t, proxy), fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, proxyCode),
		mustBytes(t, implementation), mustBytes(t, implementationCode))
}

func insertABISignatureCandidates(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	entries := []struct {
		kind      string
		signature string
		entry     string
	}{
		{"function", "transfer(address,uint256)", `{"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]}`},
		{"event", "Transfer(address,address,uint256)", `{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"amount","type":"uint256"}]}`},
		{"error", "Unauthorized(address)", `{"type":"error","name":"Unauthorized","inputs":[{"name":"caller","type":"address"}]}`},
		{"error", "Error(string)", `{"type":"error","name":"Error","inputs":[{"name":"message","type":"string"}]}`},
		{"error", "Panic(uint256)", `{"type":"error","name":"Panic","inputs":[{"name":"code","type":"uint256"}]}`},
	}
	for _, entry := range entries {
		hash := enrich.SignatureHash(entry.signature)
		identifier := hash[:]
		if entry.kind != "event" {
			identifier = identifier[:4]
		}
		execFixture(t, ctx, db, `
			INSERT INTO abi_signature_candidates (kind, identifier, signature, abi_entry)
			VALUES ($1, $2, $3, $4::jsonb)`, entry.kind, identifier, entry.signature, []byte(entry.entry))
	}
}

func insertABITrace(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	bundle ethrpc.Bundle,
	target, recipient, caller ethrpc.Address,
) {
	t.Helper()
	transaction := bundle.Block.Transactions[0].Transaction
	selector := enrich.SignatureSelector("Unauthorized(address)")
	revert := append(append([]byte(nil), selector[:]...), abiAddressWord(t, caller)...)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			trace_path, depth, call_type, from_address, to_address, value, gas,
			gas_used, input, output, error, reverted, canonical
		) VALUES (
			1, $1::numeric, $2, $3, 0, '0', 0, 'call', $4, $5, 0, 100000,
			50000, $6, $7, 'execution reverted', TRUE, TRUE
		)`, fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, transaction.Hash),
		mustBytes(t, caller), mustBytes(t, target), abiTransferCalldata(t, recipient, 19), revert)

	builtinSelector := enrich.SignatureSelector("Error(string)")
	builtin := append([]byte(nil), builtinSelector[:]...)
	builtin = append(builtin, abiUintWord(32)...)
	builtin = append(builtin, abiUintWord(4)...)
	builtin = append(builtin, append([]byte("nope"), make([]byte, 28)...)...)
	execFixture(t, ctx, db, `
		INSERT INTO normalized_traces (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			trace_path, depth, call_type, from_address, to_address, value, gas,
			gas_used, input, output, error, reverted, canonical
		) VALUES (
			1, $1::numeric, $2, $3, 0, '1', 1, 'call', $4, $5, 0, 100000,
			50000, NULL, $6, 'execution reverted', TRUE, TRUE
		)`, fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, transaction.Hash),
		mustBytes(t, caller), mustBytes(t, target), builtin)
}

func abiIntegrationJob(t *testing.T, block store.BlockRef) enrich.Job {
	t.Helper()
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	return enrich.Job{
		ID: "integration-abi-1-" + block.Hash.String(), Stage: enrich.ABIStage,
		ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	}
}

func assertABIBinding(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	target ethrpc.Address,
	codeHash ethrpc.Hash,
	source, confidence string,
	sourceAddress ethrpc.Address,
	sourceCodeHash ethrpc.Hash,
) {
	t.Helper()
	var gotConfidence, from, to string
	var gotSourceAddress, gotSourceCodeHash, gotBlockHash []byte
	var canonical bool
	err := db.QueryRowContext(ctx, `
		SELECT confidence, valid_from_block::text, coalesce(valid_to_block::text, ''),
		       source_address, source_code_hash, block_hash, canonical
		FROM contract_abis
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2 AND source = $3`,
		mustBytes(t, target), mustBytes(t, codeHash), source).Scan(
		&gotConfidence, &from, &to, &gotSourceAddress, &gotSourceCodeHash, &gotBlockHash, &canonical,
	)
	if err != nil {
		t.Fatalf("query ABI binding %s/%s: %v", target, source, err)
	}
	if gotConfidence != confidence || from != "1" || to != "" || !canonical ||
		hex.EncodeToString(gotSourceAddress) != hex.EncodeToString(mustBytes(t, sourceAddress)) ||
		hex.EncodeToString(gotSourceCodeHash) != hex.EncodeToString(mustBytes(t, sourceCodeHash)) ||
		hex.EncodeToString(gotBlockHash) != hex.EncodeToString(mustBytes(t, block.Hash)) {
		t.Fatalf("ABI binding source=%s confidence=%s range=[%s,%s] canonical=%t source_address=%x source_code=%x block=%x",
			source, gotConfidence, from, to, canonical, gotSourceAddress, gotSourceCodeHash, gotBlockHash)
	}
}

func assertABIDecodingSources(t *testing.T, ctx context.Context, db *sql.DB, block store.BlockRef, want map[string]string) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT object_kind, object_index, source, confidence, status
		FROM abi_decodings
		WHERE chain_id = 1 AND block_hash = $1 AND canonical
		ORDER BY object_kind, object_index`, mustBytes(t, block.Hash))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]string)
	for rows.Next() {
		var kind, index, source, confidence, status string
		if err := rows.Scan(&kind, &index, &source, &confidence, &status); err != nil {
			t.Fatal(err)
		}
		if status != "decoded" {
			t.Fatalf("ABI decoding %s:%s status=%s", kind, index, status)
		}
		got[kind+":"+index] = source + ":" + confidence
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ABI decoding sources=%v want=%v", got, want)
	}
}

func assertSignatureGuessCannotBeVerified(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	target ethrpc.Address,
	codeHash ethrpc.Hash,
) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO contract_abis (
			chain_id, address, code_hash, source, confidence, abi,
			valid_from_block, valid_to_block, block_number, block_hash,
			source_address, source_code_hash, canonical
		) VALUES (
			1, $1, $2, 'signature_database', 'verified', '[]'::jsonb,
			0, NULL, $3::numeric, $4, $1, $2, TRUE
		)`, mustBytes(t, target), mustBytes(t, codeHash), fmt.Sprint(block.Number), mustBytes(t, block.Hash))
	if err == nil {
		t.Fatal("database accepted signature_database ABI with verified confidence")
	}
}

func abiTransferCalldata(t *testing.T, recipient ethrpc.Address, amount uint64) []byte {
	t.Helper()
	selector := enrich.SignatureSelector("transfer(address,uint256)")
	result := append([]byte(nil), selector[:]...)
	result = append(result, abiAddressWord(t, recipient)...)
	return append(result, abiUintWord(amount)...)
}

func abiAddressWord(t *testing.T, address ethrpc.Address) []byte {
	t.Helper()
	result := make([]byte, 32)
	copy(result[12:], mustBytes(t, address))
	return result
}

func abiUintWord(value uint64) []byte {
	result := make([]byte, 32)
	for index := 0; index < 8; index++ {
		result[31-index] = byte(value)
		value >>= 8
	}
	return result
}

func mustRPCWord(t *testing.T, value []byte) ethrpc.Hash {
	t.Helper()
	word, err := ethrpc.ParseHash("0x" + hex.EncodeToString(value))
	if err != nil {
		t.Fatal(err)
	}
	return word
}
