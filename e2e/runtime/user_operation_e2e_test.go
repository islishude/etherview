//go:build runtimee2e

package runtimee2e

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
)

const runtimeEntryPointABI = `[{"type":"function","name":"handleOps","inputs":[{"name":"ops","type":"tuple[]","components":[{"name":"sender","type":"address"},{"name":"nonce","type":"uint256"},{"name":"initCode","type":"bytes"},{"name":"callData","type":"bytes"},{"name":"accountGasLimits","type":"bytes32"},{"name":"preVerificationGas","type":"uint256"},{"name":"gasFees","type":"bytes32"},{"name":"paymasterAndData","type":"bytes"},{"name":"signature","type":"bytes"}]},{"name":"beneficiary","type":"address"}],"outputs":[]}]`

//go:embed user_operation_fixture_runtime.hex
var runtimeUserOperationFixtureBytecode string

type runtimePackedUserOperation struct {
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

func (h *harness) configureUserOperationEnvironment() {
	h.t.Helper()
	entryPoints, err := json.Marshal([]map[string]any{{
		"address": h.fixture.entryPoint, "version": "0.9", "from_block": 0,
	}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.project.Env["ETHERVIEW_RUNTIME_ERC4337_ENTRY_POINTS"] = string(entryPoints)
}

func (h *harness) sendUserOperationEntryPointDeployment(ctx context.Context) {
	h.t.Helper()
	var deployerNonce hexutil.Uint64
	h.rpcCall(ctx, &deployerNonce, "eth_getTransactionCount", h.fixture.accounts[0], "pending")
	h.fixture.entryPoint = crypto.CreateAddress(
		common.HexToAddress(h.fixture.accounts[0]),
		uint64(deployerNonce),
	).Hex()
	h.fixture.entryPointCreationHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "data": h.runtimeUserOperationCreationBytecode(),
		"gas": "0x1e8480", "gasPrice": "0x3b9aca00",
	})
}

func (h *harness) sendUserOperationBundle(ctx context.Context) {
	h.t.Helper()
	h.fixture.userOperationTxHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "to": h.fixture.entryPoint,
		"data": h.runtimeUserOperationCalldata(), "gas": "0x1e8480", "gasPrice": "0x3b9aca00",
	})
}

func (h *harness) captureUserOperationFixture(ctx context.Context) {
	h.t.Helper()
	entryPointReceipt := h.waitReceipt(ctx, h.fixture.entryPointCreationHash)
	if entryPointReceipt.Status != "0x1" || !strings.EqualFold(
		entryPointReceipt.ContractAddress,
		h.fixture.entryPoint,
	) {
		h.t.Fatalf("runtime EntryPoint deployment receipt = %#v", entryPointReceipt)
	}
	var installedCode string
	h.rpcCall(ctx, &installedCode, "eth_getCode", h.fixture.entryPoint, "latest")
	if installedCode == "0x" || !strings.EqualFold(
		installedCode,
		"0x"+strings.TrimSpace(runtimeUserOperationFixtureBytecode),
	) {
		h.t.Fatalf("runtime EntryPoint code was not deployed: %s", installedCode)
	}
	userOperationReceipt := h.waitReceipt(ctx, h.fixture.userOperationTxHash)
	if userOperationReceipt.Status != "0x1" || len(userOperationReceipt.Logs) != 2 {
		h.t.Fatalf("runtime UserOperation receipt = %#v", userOperationReceipt)
	}
	for index, log := range userOperationReceipt.Logs {
		if len(log.Topics) < 2 || !common.IsHexHash(log.Topics[1]) {
			h.t.Fatalf("runtime UserOperation log %d = %#v", index, log)
		}
		if h.fixture.userOperationHash == "" {
			h.fixture.userOperationHash = strings.ToLower(log.Topics[1])
		} else if !strings.EqualFold(h.fixture.userOperationHash, log.Topics[1]) {
			h.t.Fatalf(
				"runtime UserOperation event hash mismatch: %s != %s",
				h.fixture.userOperationHash,
				log.Topics[1],
			)
		}
	}
}

