//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/query"
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
	proxyRuntime, implementationRuntime := []byte{0x60, 0x00}, []byte{0x60, 0x01}
	directRuntime := []byte{0x60, 0x02}
	directCode := crypto.Keccak256Hash(directRuntime)
	proxyCode := crypto.Keccak256Hash(proxyRuntime)
	implementationCode := crypto.Keccak256Hash(implementationRuntime)
	insertABICodeObservation(t, ctx, db, reference, direct, directCode)
	insertABICodeObservation(t, ctx, db, reference, proxy, proxyCode)
	insertABIProxyObservation(t, ctx, db, reference, proxy, proxyCode, implementation, implementationCode)
	insertABISignatureCandidates(t, ctx, db)
	publishABIStateDiff(t, ctx, db, reference, map[common.Address][]byte{
		direct: directRuntime, proxy: proxyRuntime,
	})
	publishABITrace(t, ctx, db, reference, block, proxy, recipient, caller)
	transactionReader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	beforeVerification, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatalf("list transactions before verification: %v", err)
	}
	if len(beforeVerification) == 0 || beforeVerification[0].Method == nil ||
		*beforeVerification[0].Method != "0xa9059cbb" || beforeVerification[0].MethodSignature != nil {
		t.Fatalf("pre-verification method projection = %+v", beforeVerification)
	}
	insertABIVerifiedContract(t, ctx, db, direct, directCode)
	insertABIVerifiedContract(t, ctx, db, implementation, implementationCode)
	afterVerification, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatalf("list transactions after verification without ABI replay: %v", err)
	}
	if len(afterVerification) == 0 || afterVerification[0].Method == nil ||
		*afterVerification[0].Method != "transfer" || afterVerification[0].MethodSignature == nil ||
		*afterVerification[0].MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("post-verification selector method projection = %+v", afterVerification)
	}
	txHash := block.Block.Transactions()[0].Hash()
	execFixture(t, ctx, db, `
		DELETE FROM transaction_execution_code_resolutions
		WHERE chain_id = 1 AND block_hash = $1 AND transaction_hash = $2
		  AND context_address = $3`, mustBytes(t, reference.Hash), txHash[:], mustBytes(t, direct))
	withoutExecutionIdentity, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatalf("list transactions with state-diff-omitted direct target: %v", err)
	}
	if len(withoutExecutionIdentity) == 0 || withoutExecutionIdentity[0].Method == nil ||
		*withoutExecutionIdentity[0].Method != "transfer" ||
		withoutExecutionIdentity[0].MethodSignature == nil ||
		*withoutExecutionIdentity[0].MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("verified address-range selector method projection = %+v", withoutExecutionIdentity)
	}
	addressTransactions, _, err := transactionReader.AddressTransactions(ctx, direct.String(), "", 10)
	if err != nil {
		t.Fatalf("list address transactions from verified selector index: %v", err)
	}
	if len(addressTransactions) == 0 || addressTransactions[0].Method == nil ||
		*addressTransactions[0].Method != "transfer" || addressTransactions[0].MethodSignature == nil ||
		*addressTransactions[0].MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("address verified selector method projection = %+v", addressTransactions)
	}
	catalogReader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	calldata, err := catalogReader.TransactionCalldata(ctx, "1", txHash.String())
	if err != nil {
		t.Fatalf("decode verified address-range calldata without execution identity: %v", err)
	}
	if calldata.Execution.Resolution != "unavailable" || calldata.Decoding.Status != "decoded" ||
		calldata.Decoding.FunctionName != "transfer" || calldata.Decoding.Signature != "transfer(address,uint256)" ||
		len(calldata.Decoding.Inputs) != 2 {
		t.Fatalf("verified address-range calldata projection = %+v", calldata)
	}
	execFixture(t, ctx, db, `
		INSERT INTO transaction_execution_code_resolutions (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			context_address, execution_address, execution_code_hash, resolution,
			evidence_source, canonical
		) VALUES (1, $1::numeric, $2, $3, 0, $4, $4, $5, 'direct', 'prestate_tracer', TRUE)`,
		fmt.Sprint(reference.Number), mustBytes(t, reference.Hash), txHash[:], mustBytes(t, direct), directCode[:])

	job := abiIntegrationJob(t, reference)
	result, err := processor.Process(ctx, job)
	if err != nil {
		t.Fatalf("process ABI stage before proxy publication: %v", err)
	}
	if result.State != enrich.ResultComplete || result.Details["bindings"] != "3" {
		t.Fatalf("ABI stage accepted unpublished proxy evidence: %+v", result)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1
		  AND address = $2 AND source = 'proxy_implementation'`,
		0, mustBytes(t, reference.Hash), mustBytes(t, proxy))
	publishABIGenericProxy(
		t, ctx, db, reference, proxy, proxyRuntime, implementation, implementationRuntime,
	)
	for attempt := range 2 {
		result, err := processor.Process(ctx, job)
		if err != nil {
			t.Fatalf("process ABI stage attempt %d: %v", attempt+1, err)
		}
		if result.State != enrich.ResultComplete || result.Details["decoded"] != "6" || result.Details["bindings"] != "4" {
			t.Fatalf("ABI stage attempt %d result=%+v", attempt+1, result)
		}
	}

	assertABIBinding(t, ctx, db, reference, direct, directCode, "verified", "verified", direct, directCode)
	assertABIBinding(t, ctx, db, reference, direct, directCode, "signature_database", "guess", direct, directCode)
	assertABIBinding(t, ctx, db, reference, proxy, proxyCode, "proxy_implementation", "high", implementation, implementationCode)
	assertABIBinding(t, ctx, db, reference, proxy, proxyCode, "signature_database", "guess", proxy, proxyCode)
	assertABIDecodingSources(t, ctx, db, reference, map[string]string{
		"transaction_calldata:": "decoded:verified:verified",
		"log:0":                 "decoded:proxy_implementation:high",
		"trace_calldata:":       "decoded:verified:verified",
		"trace_calldata:0":      "decoded:proxy_implementation:high",
		"trace_calldata:1":      "unknown::",
		"trace_revert:0":        "decoded:proxy_implementation:high",
		"trace_revert:1":        "decoded:builtin:high",
	})
	unpublished, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatalf("list transactions before ABI publication: %v", err)
	}
	if len(unpublished) == 0 || unpublished[0].Method == nil || *unpublished[0].Method != "transfer" ||
		unpublished[0].MethodSignature == nil || *unpublished[0].MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("verified selector method projection before abi@3 publication = %+v", unpublished)
	}
	txHashText := txHash.String()
	assertProjectedLog := func(label string) {
		t.Helper()
		page, readErr := catalogReader.TransactionLogs(ctx, catalog.TransactionResourceRequest{
			ChainID: "1", TransactionHash: txHashText, Limit: 10,
		})
		if readErr != nil {
			t.Fatalf("%s transaction logs: %v", label, readErr)
		}
		if len(page.Items) != 1 || page.Items[0].Decoding.Status != "decoded" ||
			page.Items[0].Decoding.Signature != "Transfer(address,address,uint256)" ||
			page.Items[0].Decoding.Confidence != "high" ||
			page.Items[0].Decoding.ABISource == nil ||
			page.Items[0].Decoding.ABISource.Kind != "proxy_implementation" ||
			len(page.Items[0].Decoding.Arguments) != 3 {
			t.Fatalf("%s projected log=%+v", label, page)
		}
	}
	assertProjectedLog("persisted")
	execFixture(t, ctx, db, `
		DELETE FROM abi_decodings
		WHERE chain_id = 1 AND block_hash = $1 AND object_kind = 'log'`, mustBytes(t, reference.Hash))
	execFixture(t, ctx, db, `
		DELETE FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND source = 'proxy_implementation'`, mustBytes(t, reference.Hash), mustBytes(t, proxy))
	assertProjectedLog("read-time historical proxy fallback")
	if _, err := processor.Process(ctx, job); err != nil {
		t.Fatalf("restore ABI stage after read-time projection check: %v", err)
	}
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
		WHERE chain_id = 1 AND block_hash = $1 AND stage = 'abi@3' AND canonical`, 1, mustBytes(t, reference.Hash))

	assertSignatureGuessCannotBeVerified(t, ctx, db, reference, direct, directCode)
	publishABIStage(t, ctx, db, processor, reference)
	transactions, _, err := transactionReader.Transactions(ctx, "", 10)
	if err != nil {
		t.Fatalf("list transactions with published ABI method: %v", err)
	}
	if len(transactions) == 0 || transactions[0].Hash != strings.ToLower(block.Block.Transactions()[0].Hash().Hex()) ||
		transactions[0].Method == nil || *transactions[0].Method != "transfer" ||
		transactions[0].MethodSignature == nil || *transactions[0].MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("published transaction method projection = %+v", transactions)
	}

	replacement := testBundle(1, testHash(80_001), testHash(70_000), testHash(81_001), "abi-replacement")
	ancestor := mustBlockRef(t, genesis)
	replacementRef := mustBlockRef(t, replacement)
	if err := repository.ApplyReorg(ctx, "1", store.Reorg{
		Ancestor: ancestor, Detached: []store.BlockRef{reference}, Attached: []chainbundle.Bundle{replacement},
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
			map[string]int{"contract_abis": 4, "abi_decodings": 7}[table], mustBytes(t, reference.Hash),
		)
	}
}

