package x402testnet

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/big"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const testBlockHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type fakeSettlementChain struct {
	chainIDResult      *big.Int
	transactionResult  *types.Transaction
	transactionPending bool
	receiptResult      *types.Receipt
	headerResult       *types.Header
	includedResult     *types.Transaction
	chainIDErr         error
	transactionErr     error
	receiptErr         error
	headerErr          error
	includedErr        error
	chainIDCalls       int
	transactionCalls   int
	receiptCalls       int
	headerCalls        int
	includedCalls      int
}

func (chain *fakeSettlementChain) chainID(
	context.Context,
) (*big.Int, error) {
	chain.chainIDCalls++
	return chain.chainIDResult, chain.chainIDErr
}

func (chain *fakeSettlementChain) transaction(
	context.Context,
	common.Hash,
) (*types.Transaction, bool, error) {
	chain.transactionCalls++
	return chain.transactionResult,
		chain.transactionPending,
		chain.transactionErr
}

func (chain *fakeSettlementChain) receipt(
	context.Context,
	common.Hash,
) (*types.Receipt, error) {
	chain.receiptCalls++
	return chain.receiptResult, chain.receiptErr
}

func (chain *fakeSettlementChain) header(
	context.Context,
	common.Hash,
) (*types.Header, error) {
	chain.headerCalls++
	return chain.headerResult, chain.headerErr
}

func (chain *fakeSettlementChain) includedTransaction(
	context.Context,
	common.Hash,
	uint,
) (*types.Transaction, error) {
	chain.includedCalls++
	return chain.includedResult, chain.includedErr
}

