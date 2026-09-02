package erc4337

import (
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/config"
)

func TestDecodeBlockBindsV06OperationAndDeploymentEvents(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(31337)
	entryPoint := common.HexToAddress("0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789")
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	factory := common.HexToAddress("0x2000000000000000000000000000000000000002")
	paymaster := common.HexToAddress("0x3000000000000000000000000000000000000003")
	beneficiary := common.HexToAddress("0x4000000000000000000000000000000000000004")
	nonce := big.NewInt(9)
	wire := userOperationV06Wire{
		Sender: sender, Nonce: nonce,
		InitCode: append(factory.Bytes(), 0xaa), CallData: []byte{0x01, 0x02},
		CallGasLimit: big.NewInt(100), VerificationGasLimit: big.NewInt(200),
		PreVerificationGas: big.NewInt(30), MaxFeePerGas: big.NewInt(40),
		MaxPriorityFeePerGas: big.NewInt(5),
		PaymasterAndData:     append(paymaster.Bytes(), 0xbb), Signature: []byte{0xcc},
	}
	calldata, err := entryPointV06ABI.Pack("handleOps", []userOperationV06Wire{wire}, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	transaction := signedEntryPointTransaction(t, chainID, entryPoint, calldata)
	userOpHash := common.HexToHash("0x1234")
	logs := []*types.Log{
		accountDeployedLog(t, entryPoint, userOpHash, sender, factory, paymaster, 0),
		outcomeLog(t, entryPoint, userOpHash, sender, paymaster, nonce, true, 77, 55, 1),
	}
	block, receipts := operationBlock(transaction, logs)
	registry := testRegistry(t, entryPoint, Version06)
	operations, err := DecodeBlock(registry, chainID, block, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 {
		t.Fatalf("operations=%d", len(operations))
	}
	operation := operations[0]
	if operation.Hash != userOpHash || operation.Request.Sender != sender ||
		operation.Beneficiary != beneficiary || operation.Request.Factory == nil ||
		*operation.Request.Factory != factory || operation.Request.Paymaster == nil ||
		*operation.Request.Paymaster != paymaster || len(operation.Events) != 1 ||
		operation.Events[0].Kind != EventAccountDeployed || operation.EventLogIndex != 1 {
		t.Fatalf("decoded operation = %#v", operation)
	}
}

func TestDecodeBlockParsesV09PaymasterSignatureAndFailure(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(31337)
	entryPoint := common.HexToAddress("0x433709009B8330FDa32311DF1C2AFA402eD8D009")
	sender := common.HexToAddress("0x5000000000000000000000000000000000000005")
	factory := common.HexToAddress("0x6000000000000000000000000000000000000006")
	paymaster := common.HexToAddress("0x7000000000000000000000000000000000000007")
	beneficiary := common.HexToAddress("0x8000000000000000000000000000000000000008")
	nonce := new(big.Int).SetUint64(17)
	accountGas := packed128(300, 200)
	gasFees := packed128(7, 90)
	paymasterData := append(append(append(paymaster.Bytes(), packedUint128(11)...), packedUint128(12)...), 0xaa)
	paymasterSignature := []byte{0xde, 0xad, 0xbe, 0xef}
	paymasterData = append(paymasterData, paymasterSignature...)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(paymasterSignature)))
	paymasterData = append(paymasterData, length...)
	paymasterData = append(paymasterData, paymasterSignatureMagic...)
	wire := packedUserOperationWire{
		Sender: sender, Nonce: nonce, InitCode: append(factory.Bytes(), 0x01),
		CallData: []byte{0x02}, AccountGasLimits: accountGas,
		PreVerificationGas: big.NewInt(33), GasFees: gasFees,
		PaymasterAndData: paymasterData, Signature: []byte{0x99},
	}
	calldata, err := entryPointPackedABI.Pack("handleOps", []packedUserOperationWire{wire}, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	transaction := signedEntryPointTransaction(t, chainID, entryPoint, calldata)
	userOpHash := common.HexToHash("0x5678")
	revertData := standardErrorData(t, "account rejected")
	logs := []*types.Log{
		accountDeployedLog(t, entryPoint, userOpHash, sender, factory, paymaster, 0),
		revertReasonLog(t, "PostOpRevertReason", entryPoint, userOpHash, sender, nonce, revertData, 1),
		outcomeLog(t, entryPoint, userOpHash, sender, paymaster, nonce, false, 88, 66, 2),
	}
	block, receipts := operationBlock(transaction, logs)
	operations, err := DecodeBlock(testRegistry(t, entryPoint, Version09), chainID, block, receipts)
	if err != nil {
		t.Fatal(err)
	}
	request := operations[0].Request
	if string(request.PaymasterData) != string([]byte{0xaa}) ||
		string(request.PaymasterSignature) != string(paymasterSignature) ||
		request.PaymasterVerificationGasLimit.Uint64() != 11 ||
		request.PaymasterPostOpGasLimit.Uint64() != 12 ||
		request.VerificationGasLimit.Uint64() != 300 || request.CallGasLimit.Uint64() != 200 ||
		request.MaxPriorityFeePerGas.Uint64() != 7 || request.MaxFeePerGas.Uint64() != 90 {
		t.Fatalf("packed request = %#v", request)
	}
	if operations[0].Success || len(operations[0].Events) != 2 ||
		operations[0].Events[1].Kind != EventPostOpRevert ||
		operations[0].Events[1].Reason != "account rejected" {
		t.Fatalf("outcome = %#v", operations[0])
	}
}

func TestDecodeBlockSupportsPackedAggregatedOperationsInV07AndV08(t *testing.T) {
	t.Parallel()
	for _, version := range []Version{Version07, Version08} {
		t.Run(string(version), func(t *testing.T) {
			t.Parallel()
			chainID := big.NewInt(31337)
			entryPoint := common.BytesToAddress(append(make([]byte, 19), byte(version[2])))
			sender := common.HexToAddress("0xc00000000000000000000000000000000000000c")
			aggregator := common.HexToAddress("0xd00000000000000000000000000000000000000d")
			beneficiary := common.HexToAddress("0xe00000000000000000000000000000000000000e")
			wire := packedUserOperationWire{
				Sender: sender, Nonce: big.NewInt(3), AccountGasLimits: packed128(4, 5),
				PreVerificationGas: big.NewInt(6), GasFees: packed128(7, 8), Signature: []byte{0x09},
			}
			calldata, err := entryPointPackedABI.Pack("handleAggregatedOps", []userOperationsPerAggregatorPackedWire{{
				UserOps: []packedUserOperationWire{wire}, Aggregator: aggregator, Signature: []byte{0xaa, 0xbb},
			}}, beneficiary)
			if err != nil {
				t.Fatal(err)
			}
			transaction := signedEntryPointTransaction(t, chainID, entryPoint, calldata)
			userOpHash := common.BigToHash(big.NewInt(int64(version[2])))
			block, receipts := operationBlock(transaction, []*types.Log{
				outcomeLog(t, entryPoint, userOpHash, sender, common.Address{}, big.NewInt(3), true, 10, 11, 0),
			})
			operations, err := DecodeBlock(testRegistry(t, entryPoint, version), chainID, block, receipts)
			if err != nil {
				t.Fatal(err)
			}
			if len(operations) != 1 || operations[0].Request.Aggregator == nil ||
				*operations[0].Request.Aggregator != aggregator ||
				string(operations[0].Request.AggregatedSignature) != string([]byte{0xaa, 0xbb}) {
				t.Fatalf("aggregated operation = %#v", operations)
			}
		})
	}
}

func TestDecodeBlockRejectsEventCalldataMismatchAndIgnoresRevertedBundle(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(31337)
	entryPoint := common.HexToAddress("0x4337084d9e255ff0702461cf8895ce9e3b5ff108")
	sender := common.HexToAddress("0x9000000000000000000000000000000000000009")
	beneficiary := common.HexToAddress("0xa00000000000000000000000000000000000000a")
	wire := packedUserOperationWire{
		Sender: sender, Nonce: big.NewInt(1), AccountGasLimits: packed128(1, 2),
		PreVerificationGas: big.NewInt(3), GasFees: packed128(4, 5),
	}
	calldata, err := entryPointPackedABI.Pack("handleOps", []packedUserOperationWire{wire}, beneficiary)
	if err != nil {
		t.Fatal(err)
	}
	tx := signedEntryPointTransaction(t, chainID, entryPoint, calldata)
	logs := []*types.Log{outcomeLog(
		t, entryPoint, common.HexToHash("0x99"), common.HexToAddress("0xb00000000000000000000000000000000000000b"),
		common.Address{}, big.NewInt(1), true, 1, 1, 0,
	)}
	block, receipts := operationBlock(tx, logs)
	if _, err := DecodeBlock(testRegistry(t, entryPoint, Version08), chainID, block, receipts); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch error = %v", err)
	}
	receipts[0].Status = types.ReceiptStatusFailed
	operations, err := DecodeBlock(testRegistry(t, entryPoint, Version08), chainID, block, receipts)
	if err != nil || len(operations) != 0 {
		t.Fatalf("reverted bundle operations=%d error=%v", len(operations), err)
	}
}