func publishABIStage(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	processor *enrich.PostgresABIProcessor,
	block store.BlockRef,
) {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ABIStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "abi-stage-publication", LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
}

func TestABILateSameAddressVerificationDecodesEarlierSameCodeLog(t *testing.T) {
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

	genesis := testBundle(0, testHash(82_000), testHash(0), testHash(82_001), "abi-late-verification-genesis")
	commitCanonical(t, ctx, repository, genesis)
	target, transactionTarget := testAddress(820), testAddress(821)
	recipient, caller := testAddress(822), testAddress(823)
	blockOne := abiFixtureBundleAt(
		t, 1, genesis.Block.Hash(), transactionTarget, target, recipient, caller,
		"abi-late-verification-one",
	)
	commitCanonical(t, ctx, repository, blockOne)
	blockTwo := abiFixtureBundleAt(
		t, 2, blockOne.Block.Hash(), transactionTarget, target, testAddress(824), testAddress(825),
		"abi-late-verification-two",
	)
	commitCanonical(t, ctx, repository, blockTwo)
	refOne, refTwo := mustBlockRef(t, blockOne), mustBlockRef(t, blockTwo)
	targetCode := testHash(82_002)
	insertABICodeObservation(t, ctx, db, refOne, target, targetCode)
	insertABICodeObservation(t, ctx, db, refTwo, target, targetCode)
	insertVerifiedContractFixture(
		t, ctx, db, mustBytes(t, target), mustBytes(t, targetCode), refTwo.Number, nil,
		"0.8.30", "Token", integrationTokenABI, `{}`, `{}`,
	)

	result, err := processor.Process(ctx, abiIntegrationJob(t, refOne))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != enrich.ResultComplete || result.Details["bindings"] != "1" || result.Details["decoded"] != "1" {
		t.Fatalf("late-verification ABI stage result = %+v", result)
	}
	assertABIBinding(t, ctx, db, refOne, target, targetCode, "code_hash", "high", target, targetCode)

	catalogReader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	transactionHash := blockOne.Block.Transactions()[0].Hash().String()
	assertDecoded := func(label string) {
		t.Helper()
		page, readErr := catalogReader.TransactionLogs(ctx, catalog.TransactionResourceRequest{
			ChainID: "1", TransactionHash: transactionHash, Limit: 10,
		})
		if readErr != nil {
			t.Fatalf("%s transaction logs: %v", label, readErr)
		}
		if len(page.Items) != 1 || page.Items[0].Decoding.Status != "decoded" ||
			page.Items[0].Decoding.Signature != "Transfer(address,address,uint256)" ||
			page.Items[0].Decoding.Confidence != "high" ||
			page.Items[0].Decoding.ABISource == nil ||
			page.Items[0].Decoding.ABISource.Kind != "code_hash" ||
			!strings.EqualFold(page.Items[0].Decoding.ABISource.Address, target.String()) {
			t.Fatalf("%s projected log=%+v", label, page)
		}
	}
	assertDecoded("persisted same-code projection")
	execFixture(t, ctx, db, `
		DELETE FROM abi_decodings
		WHERE chain_id = 1 AND block_hash = $1 AND object_kind = 'log'`, mustBytes(t, refOne.Hash))
	execFixture(t, ctx, db, `
		DELETE FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2`,
		mustBytes(t, refOne.Hash), mustBytes(t, target))
	assertDecoded("read-time same-code projection")
}