func TestCheckChainIDRequiresBaseSepolia(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		result   *big.Int
		expected uint64
		rpcErr   error
		wantCode string
	}{
		{
			name: "base sepolia", result: new(big.Int).SetUint64(
				baseSepoliaChainID,
			),
			expected: baseSepoliaChainID,
		},
		{
			name: "wrong chain", result: big.NewInt(1),
			expected: baseSepoliaChainID, wantCode: "chain_id_mismatch",
		},
		{
			name: "missing response", result: nil,
			expected: baseSepoliaChainID, wantCode: "chain_response_invalid",
		},
		{
			name: "negative response", result: big.NewInt(-1),
			expected: baseSepoliaChainID, wantCode: "chain_response_invalid",
		},
		{
			name:     "oversized response",
			result:   new(big.Int).Lsh(big.NewInt(1), 65),
			expected: baseSepoliaChainID, wantCode: "chain_response_invalid",
		},
		{
			name:     "rpc secret is redacted",
			result:   new(big.Int).SetUint64(baseSepoliaChainID),
			expected: baseSepoliaChainID,
			rpcErr:   errors.New("https://secret@rpc.invalid/token"),
			wantCode: "chain_unavailable",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain := &fakeSettlementChain{
				chainIDResult: test.result,
				chainIDErr:    test.rpcErr,
			}
			err := checkChainID(
				context.Background(),
				chain,
				test.expected,
			)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func TestGethTransactionRejectsUnsupportedType(t *testing.T) {
	t.Parallel()
	var transaction types.Transaction
	err := transaction.UnmarshalJSON([]byte(`{"type":"0x7f"}`))
	if !errors.Is(err, types.ErrTxTypeNotSupported) {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
}

func TestVerifyChainAcceptsSuccessfulReceiptAndAggregateTransfers(t *testing.T) {
	t.Parallel()
	chain := validFakeChain()
	expected := validChainExpectation(t, chain.transactionResult)
	chain.receiptResult.Logs = []*types.Log{
		transferLog("400", testPayer, testRecipient, expected.transactionHash),
		transferLog("600", testPayer, testRecipient, expected.transactionHash),
		transferLog("7", testRecipient, testPayer, expected.transactionHash),
	}
	evidence, err := verifyChain(context.Background(), chain, expected)
	if err != nil {
		t.Fatalf("verifyChain() error = %v", err)
	}
	if evidence.TransactionHash != canonicalHash(expected.transactionHash) ||
		evidence.BlockHash != canonicalHash(chain.headerResult.Hash()) ||
		evidence.BlockNumber != "16" ||
		evidence.TransferCount != 2 {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if chain.chainIDCalls != 1 ||
		chain.transactionCalls != 1 ||
		chain.receiptCalls != 1 ||
		chain.headerCalls != 1 ||
		chain.includedCalls != 1 {
		t.Fatalf(
			"unexpected calls: chain=%d tx=%d receipt=%d header=%d included=%d",
			chain.chainIDCalls,
			chain.transactionCalls,
			chain.receiptCalls,
			chain.headerCalls,
			chain.includedCalls,
		)
	}
}

func TestVerifyChainRejectsFailedReceiptAndTransferMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		mutate           func(*testing.T, *fakeSettlementChain)
		alignTransaction bool
		wantCode         string
	}{
		{
			name: "failed receipt",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Status = types.ReceiptStatusFailed
			},
			wantCode: "chain_receipt_failed",
		},
		{
			name: "invalid receipt status",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Status = 2
			},
			wantCode: "chain_response_invalid",
		},
		{
			name: "amount mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Data = dataWord("999")
			},
			wantCode: "chain_transfer_mismatch",
		},
		{
			name: "recipient mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Topics[2] =
					addressTopic(testPayer)
			},
			wantCode: "chain_transfer_mismatch",
		},
		{
			name: "transaction mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionResult = testTransaction(
					common.HexToAddress(testAsset),
					new(big.Int),
					settlementInput(),
					baseSepoliaChainID,
					2,
				)
			},
			wantCode: "chain_transaction_mismatch",
		},
		{
			name: "unsupported transaction type response",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionErr = normalizeChainRPCError(
					types.ErrTxTypeNotSupported,
				)
			},
			wantCode: "chain_response_invalid",
		},
		{
			name: "settlement target mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionResult = testTransaction(
					common.HexToAddress(testRecipient),
					new(big.Int),
					settlementInput(),
					baseSepoliaChainID,
					1,
				)
			},
			alignTransaction: true,
			wantCode:         "chain_transaction_mismatch",
		},
		{
			name: "settlement value mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionResult = testTransaction(
					common.HexToAddress(testAsset),
					big.NewInt(1),
					settlementInput(),
					baseSepoliaChainID,
					1,
				)
			},
			alignTransaction: true,
			wantCode:         "chain_transaction_mismatch",
		},
		{
			name: "settlement calldata mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				input := chain.transactionResult.Data()
				input[4] ^= 0xff
				chain.transactionResult = testTransaction(
					common.HexToAddress(testAsset),
					new(big.Int),
					input,
					baseSepoliaChainID,
					1,
				)
			},
			alignTransaction: true,
			wantCode:         "chain_transaction_mismatch",
		},
		{
			name: "settlement calldata oversized",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionResult = testTransaction(
					common.HexToAddress(testAsset),
					new(big.Int),
					make([]byte, maxSettlementInputBytes+1),
					baseSepoliaChainID,
					1,
				)
			},
			alignTransaction: true,
			wantCode:         "chain_transaction_mismatch",
		},
		{
			name: "transaction chain mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionResult = testTransaction(
					common.HexToAddress(testAsset),
					new(big.Int),
					settlementInput(),
					1,
					1,
				)
			},
			alignTransaction: true,
			wantCode:         "chain_transaction_mismatch",
		},
		{
			name: "receipt block number missing",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.BlockNumber = nil
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "receipt block mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.BlockNumber = big.NewInt(17)
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "receipt hash mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.TxHash =
					common.HexToHash(testBlockHash)
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "header hash mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.headerResult = &types.Header{
					Number: big.NewInt(16),
					Time:   1,
				}
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "included transaction mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.includedResult = testTransaction(
					common.HexToAddress(testAsset),
					new(big.Int),
					settlementInput(),
					baseSepoliaChainID,
					2,
				)
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "receipt type mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Type = types.LegacyTxType
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "malformed receipt response",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptErr = normalizeChainRPCError(
					errors.New("missing required receipt field"),
				)
			},
			wantCode: "chain_response_invalid",
		},
		{
			name: "pending transaction",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.transactionPending = true
			},
			wantCode: "chain_transaction_pending",
		},
		{
			name: "nil log",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0] = nil
			},
			wantCode: "chain_response_invalid",
		},
		{
			name: "log block mismatch",
			mutate: func(_ *testing.T, chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].BlockHash =
					common.HexToHash(testTxHash)
			},
			wantCode: "chain_response_invalid",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain := validFakeChain()
			expected := validChainExpectation(t, chain.transactionResult)
			test.mutate(t, chain)
			if test.alignTransaction {
				alignExpectedTransaction(&expected, chain)
			}
			_, err := verifyChain(
				context.Background(),
				chain,
				expected,
			)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func alignExpectedTransaction(
	expected *chainExpectation,
	chain *fakeSettlementChain,
) {
	transactionHash := chain.transactionResult.Hash()
	expected.transactionHash = transactionHash
	chain.receiptResult.TxHash = transactionHash
	chain.includedResult = chain.transactionResult
	for _, log := range chain.receiptResult.Logs {
		if log != nil {
			log.TxHash = transactionHash
		}
	}
}