func TestDecodeBlockRejectsSupplementalEventAfterOutcome(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(31337)
	entryPoint := common.HexToAddress("0x433709009B8330FDa32311DF1C2AFA402eD8D009")
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	wire := packedUserOperationWire{
		Sender: sender, Nonce: big.NewInt(1), AccountGasLimits: packed128(1, 2),
		PreVerificationGas: big.NewInt(3), GasFees: packed128(4, 5),
	}
	calldata, err := entryPointPackedABI.Pack(
		"handleOps",
		[]packedUserOperationWire{wire},
		common.HexToAddress("0x2000000000000000000000000000000000000002"),
	)
	if err != nil {
		t.Fatal(err)
	}
	userOpHash := common.HexToHash("0x99")
	block, receipts := operationBlock(signedEntryPointTransaction(t, chainID, entryPoint, calldata), []*types.Log{
		outcomeLog(t, entryPoint, userOpHash, sender, common.Address{}, big.NewInt(1), false, 1, 1, 0),
		revertReasonLog(t, "UserOperationRevertReason", entryPoint, userOpHash, sender, big.NewInt(1), []byte{0xff}, 1),
	})
	if _, err := DecodeBlock(testRegistry(t, entryPoint, Version09), chainID, block, receipts); err == nil ||
		!strings.Contains(err.Error(), "does not precede") {
		t.Fatalf("supplemental event order error = %v", err)
	}
}

