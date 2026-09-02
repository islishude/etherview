//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/erc4337"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

const integrationEntryPointABI = `[
  {"type":"function","name":"handleOps","inputs":[
    {"name":"ops","type":"tuple[]","components":[
      {"name":"sender","type":"address"},{"name":"nonce","type":"uint256"},
      {"name":"initCode","type":"bytes"},{"name":"callData","type":"bytes"},
      {"name":"accountGasLimits","type":"bytes32"},{"name":"preVerificationGas","type":"uint256"},
      {"name":"gasFees","type":"bytes32"},{"name":"paymasterAndData","type":"bytes"},
      {"name":"signature","type":"bytes"}
    ]},{"name":"beneficiary","type":"address"}
  ],"outputs":[]},
  {"type":"event","name":"AccountDeployed","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"factory","type":"address","indexed":false},
    {"name":"paymaster","type":"address","indexed":false}
  ]},
  {"type":"event","name":"UserOperationEvent","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"paymaster","type":"address","indexed":true},
    {"name":"nonce","type":"uint256","indexed":false},
    {"name":"success","type":"bool","indexed":false},
    {"name":"actualGasCost","type":"uint256","indexed":false},
    {"name":"actualGasUsed","type":"uint256","indexed":false}
  ]}
]`

type integrationPackedUserOperation struct {
	Sender             common.Address
	Nonce              *big.Int
	InitCode           []byte
	CallData           []byte
	AccountGasLimits   [32]byte
	PreVerificationGas *big.Int
	GasFees            [32]byte
	PaymasterAndData   []byte
	Signature          []byte
}