func (h *harness) runtimeUserOperationCreationBytecode() string {
	h.t.Helper()
	runtimeCode, err := hex.DecodeString(strings.TrimSpace(runtimeUserOperationFixtureBytecode))
	if err != nil || len(runtimeCode) == 0 || len(runtimeCode) > 0xffff {
		h.t.Fatalf("decode runtime UserOperation fixture bytecode: bytes=%d err=%v", len(runtimeCode), err)
	}
	lengthHigh, lengthLow := byte(len(runtimeCode)>>8), byte(len(runtimeCode))
	creationCode := []byte{
		0x61, lengthHigh, lengthLow,
		0x60, 0x0e,
		0x60, 0x00,
		0x39,
		0x61, lengthHigh, lengthLow,
		0x60, 0x00,
		0xf3,
	}
	creationCode = append(creationCode, runtimeCode...)
	return hexutil.Encode(creationCode)
}

func (h *harness) runtimeUserOperationCalldata() string {
	h.t.Helper()
	definition, err := abi.JSON(strings.NewReader(runtimeEntryPointABI))
	if err != nil {
		h.t.Fatal(err)
	}
	operation := runtimePackedUserOperation{
		Sender: h.fixture.accountsAddress(2), Nonce: big.NewInt(1),
		CallData: []byte{0xde, 0xad, 0xbe, 0xef}, PreVerificationGas: big.NewInt(30_000),
		Signature: []byte{0x01},
	}
	operation.AccountGasLimits[15], operation.AccountGasLimits[31] = 2, 1
	operation.GasFees[15], operation.GasFees[31] = 1, 2
	calldata, err := definition.Pack(
		"handleOps",
		[]runtimePackedUserOperation{operation},
		h.fixture.accountsAddress(3),
	)
	if err != nil {
		h.t.Fatal(err)
	}
	return hexutil.Encode(calldata)
}

func (f fixture) accountsAddress(index int) common.Address {
	return common.HexToAddress(f.accounts[index])
}

func (h *harness) assertUserOperation(ctx context.Context) {
	h.t.Helper()
	waitFor(h.t, ctx, "canonical ERC-4337 UserOperation publication", func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM published_erc4337_user_operations
			WHERE chain_id = 1 AND user_op_hash = decode($1, 'hex')
		`, strings.TrimPrefix(h.fixture.userOperationHash, "0x")).Scan(&count)
		return err == nil && count == 1, strconv.FormatInt(count, 10), err
	})
	var list gen.UserOperationListResponse
	h.mustGetJSON(ctx, "/api/v1/user-operations?limit=20", &list)
	if len(list.Data) != 1 || !strings.EqualFold(string(list.Data[0].Hash), h.fixture.userOperationHash) ||
		!strings.EqualFold(string(list.Data[0].TransactionHash), h.fixture.userOperationTxHash) ||
		!strings.EqualFold(string(list.Data[0].EntryPoint), h.fixture.entryPoint) ||
		list.Data[0].EntryPointVersion != gen.N09 || list.Data[0].Success || list.Data[0].EventLogIndex != 1 {
		h.t.Fatalf("runtime UserOperation list = %#v", list.Data)
	}
	var detail gen.UserOperationResponse
	h.mustGetJSON(ctx, "/api/v1/user-operations/"+h.fixture.userOperationHash, &detail)
	if detail.Data.Request.CallData != "0xdeadbeef" || len(detail.Data.Events) != 1 ||
		detail.Data.Events[0].Reason == nil || *detail.Data.Events[0].Reason != "runtime fixture revert" ||
		detail.Data.Events[0].Kind != gen.ExecutionRevert {
		h.t.Fatalf("runtime UserOperation detail = %#v", detail.Data)
	}
	var byTransaction gen.UserOperationListResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.userOperationTxHash+"/user-operations?limit=20", &byTransaction)
	var byAddress gen.UserOperationListResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.accounts[2]+"/user-operations?limit=20", &byAddress)
	if len(byTransaction.Data) != 1 || len(byAddress.Data) != 1 {
		h.t.Fatalf("runtime UserOperation projections: transaction=%#v address=%#v", byTransaction.Data, byAddress.Data)
	}
	var search gen.SearchResponse
	h.mustGetJSON(ctx, "/api/v1/search?q="+url.QueryEscape(h.fixture.userOperationHash), &search)
	if len(search.Data) != 1 || search.Data[0].Kind != gen.SearchResultKindUserOperation {
		h.t.Fatalf("runtime UserOperation search = %#v", search.Data)
	}
}
