package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

func TestStateDiffRPCProcessorUsesCompletePrestateForOmittedTopLevelTargets(t *testing.T) {
	t.Parallel()
	job := Job{ID: "state-block", Stage: StateDiffStage, ChainID: "1", BlockHash: uintWord(530), BlockNumber: 530}
	firstHash, secondHash := uintWord(531), uintWord(532)
	firstTarget, secondTarget := testAddress(0x31), testAddress(0x32)
	transactions := []stateDiffTransaction{
		{hash: firstHash, tx: types.NewTx(&types.LegacyTx{To: &firstTarget}), sender: testAddress(0x41)},
		{hash: secondHash, tx: types.NewTx(&types.LegacyTx{To: &secondTarget}), sender: testAddress(0x42)},
	}
	diff := json.RawMessage(`{"pre":{},"post":{}}`)
	completePrestates := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"` + secondTarget.Hex() + `":{"balance":"0x0","nonce":"0x0","code":"0x6000","storage":{}}}`),
	}
	call := 0
	caller := &traceTestCaller{handler: func(method, hash string) (json.RawMessage, error) {
		if method != debugTraceBlockByHashMethod || hash != job.BlockHash.String() {
			return nil, errors.New("unexpected state difference RPC request")
		}
		call++
		if call == 1 {
			return repeatedBlockTraceResponse(t, diff, firstHash, secondHash), nil
		}
		return blockTraceResponse(
			t, []common.Hash{firstHash, secondHash}, completePrestates, nil,
		), nil
	}}
	processor := &StateDiffRPCProcessor{limits: DefaultStateDiffLimits()}
	if err := processor.fetch(t.Context(), caller, job, transactions, &stateDiffBudget{}); err != nil {
		t.Fatal(err)
	}
	if len(caller.calls) != 2 || caller.calls[0].method != debugTraceBlockByHashMethod ||
		caller.calls[0].tracer != "prestateTracer" || caller.calls[0].diffMode == nil || !*caller.calls[0].diffMode {
		t.Fatalf("calls=%+v", caller.calls)
	}
	if caller.calls[1].method != debugTraceBlockByHashMethod ||
		caller.calls[1].tracer != "prestateTracer" || caller.calls[1].diffMode == nil || *caller.calls[1].diffMode {
		t.Fatalf("calls=%+v", caller.calls)
	}
	for index := range transactions {
		if len(transactions[index].changes) != 0 {
			t.Fatalf("transaction %d changes=%+v", index, transactions[index].changes)
		}
	}
	if got := executionResolutionForContext(transactions[0].executions, firstTarget); got != "empty" {
		t.Fatalf("omitted EOA execution resolution = %q, want empty", got)
	}
	if got := executionResolutionForContext(transactions[1].executions, secondTarget); got != "direct" {
		t.Fatalf("unchanged contract execution resolution = %q, want direct", got)
	}
}

func executionResolutionForContext(executions []executionCodeResolution, context common.Address) string {
	for _, execution := range executions {
		if execution.context == context {
			return execution.resolution
		}
	}
	return ""
}

func TestStateDiffRPCProcessorSkipsCompletePrestateWhenDiffProvesTarget(t *testing.T) {
	t.Parallel()
	job := Job{ID: "state-diff-target", Stage: StateDiffStage, ChainID: "1", BlockHash: uintWord(533), BlockNumber: 533}
	transactionHash, target := uintWord(534), testAddress(0x35)
	transaction := stateDiffTransaction{
		hash:   transactionHash,
		tx:     types.NewTx(&types.LegacyTx{To: &target}),
		sender: testAddress(0x45),
	}
	diff := json.RawMessage(`{"pre":{"` + target.Hex() + `":{"balance":"0x1"}},"post":{"` + target.Hex() + `":{"balance":"0x2"}}}`)
	caller := &traceTestCaller{handler: func(string, string) (json.RawMessage, error) {
		return repeatedBlockTraceResponse(t, diff, transactionHash), nil
	}}
	processor := &StateDiffRPCProcessor{limits: DefaultStateDiffLimits()}
	transactions := []stateDiffTransaction{transaction}
	if err := processor.fetch(t.Context(), caller, job, transactions, &stateDiffBudget{}); err != nil {
		t.Fatal(err)
	}
	if len(caller.calls) != 1 || caller.calls[0].diffMode == nil || !*caller.calls[0].diffMode {
		t.Fatalf("calls=%+v", caller.calls)
	}
	if got := executionResolutionForContext(transactions[0].executions, target); got != "empty" {
		t.Fatalf("execution resolution = %q, want empty", got)
	}
}

func TestCompletePrestateSupplementKeepsMissingDelegatedCodeUnavailable(t *testing.T) {
	t.Parallel()
	authority, delegate := testAddress(0x71), testAddress(0x72)
	tx := types.NewTx(&types.LegacyTx{To: &authority})
	evidence := transactionStateEvidence{pre: map[common.Address]stateAccountEvidence{
		authority: {code: types.AddressToDelegation(delegate)},
	}}
	supplementTransactionExecutionEvidence(tx, &evidence, map[common.Address]stateAccountEvidence{})
	_, executions, err := deriveEIP7702Evidence("1", tx, testAddress(0x73), evidence)
	if err != nil {
		t.Fatal(err)
	}
	if got := executionResolutionForContext(executions, authority); got != "unavailable" {
		t.Fatalf("delegated execution resolution = %q, want unavailable", got)
	}
}

func TestDecodeTransactionPrestateRejectsMalformedUnrelatedEvidence(t *testing.T) {
	t.Parallel()
	_, _, err := decodeTransactionPrestate(json.RawMessage(
		`{"0x0000000000000000000000000000000000000071":{"storage":{"0x01":"0x02"}}}`,
	), DefaultStateDiffLimits())
	if err == nil || !strings.Contains(err.Error(), "invalid storage key") {
		t.Fatalf("error = %v, want invalid storage key", err)
	}
}

func TestStateDiffRPCProcessorRejectsBlockPayloadOverLimit(t *testing.T) {
	t.Parallel()
	job := Job{ID: "state-block-limit", Stage: StateDiffStage, ChainID: "1", BlockHash: uintWord(540), BlockNumber: 540}
	transactionHash := uintWord(541)
	target := testAddress(0x51)
	transactions := []stateDiffTransaction{{
		hash: transactionHash, tx: types.NewTx(&types.LegacyTx{To: &target}), sender: testAddress(0x52),
	}}
	raw := repeatedBlockTraceResponse(t, json.RawMessage(`{"pre":{},"post":{}}`), transactionHash)
	caller := &traceTestCaller{handler: func(string, string) (json.RawMessage, error) { return raw, nil }}
	limits := DefaultStateDiffLimits()
	limits.MaxBlockPayloadBytes = len(raw) - 1
	processor := &StateDiffRPCProcessor{limits: limits}
	err := processor.fetch(context.Background(), caller, job, transactions, &stateDiffBudget{})
	var classified stageError
	if !errors.Is(err, ErrStateDiffLimit) || !errors.As(err, &classified) || classified.kind != "permanent" || len(caller.calls) != 1 {
		t.Fatalf("err=%#v calls=%+v", err, caller.calls)
	}
}

func TestStateDiffRPCProcessorClassifiesBlockItemErrorsWithoutTransactionFallback(t *testing.T) {
	t.Parallel()
	job := Job{ID: "state-block-error", Stage: StateDiffStage, ChainID: "1", BlockHash: uintWord(550), BlockNumber: 550}
	transactionHash := uintWord(551)
	target := testAddress(0x61)
	transactions := []stateDiffTransaction{{
		hash: transactionHash, tx: types.NewTx(&types.LegacyTx{To: &target}), sender: testAddress(0x62),
	}}
	for _, test := range []struct {
		name    string
		message string
		want    error
	}{
		{name: "history unavailable", message: "provider-secret missing trie node", want: errBlockTraceHistoryUnavailable},
		{name: "retryable item failure", message: "provider-secret tracer timeout", want: errBlockTraceTransactionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caller := &traceTestCaller{handler: func(string, string) (json.RawMessage, error) {
				return blockTraceResponse(
					t, []common.Hash{transactionHash}, []json.RawMessage{nil}, map[int]string{0: test.message},
				), nil
			}}
			processor := &StateDiffRPCProcessor{limits: DefaultStateDiffLimits()}
			err := processor.fetch(t.Context(), caller, job, transactions, &stateDiffBudget{})
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "provider-secret") ||
				len(caller.calls) != 1 || caller.calls[0].method != debugTraceBlockByHashMethod {
				t.Fatalf("err=%v calls=%+v", err, caller.calls)
			}
		})
	}
}

func TestDeriveEIP7702EvidenceReplaysOrderedAuthorizationsAndExecution(t *testing.T) {
	t.Parallel()
	key, err := crypto.HexToECDSA("4f3edf983ac63ad7c9b3f5a7b5c4e2f6a7b8c9d0e1f2233445566778899aabbc")
	if err != nil {
		t.Fatal(err)
	}
	authority := crypto.PubkeyToAddress(key.PublicKey)
	sender := testAddress(0x91)
	firstDelegate, secondDelegate := testAddress(0xa1), testAddress(0xa2)
	first, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(0), Address: firstDelegate, Nonce: 7})
	if err != nil {
		t.Fatal(err)
	}
	second, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Address: secondDelegate, Nonce: 8})
	if err != nil {
		t.Fatal(err)
	}
	target := testAddress(0xb1)
	tx := types.NewTx(&types.SetCodeTx{
		ChainID: uint256.NewInt(1), To: target, AuthList: []types.SetCodeAuthorization{first, second},
		GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(1), Value: uint256.NewInt(0),
	})
	finalCode := types.AddressToDelegation(secondDelegate)
	evidence := transactionStateEvidence{
		pre: map[common.Address]stateAccountEvidence{
			authority: {nonce: 7}, sender: {nonce: 3},
			firstDelegate: {code: []byte{0x60, 0x00}}, secondDelegate: {code: []byte{0x60, 0x01}},
		},
		post: map[common.Address]stateAccountEvidence{
			authority: {nonce: 9, code: finalCode}, sender: {nonce: 4},
		},
	}
	results, executions, err := deriveEIP7702Evidence("1", tx, sender, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].applicationStatus != "applied" ||
		results[1].applicationStatus != "applied" || results[1].authority == nil || *results[1].authority != authority {
		t.Fatalf("authorization results = %+v", results)
	}
	for index := range executions {
		item := executions[index]
		if item.context == authority {
			if item.resolution != "eip7702_delegate" || item.execution == nil || *item.execution != secondDelegate ||
				item.codeHash == nil || *item.codeHash != crypto.Keccak256Hash([]byte{0x60, 0x01}) {
				t.Fatalf("authority execution = %+v", item)
			}
			return
		}
	}
	t.Fatal("authority execution missing")
}

func TestDeriveEIP7702EvidenceHonorsSenderNonceAndStableSkipReasons(t *testing.T) {
	t.Parallel()
	key, err := crypto.HexToECDSA("6cbed15c177e12c9e9c9e7b3d9f1a9ce3a0f1d2c3b4a59687766554433221100")
	if err != nil {
		t.Fatal(err)
	}
	authority := crypto.PubkeyToAddress(key.PublicKey)
	delegate := testAddress(0xc1)
	auth, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Address: delegate, Nonce: 6})
	if err != nil {
		t.Fatal(err)
	}
	wrongChain := auth
	wrongChain.ChainID = *uint256.NewInt(2)
	target := testAddress(0xc2)
	tx := types.NewTx(&types.SetCodeTx{
		ChainID: uint256.NewInt(1), Nonce: 5, To: target,
		AuthList:  []types.SetCodeAuthorization{wrongChain, auth},
		GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(1), Value: uint256.NewInt(0),
	})
	evidence := transactionStateEvidence{
		pre:  map[common.Address]stateAccountEvidence{authority: {nonce: 5}, delegate: {code: []byte{0x60}}},
		post: map[common.Address]stateAccountEvidence{authority: {nonce: 7, code: types.AddressToDelegation(delegate)}},
	}
	results, _, err := deriveEIP7702Evidence("1", tx, authority, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].skipReason != "wrong_chain_id" ||
		results[0].applicationStatus != "skipped" || results[1].applicationStatus != "applied" {
		t.Fatalf("authorization results = %+v", results)
	}
}

func TestDeriveEIP7702EvidenceUsesSingleDelegationLayerAndEmptyTarget(t *testing.T) {
	t.Parallel()
	authority, delegate, second := testAddress(0xd1), testAddress(0xd2), testAddress(0xd3)
	tx := types.NewTx(&types.LegacyTx{To: &authority})
	for _, test := range []struct {
		name         string
		delegateCode []byte
		want         string
		wantHash     common.Hash
	}{
		{name: "delegate to delegate", delegateCode: types.AddressToDelegation(second), want: "eip7702_delegate", wantHash: crypto.Keccak256Hash(types.AddressToDelegation(second))},
		{name: "empty delegate", delegateCode: nil, want: "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := transactionStateEvidence{
				pre: map[common.Address]stateAccountEvidence{
					authority: {code: types.AddressToDelegation(delegate)}, delegate: {code: test.delegateCode},
				}, post: map[common.Address]stateAccountEvidence{},
			}
			_, executions, err := deriveEIP7702Evidence("1", tx, testAddress(0xee), evidence)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range executions {
				if item.context == authority {
					if item.resolution != test.want || test.wantHash != (common.Hash{}) &&
						(item.codeHash == nil || *item.codeHash != test.wantHash) {
						t.Fatalf("execution=%+v", item)
					}
					return
				}
			}
			t.Fatal("authority execution missing")
		})
	}

	t.Run("precompile omitted from state", func(t *testing.T) {
		precompile := common.BytesToAddress([]byte{1})
		evidence := transactionStateEvidence{pre: map[common.Address]stateAccountEvidence{
			authority: {code: types.AddressToDelegation(precompile)},
		}}
		_, executions, err := deriveEIP7702Evidence("1", tx, testAddress(0xee), evidence)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range executions {
			if item.context == authority {
				if item.resolution != "empty" || item.execution != nil || item.codeHash != nil {
					t.Fatalf("precompile execution=%+v", item)
				}
				return
			}
		}
		t.Fatal("precompile authority execution missing")
	})
}

func TestDeriveEIP7702EvidenceRejectsInvalidTuplesAndClearsDelegation(t *testing.T) {
	t.Parallel()
	key, err := crypto.HexToECDSA("8f2a5594903ef45b7b5a8f9f033b0f037b3a3fce0fdd7f1464d31dce1a4f809d")
	if err != nil {
		t.Fatal(err)
	}
	authority := crypto.PubkeyToAddress(key.PublicKey)
	sender, delegate := testAddress(0xe1), testAddress(0xe2)
	sign := func(address common.Address, nonce uint64) types.SetCodeAuthorization {
		t.Helper()
		auth, signErr := types.SignSetCode(key, types.SetCodeAuthorization{
			ChainID: *uint256.NewInt(1), Address: address, Nonce: nonce,
		})
		if signErr != nil {
			t.Fatal(signErr)
		}
		return auth
	}
	transaction := func(auth types.SetCodeAuthorization) *types.Transaction {
		return types.NewTx(&types.SetCodeTx{
			ChainID: uint256.NewInt(1), To: sender, AuthList: []types.SetCodeAuthorization{auth},
			GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(1), Value: uint256.NewInt(0),
		})
	}

	highS := sign(delegate, 4)
	highS.S.SetFromBig(new(big.Int).Sub(crypto.S256().Params().N, highS.S.ToBig()))
	for _, test := range []struct {
		name       string
		auth       types.SetCodeAuthorization
		pre        stateAccountEvidence
		wantReason string
	}{
		{name: "high s", auth: highS, pre: stateAccountEvidence{nonce: 4}, wantReason: "invalid_signature"},
		{name: "authorization nonce overflow", auth: types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Nonce: math.MaxUint64}, wantReason: "nonce_overflow"},
		{name: "nonce mismatch", auth: sign(delegate, 4), pre: stateAccountEvidence{nonce: 3}, wantReason: "nonce_mismatch"},
		{name: "ordinary authority code", auth: sign(delegate, 4), pre: stateAccountEvidence{nonce: 4, code: []byte{0x60}}, wantReason: "authority_has_code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := transactionStateEvidence{pre: map[common.Address]stateAccountEvidence{
				sender: {}, authority: test.pre,
			}}
			results, _, deriveErr := deriveEIP7702Evidence("1", transaction(test.auth), sender, evidence)
			if deriveErr != nil {
				t.Fatal(deriveErr)
			}
			if len(results) != 1 || results[0].applicationStatus != "skipped" || results[0].skipReason != test.wantReason {
				t.Fatalf("authorization results=%+v", results)
			}
		})
	}

	clear := sign(common.Address{}, 4)
	results, executions, err := deriveEIP7702Evidence("1", transaction(clear), sender, transactionStateEvidence{
		pre: map[common.Address]stateAccountEvidence{
			sender: {}, authority: {nonce: 4, code: types.AddressToDelegation(delegate)},
		},
		post: map[common.Address]stateAccountEvidence{authority: {nonce: 5}},
	})
	if err != nil || len(results) != 1 || results[0].applicationStatus != "applied" {
		t.Fatalf("clear results=%+v err=%v", results, err)
	}
	for _, execution := range executions {
		if execution.context == authority && execution.resolution != "empty" {
			t.Fatalf("cleared execution=%+v", execution)
		}
	}
}

func TestDeriveEIP7702EvidenceFailsClosedOnNonceAndPostStateContradictions(t *testing.T) {
	t.Parallel()
	key, err := crypto.HexToECDSA("9f2a5594903ef45b7b5a8f9f033b0f037b3a3fce0fdd7f1464d31dce1a4f809c")
	if err != nil {
		t.Fatal(err)
	}
	authority, sender, delegate := crypto.PubkeyToAddress(key.PublicKey), testAddress(0xf1), testAddress(0xf2)
	auth, err := types.SignSetCode(key, types.SetCodeAuthorization{ChainID: *uint256.NewInt(1), Address: delegate, Nonce: 4})
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.SetCodeTx{
		ChainID: uint256.NewInt(1), To: sender, AuthList: []types.SetCodeAuthorization{auth},
		GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(1), Value: uint256.NewInt(0),
	})
	_, _, err = deriveEIP7702Evidence("1", tx, sender, transactionStateEvidence{
		pre: map[common.Address]stateAccountEvidence{sender: {nonce: math.MaxUint64}, authority: {nonce: 4}},
	})
	if err == nil || !strings.Contains(err.Error(), "sender nonce overflow") {
		t.Fatalf("sender overflow err=%v", err)
	}
	_, _, err = deriveEIP7702Evidence("1", tx, sender, transactionStateEvidence{
		pre:  map[common.Address]stateAccountEvidence{sender: {}, authority: {nonce: 4}, delegate: {code: []byte{0x60}}},
		post: map[common.Address]stateAccountEvidence{authority: {nonce: 5, code: types.AddressToDelegation(testAddress(0xff))}},
	})
	if err == nil || !strings.Contains(err.Error(), "contradicts post-state") {
		t.Fatalf("post-state contradiction err=%v", err)
	}
}

func TestProxyRelevantStateChangeRecognizesOnlyCodeAndERC1967Slots(t *testing.T) {
	t.Parallel()
	address := common.HexToAddress("0x0000000000000000000000000000000000000001")
	tests := []struct {
		name   string
		change stateChange
		want   bool
	}{
		{name: "code", change: stateChange{address: address, kind: "code"}, want: true},
		{name: "implementation", change: stateChange{address: address, kind: "storage", key: EIP1967ImplementationSlot[:]}, want: true},
		{name: "beacon", change: stateChange{address: address, kind: "storage", key: EIP1967BeaconSlot[:]}, want: true},
		{name: "admin", change: stateChange{address: address, kind: "storage", key: EIP1967AdminSlot[:]}, want: true},
		{name: "ordinary storage", change: stateChange{address: address, kind: "storage", key: common.HexToHash("0x01").Bytes()}, want: false},
		{name: "balance", change: stateChange{address: address, kind: "balance"}, want: false},
		{name: "short storage key", change: stateChange{address: address, kind: "storage", key: []byte{1}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := proxyRelevantStateChange(test.change); got != test.want {
				t.Fatalf("proxy relevance=%t want=%t change=%+v", got, test.want, test.change)
			}
		})
	}
}

func TestNormalizeStateDiffCanonicalizesAndSortsChanges(t *testing.T) {
	t.Parallel()
	addressA := "0x0000000000000000000000000000000000000001"
	addressB := "0x0000000000000000000000000000000000000002"
	key := "0x" + strings.Repeat("00", 31) + "01"
	beforeStorage := "0x" + strings.Repeat("00", 31) + "02"
	afterStorage := "0x" + strings.Repeat("00", 31) + "03"
	raw := json.RawMessage(`{
		"pre": {
			"` + addressB + `": {"nonce":"0x1"},
			"` + addressA + `": {
				"balance":"0x1",
				"code":"0x6000",
				"storage":{"` + key + `":"` + beforeStorage + `"}
			}
		},
		"post": {
			"` + addressA + `": {
				"balance":"0x2",
				"code":"0x6001",
				"storage":{"` + key + `":"` + afterStorage + `"}
			},
			"` + addressB + `": {"nonce":"0x2"}
		}
	}`)

	changes, counts, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if counts.accounts != 2 || counts.slots != 1 || counts.code != 4 || counts.text == 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].address.Hex() != "0x0000000000000000000000000000000000000001" ||
		changes[0].kind != "balance" || changes[1].kind != "code" ||
		changes[2].kind != "storage" || changes[3].kind != "nonce" {
		t.Fatalf("changes are not stable and sorted: %+v", changes)
	}
	if changes[0].before == nil || *changes[0].before != "1" ||
		changes[0].after == nil || *changes[0].after != "2" {
		t.Fatalf("balance change = %+v", changes[0])
	}
}

func TestNormalizeStateDiffAcceptsGethNumericNonce(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{"balance":"0x1"}},
		"post":{"` + address + `":{"balance":"0x1","nonce":1}}
	}`)

	changes, _, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "nonce" ||
		changes[0].key == nil || len(changes[0].key) != 0 ||
		changes[0].before != nil || changes[0].after == nil || *changes[0].after != "1" {
		t.Fatalf("numeric nonce changes = %+v", changes)
	}
}