func TestEntryPointV06RejectsLaterLifecycleEvents(t *testing.T) {
	t.Parallel()
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	request := Request{Sender: sender, Nonce: big.NewInt(1)}
	outcome := operationOutcome{Sender: sender, Nonce: big.NewInt(1), LogIndex: 2}
	for _, kind := range []EventKind{EventPostOpRevert, EventPrefundTooLow} {
		err := validateAssociatedEvents(Version06, request, outcome, []ProtocolEvent{{
			Kind: kind, Sender: sender, Nonce: big.NewInt(1), LogIndex: 1,
		}})
		if err == nil || !strings.Contains(err.Error(), "v0.6") {
			t.Fatalf("v0.6 event %s error = %v", kind, err)
		}
	}
}

func TestDecodeBlockRejectsDuplicateUserOperationHash(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(31337)
	entryPoint := common.HexToAddress("0x4337084d9e255ff0702461cf8895ce9e3b5ff108")
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	wire := packedUserOperationWire{
		Sender: sender, Nonce: big.NewInt(1), AccountGasLimits: packed128(1, 2),
		PreVerificationGas: big.NewInt(3), GasFees: packed128(4, 5),
	}
	calldata, err := entryPointPackedABI.Pack(
		"handleOps",
		[]packedUserOperationWire{wire, wire},
		common.HexToAddress("0x2000000000000000000000000000000000000002"),
	)
	if err != nil {
		t.Fatal(err)
	}
	userOpHash := common.HexToHash("0x99")
	block, receipts := operationBlock(signedEntryPointTransaction(t, chainID, entryPoint, calldata), []*types.Log{
		outcomeLog(t, entryPoint, userOpHash, sender, common.Address{}, big.NewInt(1), true, 1, 1, 0),
		outcomeLog(t, entryPoint, userOpHash, sender, common.Address{}, big.NewInt(1), true, 1, 1, 1),
	})
	if _, err := DecodeBlock(testRegistry(t, entryPoint, Version08), chainID, block, receipts); err == nil ||
		!strings.Contains(err.Error(), "duplicate userOpHash") {
		t.Fatalf("duplicate userOpHash error = %v", err)
	}
}