func TestABIStageSelectsNumericCodeAndProxyObservationHeights(t *testing.T) {
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

	direct, proxy := testAddress(920), testAddress(921)
	recipient, caller := testAddress(922), testAddress(923)
	block99 := abiFixtureBundleAt(
		t, 99, testHash(0), direct, proxy, recipient, caller, "abi-height-99",
	)
	commitCanonical(t, ctx, repository, block99)
	block100 := abiFixtureBundleAt(t, 100, block99.Block.Hash(), direct, proxy, recipient, caller, "abi-height-100")
	commitCanonical(t, ctx, repository, block100)
	ref99, ref100 := mustBlockRef(t, block99), mustBlockRef(t, block100)

	oldDirectRuntime, currentDirectRuntime := []byte{0x60, 0x31}, []byte{0x60, 0x32}
	oldDirectCode, currentDirectCode := crypto.Keccak256Hash(oldDirectRuntime), crypto.Keccak256Hash(currentDirectRuntime)
	proxyRuntime := []byte{0x60, 0x20}
	proxyCode := crypto.Keccak256Hash(proxyRuntime)
	oldImplementation, currentImplementation := testAddress(924), testAddress(925)
	oldImplementationRuntime, currentImplementationRuntime := []byte{0x60, 0x21}, []byte{0x60, 0x22}
	currentImplementationCode := crypto.Keccak256Hash(currentImplementationRuntime)
	insertABICodeObservation(t, ctx, db, ref99, direct, oldDirectCode)
	insertABICodeObservation(t, ctx, db, ref100, direct, currentDirectCode)
	publishABIGenericProxy(
		t, ctx, db, ref99, proxy, proxyRuntime, oldImplementation, oldImplementationRuntime,
	)
	publishABIGenericProxy(
		t, ctx, db, ref100, proxy, proxyRuntime, currentImplementation, currentImplementationRuntime,
	)
	insertABIVerifiedContract(t, ctx, db, direct, currentDirectCode)
	insertABIVerifiedContract(t, ctx, db, currentImplementation, currentImplementationCode)
	publishABIStateDiff(t, ctx, db, ref100, map[common.Address][]byte{direct: currentDirectRuntime})

	result, err := processor.Process(ctx, abiIntegrationJob(t, ref100))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != enrich.ResultComplete {
		t.Fatalf("ABI stage result = %+v", result)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND code_hash = $3 AND source = 'verified'
		  AND valid_from_block = 100`,
		1, mustBytes(t, ref100.Hash), mustBytes(t, direct), mustBytes(t, currentDirectCode))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND code_hash = $3 AND source = 'proxy_implementation'
		  AND source_address = $4 AND source_code_hash = $5
		  AND valid_from_block = 100 AND valid_to_block = 100`,
		1, mustBytes(t, ref100.Hash), mustBytes(t, proxy), mustBytes(t, proxyCode),
		mustBytes(t, currentImplementation), mustBytes(t, currentImplementationCode))
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1
		  AND (code_hash = $2 OR source_address = $3)`,
		0, mustBytes(t, ref100.Hash), mustBytes(t, oldDirectCode), mustBytes(t, oldImplementation))
}

func TestABIStageBeaconUsesLatestCanonicalPublishedSharedImplementation(t *testing.T) {
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

	genesis := testBundle(0, testHash(93_000), testHash(0), testHash(93_100), "abi-beacon-genesis")
	commitCanonical(t, ctx, repository, genesis)
	targetProxy, siblingProxy, beacon := testAddress(930), testAddress(931), testAddress(932)
	implementationA, implementationB, unpublishedImplementationC := testAddress(933), testAddress(934), testAddress(935)
	directOne, directTwo, directThree := testAddress(936), testAddress(937), testAddress(938)
	recipient, caller := testAddress(939), testAddress(940)
	blockOne := abiFixtureBundleAt(
		t, 1, genesis.Block.Hash(), directOne, targetProxy, recipient, caller, "abi-beacon-a",
	)
	blockTwo := abiFixtureBundleAt(
		t, 2, blockOne.Block.Hash(), directTwo, siblingProxy, recipient, caller, "abi-beacon-b",
	)
	blockThree := abiFixtureBundleAt(
		t, 3, blockTwo.Block.Hash(), directThree, targetProxy, recipient, caller, "abi-beacon-read",
	)
	commitCanonical(t, ctx, repository, blockOne)
	commitCanonical(t, ctx, repository, blockTwo)
	commitCanonical(t, ctx, repository, blockThree)
	refOne, refTwo, refThree := mustBlockRef(t, blockOne), mustBlockRef(t, blockTwo), mustBlockRef(t, blockThree)

	targetRuntime, siblingRuntime := []byte{0x60, 0x31}, []byte{0x60, 0x32}
	beaconRuntime := []byte{0x60, 0x33}
	implementationRuntimeA, implementationRuntimeB := []byte{0x60, 0x34}, []byte{0x60, 0x35}
	unpublishedImplementationRuntimeC := []byte{0x60, 0x36}
	targetCode := crypto.Keccak256Hash(targetRuntime)
	siblingCode := crypto.Keccak256Hash(siblingRuntime)
	beaconCode := crypto.Keccak256Hash(beaconRuntime)
	implementationCodeA := crypto.Keccak256Hash(implementationRuntimeA)
	implementationCodeB := crypto.Keccak256Hash(implementationRuntimeB)
	unpublishedImplementationCodeC := crypto.Keccak256Hash(unpublishedImplementationRuntimeC)

	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, refOne, generation, compilerDigest, executorDigest,
		targetProxy, targetCode, targetRuntime, "beacon_proxy", &beacon,
	)
	insertAuthenticatedProxyArtifactFixture(
		t, ctx, db, refTwo, generation, compilerDigest, executorDigest,
		siblingProxy, siblingCode, siblingRuntime, "beacon_proxy", &beacon,
	)
	insertABIVerifiedContract(t, ctx, db, implementationA, implementationCodeA)
	insertABIVerifiedContract(t, ctx, db, implementationB, implementationCodeB)

	publishABIProxyStage(t, ctx, db, refOne, map[string]proxyContractState{
		targetProxy.String(): {code: targetRuntime, beacon: &beacon},
		beacon.String(): {
			code: beaconRuntime, beaconImplementation: &implementationA,
		},
		implementationA.String(): {code: implementationRuntimeA},
	})
	publishABIProxyStage(t, ctx, db, refTwo, map[string]proxyContractState{
		siblingProxy.String(): {code: siblingRuntime, beacon: &beacon},
		beacon.String(): {
			code: beaconRuntime, beaconImplementation: &implementationB,
		},
		implementationB.String(): {code: implementationRuntimeB},
	})
	// A canonical raw observation without its generation publication must not
	// hide the latest published Beacon implementation (B) or revive A from the
	// proxy's older artifact resolution.
	execFixture(t, ctx, db, `
		INSERT INTO beacon_implementation_observations (
			chain_id, beacon_address, block_number, block_hash,
			beacon_code_hash, implementation_address,
			implementation_code_hash, stage_version, confidence, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, $6, 2, 'high', TRUE)`,
		mustBytes(t, beacon), fmt.Sprint(refThree.Number), mustBytes(t, refThree.Hash),
		mustBytes(t, beaconCode), mustBytes(t, unpublishedImplementationC),
		mustBytes(t, unpublishedImplementationCodeC))

	result, err := processor.Process(ctx, abiIntegrationJob(t, refThree))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != enrich.ResultComplete || result.Details["bindings"] != "2" || result.Details["decoded"] != "1" {
		t.Fatalf("ABI Beacon stage result = %+v", result)
	}
	assertABIBinding(
		t, ctx, db, refThree, targetProxy, targetCode,
		"proxy_implementation", "high", implementationB, implementationCodeB,
	)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_artifact_resolutions
		WHERE chain_id = 1 AND proxy_address = $1
		  AND observation_block_hash = $2
		  AND proxy_pattern = 'beacon'
		  AND implementation_address = $3`, 1,
		mustBytes(t, targetProxy), mustBytes(t, refOne.Hash), mustBytes(t, implementationA))
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1
		  AND address = $2 AND source = 'proxy_implementation'
		  AND source_address IN ($3, $4)`, 0,
		mustBytes(t, refThree.Hash), mustBytes(t, targetProxy),
		mustBytes(t, implementationA), mustBytes(t, unpublishedImplementationC))
}

func abiFixtureBundle(t *testing.T, direct, proxy, recipient, caller common.Address) chainbundle.Bundle {
	t.Helper()
	return abiFixtureBundleAt(t, 1, testHash(70_000), direct, proxy, recipient, caller, "abi-block")
}

func abiFixtureBundleAt(
	t *testing.T,
	number uint64,
	parent common.Hash,
	direct, proxy, recipient, caller common.Address,
	variant string,
) chainbundle.Bundle {
	t.Helper()
	transferTopic := enrich.SignatureHash("Transfer(address,address,uint256)")
	block, err := newIntegrationBundle(integrationBundleOptions{
		Number:     number,
		ParentHash: parent,
		ExtraData:  []byte(variant),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType,
			To:   &direct,
			Data: abiTransferCalldata(t, recipient, 17),
			Logs: []*types.Log{{
				Address: proxy,
				Topics: []common.Hash{
					mustRPCWord(t, transferTopic[:]),
					mustRPCWord(t, abiAddressWord(t, caller)),
					mustRPCWord(t, abiAddressWord(t, recipient)),
				},
				Data: abiUintWord(17),
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": variant},
	})
	if err != nil {
		t.Fatalf("build ABI fixture bundle: %v", err)
	}
	return block
}

func insertABICodeObservation(t *testing.T, ctx context.Context, db *sql.DB, block store.BlockRef, address common.Address, codeHash common.Hash) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)`,
		mustBytes(t, address), fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, codeHash), []byte{0x60, 0x00})
}