func TestERC4337StagePublishesCanonicalAPIsAndWithdrawsOnReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	entryPoint := common.HexToAddress("0x433709009B8330FDa32311DF1C2AFA402eD8D009")
	sender, beneficiary := testAddress(101), testAddress(102)
	userOpHash := testHash(76_001)
	genesis := testBundle(0, testHash(76_000), testHash(0), testHash(77_000), "userop-genesis")
	userOperationBlock := integrationUserOperationBundle(
		t, 1, genesis.Block.Hash(), []byte("userop-old"), entryPoint, sender, beneficiary, userOpHash,
	)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, userOperationBlock}); err != nil {
		t.Fatalf("commit UserOperation canonical segment: %v", err)
	}
	registry := integrationUserOperationRegistry(t, entryPoint)
	processUserOperationOutbox(t, ctx, db, registry)
	reader, err := query.NewPostgresReader(db, query.Options{
		ChainID: 1, StartBlock: 0,
		OptionalStages:        gen.Completeness{UserOperations: gen.StageStatePending},
		UserOperationRegistry: &registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.UserOperations(ctx, "", 25)
	if err != nil || len(page.Items) != 1 || page.CoverageStart != 1 || page.CoverageEnd != 1 {
		t.Fatalf("UserOperation page=%+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.Hash != strings.ToLower(userOpHash.Hex()) || item.Sender != sender.Hex() ||
		item.EntryPoint != entryPoint.Hex() || item.EntryPointVersion != gen.N09 || !item.Success ||
		item.EventLogIndex != 1 {
		t.Fatalf("UserOperation item=%+v", item)
	}
	detail, err := reader.UserOperation(ctx, userOpHash.Hex())
	if err != nil || detail.Request.CallData != "0xdeadbeef" || detail.ActualGasCost != "1000" ||
		len(detail.Events) != 1 || detail.Events[0].Kind != gen.AccountDeployed {
		t.Fatalf("UserOperation detail=%+v err=%v", detail, err)
	}
	txHash := userOperationBlock.Block.Transactions()[0].Hash()
	txPage, err := reader.TransactionUserOperations(ctx, txHash.Hex(), "", 25)
	if err != nil || len(txPage.Items) != 1 {
		t.Fatalf("transaction UserOperations=%+v err=%v", txPage, err)
	}
	addressPage, err := reader.AddressUserOperations(ctx, sender.Hex(), "", 25)
	if err != nil || len(addressPage.Items) != 1 || addressPage.Items[0].ParticipatingRoles == nil ||
		len(*addressPage.Items[0].ParticipatingRoles) != 1 || (*addressPage.Items[0].ParticipatingRoles)[0] != gen.UserOperationRoleSender {
		t.Fatalf("address UserOperations=%+v err=%v", addressPage, err)
	}
	results, _, err := reader.Search(ctx, userOpHash.Hex(), "", 20)
	if err != nil || !containsSearchKind(results, gen.SearchResultKindUserOperation) {
		t.Fatalf("UserOperation search=%+v err=%v", results, err)
	}
	status, err := reader.Status(ctx)
	if err != nil || status.Completeness.UserOperations != gen.StageStateComplete {
		t.Fatalf("UserOperation status=%+v err=%v", status.Completeness, err)
	}
	replacement := testBundle(1, testHash(86_001), genesis.Block.Hash(), testHash(87_001), "userop-new")
	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{userOperationBlock}, []chainbundle.Bundle{replacement}, "replace UserOperation block")
	processUserOperationOutbox(t, ctx, db, registry)
	page, err = reader.UserOperations(ctx, "", 25)
	if err != nil || len(page.Items) != 0 || page.CoverageEnd != 1 {
		t.Fatalf("replacement UserOperation page=%+v err=%v", page, err)
	}
	if _, err := reader.UserOperation(ctx, userOpHash.Hex()); !errors.Is(err, publicquery.ErrNotFound) {
		t.Fatalf("orphan UserOperation detail error=%v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operations
		WHERE chain_id = 1 AND user_op_hash = $1 AND canonical = FALSE`, 1, userOpHash[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operation_events
		WHERE chain_id = 1 AND transaction_hash = $1 AND canonical = FALSE`, 1, txHash[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operation_participants
		WHERE chain_id = 1 AND transaction_hash = $1 AND canonical = FALSE`, 5, txHash[:])

	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{replacement}, []chainbundle.Bundle{userOperationBlock}, "reattach UserOperation block")
	processUserOperationOutbox(t, ctx, db, registry)
	page, err = reader.UserOperations(ctx, "", 25)
	if err != nil || len(page.Items) != 1 || page.CoverageEnd != 1 || page.Items[0].Hash != strings.ToLower(userOpHash.Hex()) {
		t.Fatalf("reattached UserOperation page=%+v err=%v", page, err)
	}
	if _, err := reader.UserOperation(ctx, userOpHash.Hex()); err != nil {
		t.Fatalf("reattached UserOperation detail: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operations
		WHERE chain_id = 1 AND user_op_hash = $1 AND canonical = TRUE`, 1, userOpHash[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operation_events
		WHERE chain_id = 1 AND transaction_hash = $1 AND canonical = TRUE`, 1, txHash[:])
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc4337_user_operation_participants
		WHERE chain_id = 1 AND transaction_hash = $1 AND canonical = TRUE`, 5, txHash[:])
}

func integrationUserOperationBundle(
	t *testing.T,
	number uint64,
	parent common.Hash,
	extra []byte,
	entryPoint, sender, beneficiary common.Address,
	userOpHash common.Hash,
) chainbundle.Bundle {
	t.Helper()
	definition, err := gethabi.JSON(strings.NewReader(integrationEntryPointABI))
	if err != nil {
		t.Fatal(err)
	}
	operation := integrationPackedUserOperation{
		Sender: sender, Nonce: big.NewInt(1), InitCode: append(testAddress(103).Bytes(), 0xaa),
		CallData:           []byte{0xde, 0xad, 0xbe, 0xef},
		PreVerificationGas: big.NewInt(30_000), Signature: []byte{0x01},
	}
	operation.AccountGasLimits[15], operation.AccountGasLimits[31] = 2, 1
	operation.GasFees[15], operation.GasFees[31] = 1, 2
	calldata, err := definition.Pack("handleOps", []integrationPackedUserOperation{operation}, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	event := definition.Events["UserOperationEvent"]
	data, err := event.Inputs.NonIndexed().Pack(big.NewInt(1), true, big.NewInt(1000), big.NewInt(25_000))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: extra,
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &entryPoint, Data: calldata,
			Logs: integrationUserOperationLogs(t, definition, entryPoint, sender, userOpHash, data),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func integrationUserOperationLogs(
	t *testing.T,
	definition gethabi.ABI,
	entryPoint, sender common.Address,
	userOpHash common.Hash,
	outcomeData []byte,
) []*types.Log {
	t.Helper()
	deployment := definition.Events["AccountDeployed"]
	deploymentData, err := deployment.Inputs.NonIndexed().Pack(testAddress(103), common.Address{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := definition.Events["UserOperationEvent"]
	return []*types.Log{
		{Address: entryPoint, Index: 0, Topics: []common.Hash{
			deployment.ID, userOpHash, common.BytesToHash(sender.Bytes()),
		}, Data: deploymentData},
		{Address: entryPoint, Index: 1, Topics: []common.Hash{
			outcome.ID, userOpHash, common.BytesToHash(sender.Bytes()), {},
		}, Data: outcomeData},
	}
}

func integrationUserOperationRegistry(t *testing.T, entryPoint common.Address) erc4337.Registry {
	t.Helper()
	registry, err := erc4337.NewRegistry(config.ERC4337Config{EntryPoints: []config.ERC4337EntryPointConfig{{
		Address: entryPoint.Hex(), Version: "0.9", FromBlock: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func processUserOperationOutbox(t *testing.T, ctx context.Context, db *sql.DB, registry erc4337.Registry) {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
		Stages: []enrich.StageID{enrich.UserOperationStage},
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		result, dispatchErr := dispatcher.DispatchOne(ctx)
		if dispatchErr != nil {
			t.Fatal(dispatchErr)
		}
		if result.State == enrich.OutboxIdle {
			break
		}
		if result.State == enrich.OutboxRetry {
			t.Fatalf("UserOperation outbox retry: %+v", result)
		}
	}
	processor, err := enrich.NewPostgresUserOperationProcessor(db, registry)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "userop-integration", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for idlePolls := 0; idlePolls < 3; {
		processed, processErr := worker.ProcessOne(ctx)
		if processErr != nil {
			t.Fatal(processErr)
		}
		if !processed {
			idlePolls++
			time.Sleep(10 * time.Millisecond)
			continue
		}
		idlePolls = 0
	}
}

func containsSearchKind(results []gen.SearchResult, kind gen.SearchResultKind) bool {
	for _, result := range results {
		if result.Kind == kind {
			return true
		}
	}
	return false
}