func TestEIP7702InitCodeRequiresZeroPaddedMarker(t *testing.T) {
	t.Parallel()
	if !eip7702InitCode([]byte{0x77, 0x02}) || !eip7702InitCode(append([]byte{0x77, 0x02}, make([]byte, 18)...)) {
		t.Fatal("valid EIP-7702 marker was rejected")
	}
	if eip7702InitCode([]byte{0x77, 0x02, 0x01}) {
		t.Fatal("non-zero-padded EIP-7702 marker was accepted")
	}
}

func testRegistry(t *testing.T, address common.Address, version Version) Registry {
	t.Helper()
	registry, err := NewRegistry(config.ERC4337Config{EntryPoints: []config.ERC4337EntryPointConfig{{
		Address: address.Hex(), Version: string(version),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func signedEntryPointTransaction(t *testing.T, chainID *big.Int, target common.Address, data []byte) *types.Transaction {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.LegacyTx{Nonce: 0, Gas: 5_000_000, GasPrice: big.NewInt(1), To: &target, Data: data})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func operationBlock(transaction *types.Transaction, logs []*types.Log) (*types.Block, types.Receipts) {
	header := &types.Header{Number: big.NewInt(7), GasLimit: 30_000_000, Time: 1}
	block := types.NewBlockWithHeader(header).WithBody(types.Body{Transactions: []*types.Transaction{transaction}})
	for _, log := range logs {
		log.BlockNumber, log.BlockHash, log.TxHash, log.TxIndex = 7, block.Hash(), transaction.Hash(), 0
	}
	receipt := &types.Receipt{
		Status: types.ReceiptStatusSuccessful, TxHash: transaction.Hash(),
		BlockHash: block.Hash(), BlockNumber: big.NewInt(7), TransactionIndex: 0, Logs: logs,
	}
	return block, types.Receipts{receipt}
}

func outcomeLog(
	t *testing.T, entryPoint common.Address, hash common.Hash, sender, paymaster common.Address,
	nonce *big.Int, success bool, cost, used uint64, index uint,
) *types.Log {
	t.Helper()
	event := protocolEventABI.Events["UserOperationEvent"]
	data, err := event.Inputs.NonIndexed().Pack(nonce, success, new(big.Int).SetUint64(cost), new(big.Int).SetUint64(used))
	if err != nil {
		t.Fatal(err)
	}
	return &types.Log{Address: entryPoint, Index: index, Topics: []common.Hash{
		event.ID, hash, common.BytesToHash(sender.Bytes()), common.BytesToHash(paymaster.Bytes()),
	}, Data: data}
}

func accountDeployedLog(
	t *testing.T, entryPoint common.Address, hash common.Hash, sender, factory, paymaster common.Address, index uint,
) *types.Log {
	t.Helper()
	event := protocolEventABI.Events["AccountDeployed"]
	data, err := event.Inputs.NonIndexed().Pack(factory, paymaster)
	if err != nil {
		t.Fatal(err)
	}
	return &types.Log{Address: entryPoint, Index: index, Topics: []common.Hash{
		event.ID, hash, common.BytesToHash(sender.Bytes()),
	}, Data: data}
}

func revertReasonLog(
	t *testing.T, name string, entryPoint common.Address, hash common.Hash, sender common.Address,
	nonce *big.Int, raw []byte, index uint,
) *types.Log {
	t.Helper()
	event := protocolEventABI.Events[name]
	data, err := event.Inputs.NonIndexed().Pack(nonce, raw)
	if err != nil {
		t.Fatal(err)
	}
	return &types.Log{Address: entryPoint, Index: index, Topics: []common.Hash{
		event.ID, hash, common.BytesToHash(sender.Bytes()),
	}, Data: data}
}

func standardErrorData(t *testing.T, reason string) []byte {
	t.Helper()
	data, err := errorArguments.Pack(reason)
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte(nil), errorSelector...), data...)
}

func packed128(high, low uint64) [32]byte {
	var packed [32]byte
	binary.BigEndian.PutUint64(packed[8:16], high)
	binary.BigEndian.PutUint64(packed[24:32], low)
	return packed
}

func packedUint128(value uint64) []byte {
	result := make([]byte, 16)
	binary.BigEndian.PutUint64(result[8:], value)
	return result
}