func insertABIVerifiedContract(t *testing.T, ctx context.Context, db *sql.DB, address common.Address, codeHash common.Hash) {
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
	proxy common.Address,
	proxyCode common.Hash,
	implementation common.Address,
	implementationCode common.Hash,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, stage_version, proxy_code_hash,
			proxy_kind, implementation_address, implementation_code_hash,
			confidence, canonical
		) VALUES (1, $1, $2::numeric, $3, 2, $4, 'eip1967', $5, $6, 'high', TRUE)`,
		mustBytes(t, proxy), fmt.Sprint(block.Number), mustBytes(t, block.Hash), mustBytes(t, proxyCode),
		mustBytes(t, implementation), mustBytes(t, implementationCode))
}

func publishABIGenericProxy(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy common.Address,
	proxyRuntime []byte,
	implementation common.Address,
	implementationRuntime []byte,
) {
	t.Helper()
	publishABIProxyStage(t, ctx, db, block, map[string]proxyContractState{
		proxy.String(): {
			code:           common.CopyBytes(proxyRuntime),
			implementation: &implementation,
		},
		implementation.String(): {code: common.CopyBytes(implementationRuntime)},
	})
}

func publishABIProxyStage(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	states map[string]proxyContractState,
) {
	t.Helper()
	result, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical'
		  AND message_key = $1`, block.Hash.String())
	if err != nil {
		t.Fatalf("publish ABI proxy core outbox: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("publish ABI proxy core outbox rows=%d error=%v", affected, err)
	}
	var callMu sync.Mutex
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(
			t, "abi-proxy", map[string]map[string]proxyContractState{
				block.Hash.String(): states,
			}, nil, &callMu, make(map[string][]string),
		),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT source_verification_job_id::text
		FROM proxy_replay_targets
		WHERE chain_id = 1 AND block_number = $1::numeric AND block_hash = $2
		ORDER BY source_verification_job_id::text`, block.Number, block.Hash.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var enqueued enrich.EnqueueResult
	for rows.Next() {
		var sourceJobID string
		if err := rows.Scan(&sourceJobID); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		enqueued, err = queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
			Replay: enrich.ReplaySource{Kind: "verification-publication", Key: sourceJobID},
		})
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if enqueued.Job.ID == "" {
		enqueued, err = queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: enrich.ProxyStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "abi-proxy-publication", LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM published_block_stage_results
		WHERE durable_job_id = $1 AND stage = 'proxy'
		  AND stage_version = 2 AND state = 'complete'`, 1, enqueued.Job.ID)
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