func TestVerifyChainRejectsMalformedTransferLog(t *testing.T) {
	t.Parallel()
	chain := validFakeChain()
	expected := validChainExpectation(t, chain.transactionResult)
	topic := addressTopic(testPayer)
	topic[0] = 1
	chain.receiptResult.Logs[0].Topics[1] = topic
	_, err := verifyChain(
		context.Background(),
		chain,
		expected,
	)
	if got := ErrorCode(err); got != "chain_response_invalid" {
		t.Fatalf("ErrorCode() = %q", got)
	}
}

func TestChainHTTPClientIsRestricted(t *testing.T) {
	t.Parallel()
	if validChainRPCURL("http://rpc.example") ||
		validChainRPCURL("https://user:secret@rpc.example") ||
		validChainRPCURL("https://rpc.example/#fragment") ||
		!validChainRPCURL("https://rpc.example/v1/token") {
		t.Fatal("unexpected RPC URL validation")
	}
	client := newChainHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("chain RPC transport retained proxy discovery")
	}
	if transport.MaxResponseHeaderBytes !=
		maxChainRPCResponseHeaderBytes ||
		transport.MaxConnsPerHost != 2 ||
		transport.MaxIdleConns != 2 ||
		transport.MaxIdleConnsPerHost != 1 {
		t.Fatalf(
			"unexpected transport bounds: headers=%d conns=%d idle=%d idle_host=%d",
			transport.MaxResponseHeaderBytes,
			transport.MaxConnsPerHost,
			transport.MaxIdleConns,
			transport.MaxIdleConnsPerHost,
		)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"https://rpc.example",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(request, nil); !errors.Is(
		err,
		http.ErrUseLastResponse,
	) {
		t.Fatalf("CheckRedirect() error = %v", err)
	}
}

func validChainExpectation(
	t *testing.T,
	transaction *types.Transaction,
) chainExpectation {
	t.Helper()
	callData := testSettlementCallData()
	result, ok := parseChainExpectation(ChainOptions{
		RPCURL:  "https://rpc.example/v1/token",
		ChainID: baseSepoliaChainID,
		TransactionHash: canonicalHash(
			transaction.Hash(),
		),
		Asset:                testAsset,
		AmountAtomic:         "1000",
		Recipient:            testRecipient,
		Payer:                testPayer,
		CallDataPrefixBytes:  len(callData),
		CallDataPrefixSHA256: sha256.Sum256(callData),
	})
	if !ok {
		t.Fatal("valid chain expectation did not parse")
	}
	return result
}

func validFakeChain() *fakeSettlementChain {
	transaction := testTransaction(
		common.HexToAddress(testAsset),
		new(big.Int),
		settlementInput(),
		baseSepoliaChainID,
		1,
	)
	transactionHash := transaction.Hash()
	header := testHeader()
	blockHash := header.Hash()
	return &fakeSettlementChain{
		chainIDResult:     new(big.Int).SetUint64(baseSepoliaChainID),
		transactionResult: transaction,
		headerResult:      header,
		includedResult:    transaction,
		receiptResult: &types.Receipt{
			Type:        transaction.Type(),
			Status:      types.ReceiptStatusSuccessful,
			TxHash:      transactionHash,
			BlockHash:   blockHash,
			BlockNumber: big.NewInt(16),
			Logs: []*types.Log{
				transferLog(
					"1000",
					testPayer,
					testRecipient,
					transactionHash,
				),
			},
		},
	}
}

func testTransaction(
	to common.Address,
	value *big.Int,
	input []byte,
	chainID uint64,
	nonce uint64,
) *types.Transaction {
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   new(big.Int).SetUint64(chainID),
		Nonce:     nonce,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(2),
		Gas:       500_000,
		To:        &to,
		Value:     new(big.Int).Set(value),
		Data:      append([]byte(nil), input...),
	})
}

func settlementInput() []byte {
	return append(
		testSettlementCallData(),
		0xde, 0xad, 0xbe, 0xef,
	)
}

func testHeader() *types.Header {
	return &types.Header{Number: big.NewInt(16)}
}

func testSettlementCallData() []byte {
	result := make([]byte, expectedSettlementCallBytes)
	for index := range result {
		result[index] = byte(index)
	}
	return result
}

func transferLog(
	amount string,
	from string,
	to string,
	transactionHash common.Hash,
) *types.Log {
	return &types.Log{
		Address: common.HexToAddress(testAsset),
		Topics: []common.Hash{
			erc20TransferTopic,
			addressTopic(from),
			addressTopic(to),
		},
		Data:        dataWord(amount),
		BlockHash:   testHeader().Hash(),
		BlockNumber: 16,
		TxHash:      transactionHash,
	}
}

func addressTopic(address string) common.Hash {
	return common.BytesToHash(common.HexToAddress(address).Bytes())
}

func dataWord(amount string) []byte {
	value, ok := parseAmount(amount)
	if !ok {
		panic("invalid test amount")
	}
	result := make([]byte, common.HashLength)
	return value.FillBytes(result)
}