func TestNormalizeStateDiffTreatsOmittedPostScalarsAsUnchanged(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	key := "0x" + strings.Repeat("00", 31) + "01"
	beforeStorage := "0x" + strings.Repeat("00", 31) + "02"
	afterStorage := "0x" + strings.Repeat("00", 31) + "03"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{
			"balance":"0x2a","nonce":"0x7","code":"0x6000",
			"storage":{"` + key + `":"` + beforeStorage + `"}
		}},
		"post":{"` + address + `":{
			"storage":{"` + key + `":"` + afterStorage + `"}
		}}
	}`)

	changes, counts, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "storage" {
		t.Fatalf("sparse post-state changes = %+v, want only storage", changes)
	}
	if counts.code != 2 {
		t.Fatalf("validated code bytes = %d, want 2", counts.code)
	}
}

func TestNormalizeStateDiffDistinguishesExplicitCodeClearFromOmission(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{"code":"0x6000"}},
		"post":{"` + address + `":{"code":"0x"}}
	}`)

	changes, _, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "code" ||
		changes[0].before == nil || *changes[0].before != "0x6000" ||
		changes[0].after == nil || *changes[0].after != "0x" {
		t.Fatalf("explicit code clear = %+v", changes)
	}
}

func TestNormalizeStateDiffRejectsMalformedAndOverBudgetPayloads(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	key := "0x" + strings.Repeat("00", 32)
	value := "0x" + strings.Repeat("00", 32)
	base := DefaultStateDiffLimits()
	tests := []struct {
		name   string
		raw    json.RawMessage
		mutate func(*StateDiffLimits)
		target error
	}{
		{name: "malformed JSON", raw: json.RawMessage(`{"pre":`)},
		{name: "missing post", raw: json.RawMessage(`{"pre":{}}`)},
		{name: "invalid address", raw: json.RawMessage(`{"pre":{"0x1":{}},"post":{}}`)},
		{name: "invalid quantity", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"balance":"1"}},"post":{}}`,
		)},
		{name: "invalid numeric nonce", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"nonce":-1}},"post":{}}`,
		)},
		{name: "oversized quoted nonce", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"nonce":"0x10000000000000000"}},"post":{}}`,
		)},
		{name: "invalid code", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"code":"xyz"}},"post":{}}`,
		)},
		{name: "invalid storage value", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"storage":{"` + key + `":"0x1"}}},"post":{}}`,
		)},
		{
			name: "account budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxAccounts = 0 },
			target: ErrStateDiffLimit,
		},
		{
			name: "storage budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"storage":{"` + key + `":"` + value + `"}}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxStorageSlots = 0 },
			target: ErrStateDiffLimit,
		},
		{
			name: "code budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"code":"0x6000"}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxCodeBytes = 1 },
			target: ErrStateDiffLimit,
		},
		{
			name:   "payload budget",
			raw:    json.RawMessage(`{"pre":{},"post":{}}`),
			mutate: func(limits *StateDiffLimits) { limits.MaxPayloadBytes = 1 },
			target: ErrStateDiffLimit,
		},
		{
			name: "text budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"balance":"0x1"}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxTextBytes = 1 },
			target: ErrStateDiffLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limits := base
			if test.mutate != nil {
				test.mutate(&limits)
			}
			_, _, err := normalizeStateDiff(test.raw, limits)
			if err == nil {
				t.Fatal("normalizeStateDiff succeeded")
			}
			if test.target != nil && !errors.Is(err, test.target) {
				t.Fatalf("error = %v, want %v", err, test.target)
			}
		})
	}
}

func TestStateDiffBudgetEnforcesWholeBlockLimits(t *testing.T) {
	t.Parallel()
	limits := DefaultStateDiffLimits()
	limits.MaxBlockPayloadBytes = 10
	limits.MaxBlockAccounts = 2
	limits.MaxBlockStorageSlots = 2
	limits.MaxBlockCodeBytes = 2
	limits.MaxBlockTextBytes = 2
	budget := &stateDiffBudget{}
	counts := normalizedStateDiffCounts{accounts: 1, slots: 1, code: 1, text: 1}
	if err := budget.add(5, counts, limits); err != nil {
		t.Fatal(err)
	}
	if err := budget.add(6, counts, limits); !errors.Is(err, ErrStateDiffLimit) {
		t.Fatalf("error = %v, want %v", err, ErrStateDiffLimit)
	}
}