func publishABITrace(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	bundle chainbundle.Bundle,
	target, recipient, caller common.Address,
) {
	t.Helper()
	transaction := bundle.Block.Transactions()[0]
	if transaction.To() == nil {
		t.Fatal("ABI trace fixture requires a call transaction")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		t.Fatalf("recover ABI trace sender: %v", err)
	}
	selector := enrich.SignatureSelector("Unauthorized(address)")
	revert := append(append([]byte(nil), selector[:]...), abiAddressWord(t, caller)...)
	builtinSelector := enrich.SignatureSelector("Error(string)")
	builtin := append([]byte(nil), builtinSelector[:]...)
	builtin = append(builtin, abiUintWord(32)...)
	builtin = append(builtin, abiUintWord(4)...)
	builtin = append(builtin, append([]byte("nope"), make([]byte, 28)...)...)
	raw, err := json.Marshal(map[string]any{
		"type": "CALL", "from": sender.String(), "to": transaction.To().String(),
		"value": fmt.Sprintf("0x%x", transaction.Value()), "gas": "0x5208", "gasUsed": "0x5000",
		"input": fmt.Sprintf("0x%x", transaction.Data()), "output": "0x",
		"calls": []any{
			map[string]any{
				"type": "CALL", "from": transaction.To().String(), "to": target.String(),
				"value": "0x0", "gas": "0x186a0", "gasUsed": "0xc350",
				"input":  fmt.Sprintf("0x%x", abiTransferCalldata(t, recipient, 19)),
				"output": fmt.Sprintf("0x%x", revert), "error": "execution reverted",
			},
			map[string]any{
				"type": "CALL", "from": transaction.To().String(), "to": target.String(),
				"value": "0x0", "gas": "0x186a0", "gasUsed": "0xc350",
				"input": "0x", "output": fmt.Sprintf("0x%x", builtin), "error": "execution reverted",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &traceStageService{raw: raw}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "abi-trace", Client: newIntegrationRPCClient(t, "debug", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewTraceRPCProcessor(db, pool, enrich.TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical'
		  AND message_key = $1`, block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("publish ABI trace core outbox rows=%d error=%v", affected, err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TraceStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "abi-trace-publication", LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
}

type abiStateDiffService struct{ raw json.RawMessage }

func (service *abiStateDiffService) TraceTransaction(
	context.Context, common.Hash, map[string]any,
) (json.RawMessage, error) {
	return append(json.RawMessage(nil), service.raw...), nil
}

func publishABIStateDiff(
	t *testing.T, ctx context.Context, db *sql.DB, block store.BlockRef,
	code map[common.Address][]byte,
) {
	t.Helper()
	pre := make(map[string]any, len(code))
	post := make(map[string]any, len(code))
	for address, runtime := range code {
		pre[address.Hex()] = map[string]any{"nonce": "0x0", "code": "0x" + hex.EncodeToString(runtime)}
		post[address.Hex()] = map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"pre": pre, "post": post})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "abi-state-diff", Client: newIntegrationRPCClient(t, "debug", &abiStateDiffService{raw: raw}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: ethrpc.AvailabilityAvailable,
		}},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewStateDiffRPCProcessor(db, pool, enrich.StateDiffLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	word, err := enrich.ParseWord(block.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.StateDiffStage, ChainID: "1", BlockHash: word, BlockNumber: block.Number,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "abi-state-diff-publication", LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	processOne(t, ctx, worker)
	assertJobStatus(t, ctx, db, enqueued.Job.ID, "succeeded")
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
	target common.Address,
	codeHash common.Hash,
	source, confidence string,
	sourceAddress common.Address,
	sourceCodeHash common.Hash,
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
	wantFrom, wantTo := "1", ""
	if source == "proxy_implementation" {
		wantFrom = fmt.Sprint(block.Number)
		wantTo = wantFrom
	}
	if gotConfidence != confidence || from != wantFrom || to != wantTo || !canonical ||
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
	defer rows.Close() //nolint:errcheck
	got := make(map[string]string)
	for rows.Next() {
		var kind, index, status string
		var source, confidence sql.NullString
		if err := rows.Scan(&kind, &index, &source, &confidence, &status); err != nil {
			t.Fatal(err)
		}
		got[kind+":"+index] = status + ":" + source.String + ":" + confidence.String
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
	target common.Address,
	codeHash common.Hash,
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

func abiTransferCalldata(t *testing.T, recipient common.Address, amount uint64) []byte {
	t.Helper()
	selector := enrich.SignatureSelector("transfer(address,uint256)")
	result := append([]byte(nil), selector[:]...)
	result = append(result, abiAddressWord(t, recipient)...)
	return append(result, abiUintWord(amount)...)
}

func abiAddressWord(t *testing.T, address common.Address) []byte {
	t.Helper()
	result := make([]byte, 32)
	copy(result[12:], mustBytes(t, address))
	return result
}

func abiUintWord(value uint64) []byte {
	result := make([]byte, 32)
	for index := range 8 {
		result[31-index] = byte(value)
		value >>= 8
	}
	return result
}

func mustRPCWord(t *testing.T, value []byte) common.Hash {
	t.Helper()
	if len(value) != common.HashLength {
		t.Fatalf("RPC word length=%d, want %d (%s)", len(value), common.HashLength, hex.EncodeToString(value))
	}
	return common.BytesToHash(value)
}
