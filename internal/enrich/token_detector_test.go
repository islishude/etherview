package enrich

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

type tokenRPCCall struct {
	method string
	params []any
}

type fakeTokenRPCCaller struct {
	mu      sync.Mutex
	calls   []tokenRPCCall
	handler func(string, []any) ([]byte, error)
}

type tokenRPCCallerFunc func(context.Context, string, []any, any) error

func (function tokenRPCCallerFunc) Call(ctx context.Context, method string, params []any, result any) error {
	return function(ctx, method, params, result)
}

func (caller *fakeTokenRPCCaller) Call(_ context.Context, method string, params []any, result any) error {
	caller.mu.Lock()
	caller.calls = append(caller.calls, tokenRPCCall{method: method, params: params})
	caller.mu.Unlock()
	output, err := caller.handler(method, params)
	if err != nil {
		return err
	}
	encoded, ok := result.(*ethrpc.Data)
	if !ok {
		return fmt.Errorf("unexpected token RPC result %T", result)
	}
	*encoded = ethrpc.DataFromBytes(output)
	return nil
}

func (caller *fakeTokenRPCCaller) recordedCalls() []tokenRPCCall {
	caller.mu.Lock()
	defer caller.mu.Unlock()
	return append([]tokenRPCCall(nil), caller.calls...)
}

func TestRPCTokenDetectorCombinesERC20SignalsAtFixedBlock(t *testing.T) {
	t.Parallel()
	job := Job{ID: "detect-20", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(20), BlockNumber: 20}
	address := testAddress(0x20)
	code := []byte{0x60, 0x01, 0x60, 0x02}
	nameSelector := SignatureSelector("name()")
	symbolSelector := SignatureSelector("symbol()")
	decimalsSelector := SignatureSelector("decimals()")
	totalSupplySelector := SignatureSelector("totalSupply()")
	supportsSelector := SignatureSelector("supportsInterface(bytes4)")
	caller := &fakeTokenRPCCaller{}
	caller.handler = func(method string, params []any) ([]byte, error) {
		switch method {
		case "eth_getCode":
			return code, nil
		case "eth_call":
			request, ok := params[0].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("eth_call request type %T", params[0])
			}
			encoded, err := ethrpc.ParseData(request["data"].(string))
			if err != nil {
				return nil, err
			}
			input, _ := encoded.Bytes()
			if len(input) < 4 {
				return nil, errors.New("short eth_call input")
			}
			var selector [4]byte
			copy(selector[:], input[:4])
			switch selector {
			case supportsSelector:
				return wordBytes(uintWord(0)), nil
			case nameSelector:
				return nil, &ethrpc.RPCError{Code: 3, Message: "execution reverted"}
			case symbolSelector:
				return encodeDynamicBytes([]byte("TOK")), nil
			case decimalsSelector:
				return wordBytes(uintWord(18)), nil
			case totalSupplySelector:
				return wordBytes(uintWord(1_000_000)), nil
			default:
				return nil, fmt.Errorf("unexpected selector 0x%x", selector)
			}
		default:
			return nil, fmt.Errorf("unexpected RPC method %q", method)
		}
	}
	detector, err := NewRPCTokenDetector(caller, TokenProbeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	detection, err := detector.Detect(context.Background(), TokenDetectionRequest{
		Job: job, Address: address, Evidence: TokenLogEvidence{ERC20: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detection.Standard != TokenERC20 || detection.Confidence != ConfidenceHigh || detection.CodeHash != codeHash(code) {
		t.Fatalf("classification=%s confidence=%s code_hash=%s", detection.Standard, detection.Confidence, detection.CodeHash)
	}
	if detection.Name != nil || detection.Symbol == nil || *detection.Symbol != "TOK" ||
		detection.Decimals == nil || *detection.Decimals != 18 || detection.TotalSupply == nil || *detection.TotalSupply != "1000000" ||
		detection.MetadataState != TokenMetadataComplete {
		t.Fatalf("metadata=%+v", detection)
	}
	calls := caller.recordedCalls()
	if len(calls) != 7 {
		t.Fatalf("RPC calls=%d, want getCode plus six bounded probes", len(calls))
	}
	for _, call := range calls {
		if len(call.params) != 2 {
			t.Fatalf("%s params=%v", call.method, call.params)
		}
		block, ok := call.params[1].(map[string]any)
		if !ok || block["blockHash"] != job.BlockHash.String() || block["requireCanonical"] != true {
			t.Fatalf("%s block reference=%v", call.method, call.params[1])
		}
	}
}

func TestPoolTokenDetectorPinsOneEndpointPerBlockAndRotatesBetweenBlocks(t *testing.T) {
	t.Parallel()
	newCaller := func() *fakeTokenRPCCaller {
		return &fakeTokenRPCCaller{handler: func(method string, _ []any) ([]byte, error) {
			if method == "eth_getCode" {
				return []byte{0x60, 0x00}, nil
			}
			return nil, &ethrpc.RPCError{Code: 3, Message: "execution reverted"}
		}}
	}
	first, second := newCaller(), newCaller()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		{Name: "state-a", Client: first, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}},
		{Name: "state-b", Client: second, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}},
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	detector, err := NewPoolTokenDetector(pool, TokenProbeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[Address]TokenLogEvidence{
		testAddress(1): {ERC20: true},
		testAddress(2): {ERC721Or1155: true},
	}
	jobs := []Job{
		{ID: "block-one", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(101), BlockNumber: 101},
		{ID: "block-two", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(102), BlockNumber: 102},
	}
	if _, err := detector.DetectBlock(t.Context(), jobs[0], evidence); err != nil {
		t.Fatal(err)
	}
	if got := len(first.recordedCalls()); got != 14 || len(second.recordedCalls()) != 0 {
		t.Fatalf("first block calls state-a=%d state-b=%d, want 14 and 0", got, len(second.recordedCalls()))
	}
	if _, err := detector.DetectBlock(t.Context(), jobs[1], evidence); err != nil {
		t.Fatal(err)
	}
	if got := len(first.recordedCalls()); got != 14 || len(second.recordedCalls()) != 14 {
		t.Fatalf("two block calls state-a=%d state-b=%d, want 14 each", got, len(second.recordedCalls()))
	}
	for index, caller := range []*fakeTokenRPCCaller{first, second} {
		for _, call := range caller.recordedCalls() {
			block, ok := call.params[1].(map[string]any)
			if !ok || block["blockHash"] != jobs[index].BlockHash.String() || block["requireCanonical"] != true {
				t.Fatalf("endpoint %d call %s block selector=%#v", index, call.method, call.params[1])
			}
		}
	}
}

func TestRPCTokenDetectorRecognizesERC721MintedDuringConstructorAtBlockEnd(t *testing.T) {
	t.Parallel()
	supportsSelector := SignatureSelector("supportsInterface(bytes4)")
	caller := &fakeTokenRPCCaller{handler: func(method string, params []any) ([]byte, error) {
		if method == "eth_getCode" {
			// A constructor-emitted mint is processed after the block is complete,
			// so the block-hash state must expose the deployed runtime code.
			return []byte{0x60, 0x01}, nil
		}
		request := params[0].(map[string]any)
		encoded, _ := ethrpc.ParseData(request["data"].(string))
		input, _ := encoded.Bytes()
		if len(input) == 36 && bytes.Equal(input[:4], supportsSelector[:]) {
			if bytes.Equal(input[4:8], []byte{0x80, 0xac, 0x58, 0xcd}) {
				return wordBytes(uintWord(1)), nil
			}
			return wordBytes(uintWord(0)), nil
		}
		return nil, &ethrpc.RPCError{Code: 3, Message: "execution reverted"}
	}}
	detector, err := NewRPCTokenDetector(caller, TokenProbeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	job := Job{ID: "constructor-mint", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(200), BlockNumber: 200}
	detection, err := detector.Detect(t.Context(), TokenDetectionRequest{
		Job: job, Address: testAddress(200), Evidence: TokenLogEvidence{ERC721: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detection.Standard != TokenERC721 || detection.Confidence != ConfidenceHigh {
		t.Fatalf("constructor-minted contract detection=%+v", detection)
	}
}

func TestRPCTokenDetectorReturnsTransportErrorsForRetry(t *testing.T) {
	t.Parallel()
	retryable := errors.New("RPC transport unavailable")
	caller := &fakeTokenRPCCaller{handler: func(method string, _ []any) ([]byte, error) {
		if method == "eth_getCode" {
			return []byte{0x60}, nil
		}
		return nil, retryable
	}}
	detector, err := NewRPCTokenDetector(caller, TokenProbeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = detector.Detect(context.Background(), TokenDetectionRequest{
		Job:     Job{ID: "retry", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(1), BlockNumber: 1},
		Address: testAddress(1), Evidence: TokenLogEvidence{ERC20: true},
	})
	if !errors.Is(err, retryable) {
		t.Fatalf("error=%v, want original retryable transport error", err)
	}
	if strings.Contains(err.Error(), retryable.Error()) {
		t.Fatalf("retryable error leaked hostile RPC text: %v", err)
	}
	var classified stageError
	if errors.As(err, &classified) {
		t.Fatalf("transport error was incorrectly made terminal: %+v", classified)
	}
}

func TestRPCTokenDetectorClassifiesExactStateCapabilityGaps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		method  string
		rpcErr  error
		message string
	}{
		{
			name: "missing getCode method", method: "eth_getCode",
			rpcErr:  &ethrpc.RPCError{Code: -32601, Message: "method not found"},
			message: "EIP-1898 block hash is unavailable",
		},
		{
			name: "invalid block-hash selector", method: "eth_getCode",
			rpcErr:  &ethrpc.RPCError{Code: -32602, Message: "invalid argument"},
			message: "cannot serve the exact block-hash state",
		},
		{
			name: "missing trie node", method: "eth_getCode",
			rpcErr:  &ethrpc.RPCError{Code: -32000, Message: "missing trie node 0xsecret"},
			message: "cannot serve the exact block-hash state",
		},
		{
			name: "pruned historical state", method: "eth_call",
			rpcErr:  &ethrpc.RPCError{Code: -32000, Message: "historical state was pruned"},
			message: "cannot serve the exact block-hash state",
		},
		{
			name: "header not found", method: "eth_call",
			rpcErr:  &ethrpc.RPCError{Code: -32000, Message: "header not found"},
			message: "cannot serve the exact block-hash state",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeTokenRPCCaller{handler: func(method string, _ []any) ([]byte, error) {
				if method == test.method {
					return nil, test.rpcErr
				}
				if method == "eth_getCode" {
					return []byte{0x60}, nil
				}
				return wordBytes(uintWord(0)), nil
			}}
			detector, err := NewRPCTokenDetector(caller, TokenProbeLimits{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = detector.Detect(t.Context(), TokenDetectionRequest{
				Job: Job{
					ID: "capability-" + test.name, Stage: TokenStage,
					ChainID: "1", BlockHash: uintWord(2), BlockNumber: 2,
				},
				Address: testAddress(2), Evidence: TokenLogEvidence{ERC20: true},
			})
			var classified stageError
			if !errors.As(err, &classified) || classified.kind != "unavailable" ||
				!strings.Contains(classified.Error(), test.message) {
				t.Fatalf("error=%#v, want unavailable containing %q", err, test.message)
			}
			if strings.Contains(err.Error(), "0xsecret") {
				t.Fatalf("capability error leaked RPC text: %v", err)
			}
		})
	}
}

func TestRPCTokenDetectorClassifiesMalformedCodeAsPermanent(t *testing.T) {
	t.Parallel()
	caller := tokenRPCCallerFunc(func(_ context.Context, method string, _ []any, result any) error {
		if method != "eth_getCode" {
			return errors.New("unexpected token RPC method")
		}
		encoded, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("unexpected token RPC result")
		}
		*encoded = ethrpc.Data("0xzz")
		return nil
	})
	detector, err := NewRPCTokenDetector(caller, TokenProbeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = detector.Detect(t.Context(), TokenDetectionRequest{
		Job: Job{
			ID: "malformed-code", Stage: TokenStage,
			ChainID: "1", BlockHash: uintWord(3), BlockNumber: 3,
		},
		Address: testAddress(3), Evidence: TokenLogEvidence{ERC20: true},
	})
	var classified stageError
	if !errors.As(err, &classified) || classified.kind != "permanent" ||
		classified.Error() != "eth_getCode returned invalid bytecode" {
		t.Fatalf("error=%#v, want permanent malformed-wire classification", err)
	}
}

func TestClassifyTokenRequiresCompatibleSignals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		evidence   TokenLogEvidence
		probes     tokenProbeResults
		standard   TokenStandard
		confidence Confidence
	}{
		{
			name: "both NFT interfaces are unreliable", evidence: TokenLogEvidence{ERC721: true},
			probes: tokenProbeResults{erc721: true, erc1155: true}, standard: TokenStandardUnknown, confidence: ConfidenceGuess,
		},
		{
			name: "ERC721 interface conflicts with ERC20 layout", evidence: TokenLogEvidence{ERC20: true},
			probes: tokenProbeResults{erc721: true}, standard: TokenStandardUnknown, confidence: ConfidenceGuess,
		},
		{
			name: "ERC721 interface resolves shared log layout", evidence: TokenLogEvidence{ERC721Or1155: true},
			probes: tokenProbeResults{erc721: true}, standard: TokenERC721, confidence: ConfidenceHigh,
		},
		{
			name: "ERC1155 interface agrees with exact layout", evidence: TokenLogEvidence{ERC1155: true},
			probes: tokenProbeResults{erc1155: true}, standard: TokenERC1155, confidence: ConfidenceHigh,
		},
		{
			name: "ERC20 needs supply and another valid call", evidence: TokenLogEvidence{ERC20: true},
			probes: tokenProbeResults{totalSupplyOK: true, symbolOK: true}, standard: TokenERC20, confidence: ConfidenceHigh,
		},
		{
			name: "fake Transfer log layout alone remains unknown", evidence: TokenLogEvidence{ERC20: true},
			standard: TokenStandardUnknown, confidence: ConfidenceGuess,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			standard, confidence := classifyToken(test.evidence, test.probes)
			if standard != test.standard || confidence != test.confidence {
				t.Fatalf("classification=(%s,%s), want (%s,%s)", standard, confidence, test.standard, test.confidence)
			}
		})
	}
}

func TestDecodeTokenStringRejectsUnboundedOrNonCanonicalABI(t *testing.T) {
	t.Parallel()
	if value, ok := decodeTokenString(encodeDynamicBytes([]byte("Token")), 16); !ok || value != "Token" {
		t.Fatalf("valid dynamic string=(%q,%v)", value, ok)
	}
	if value, ok := decodeTokenString(append([]byte("TOK"), make([]byte, 29)...), 16); !ok || value != "TOK" {
		t.Fatalf("valid bytes32 string=(%q,%v)", value, ok)
	}
	wrongOffset := encodeDynamicBytes([]byte("Token"))
	wrongOffset[31] = 64
	nonzeroPadding := encodeDynamicBytes([]byte("Token"))
	nonzeroPadding[len(nonzeroPadding)-1] = 1
	for name, encoded := range map[string][]byte{
		"oversize":        encodeDynamicBytes([]byte(strings.Repeat("x", 17))),
		"wrong offset":    wrongOffset,
		"nonzero padding": nonzeroPadding,
		"invalid UTF-8":   encodeDynamicBytes([]byte{0xff}),
	} {
		if value, ok := decodeTokenString(encoded, 16); ok {
			t.Errorf("%s decoded as %q", name, value)
		}
	}
}

func TestPostgresTokenProcessorDetectsAddressOnceAndPersistsUnknown(t *testing.T) {
	t.Parallel()
	job := Job{ID: "detect-block", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(77), BlockNumber: 77}
	contract := testAddress(0x77)
	from, to := testAddress(1), testAddress(2)
	firstHash, secondHash := uintWord(771), uintWord(772)
	rows := [][]driver.Value{
		storedERC20Transfer(job, contract, from, to, firstHash, 0, 5),
		storedERC20Transfer(job, contract, from, to, secondHash, 1, 7),
	}
	var mu sync.Mutex
	logQueries, detectorCalls, contractWrites, eventWrites, deltaWrites, stageWrites, journalWrites := 0, 0, 0, 0, 0, 0, 0
	detectionCodeHash := uintWord(7700)
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "SELECT EXISTS"):
				return &fakeSQLRows{columns: []string{"exists"}, values: [][]driver.Value{{true}}}, nil
			case strings.Contains(query, "FOR KEY SHARE"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "FROM logs"):
				logQueries++
				return &fakeSQLRows{columns: []string{"log_index", "tx_hash", "address", "raw"}, values: rows}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "INSERT INTO token_contracts"):
				contractWrites++
				if !strings.Contains(query, "ELSE current.confidence") || !strings.Contains(query, "current.standard = 'unknown'") {
					t.Errorf("token upsert can downgrade confidence or cannot upgrade unknown:\n%s", query)
				}
				if arguments[0].Value != job.ChainID || !bytes.Equal(arguments[1].Value.([]byte), contract[:]) ||
					!bytes.Equal(arguments[2].Value.([]byte), detectionCodeHash[:]) || arguments[3].Value != string(TokenStandardUnknown) ||
					arguments[4].Value != string(ConfidenceGuess) || arguments[10].Value != "77" ||
					!bytes.Equal(arguments[11].Value.([]byte), job.BlockHash[:]) {
					t.Errorf("token contract arguments=%+v", arguments)
				}
			case strings.Contains(query, "INSERT INTO token_events"):
				eventWrites++
			case strings.Contains(query, "INSERT INTO token_balance_deltas"):
				deltaWrites++
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWrites++
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWrites++
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
			return driver.RowsAffected(1), nil
		},
	}
	detector := TokenDetectorFunc(func(_ context.Context, request TokenDetectionRequest) (TokenDetection, error) {
		mu.Lock()
		defer mu.Unlock()
		detectorCalls++
		if request.Job != job || request.Address != contract || !request.Evidence.ERC20 ||
			request.Evidence.ERC721 || request.Evidence.ERC1155 || request.Evidence.ERC721Or1155 {
			t.Errorf("detection request=%+v", request)
		}
		return TokenDetection{
			Standard: TokenStandardUnknown, Confidence: ConfidenceGuess, CodeHash: detectionCodeHash,
			MetadataState: TokenMetadataUnavailable,
		}, nil
	})
	processor, err := NewPostgresTokenProcessorWithDetector(openFakeSQLDB(t, backend), detector)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if result.State != ResultComplete || result.Details["contracts"] != "1" || result.Details["events"] != "2" ||
		logQueries != 2 || detectorCalls != 1 || contractWrites != 1 || eventWrites != 2 || deltaWrites != 4 || stageWrites != 1 || journalWrites != 1 {
		t.Fatalf("result=%+v log_queries=%d detector=%d contracts=%d events=%d deltas=%d stage=%d journal=%d",
			result, logQueries, detectorCalls, contractWrites, eventWrites, deltaWrites, stageWrites, journalWrites)
	}
}

func TestPostgresTokenProcessorDoesNotOpenTransactionOnDetectorTransportError(t *testing.T) {
	t.Parallel()
	job := Job{ID: "detect-retry", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(88), BlockNumber: 88}
	contract := testAddress(0x88)
	transactionHash := uintWord(880)
	retryable := errors.New("token RPC timed out")
	var begins atomic.Int64
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "SELECT EXISTS"):
				return &fakeSQLRows{columns: []string{"exists"}, values: [][]driver.Value{{true}}}, nil
			case strings.Contains(query, "FROM logs"):
				return &fakeSQLRows{
					columns: []string{"log_index", "tx_hash", "address", "raw"},
					values: [][]driver.Value{storedERC20Transfer(
						job, contract, testAddress(1), testAddress(2), transactionHash, 0, 1,
					)},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec:  func(string, []driver.NamedValue) (driver.Result, error) { return nil, errors.New("unexpected exec") },
		begin: func() { begins.Add(1) },
	}
	processor, err := NewPostgresTokenProcessorWithDetector(openFakeSQLDB(t, backend), TokenDetectorFunc(
		func(context.Context, TokenDetectionRequest) (TokenDetection, error) {
			return TokenDetection{}, retryable
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	_, err = processor.Process(context.Background(), job)
	if !errors.Is(err, retryable) || begins.Load() != 0 {
		t.Fatalf("error=%v begin_count=%d", err, begins.Load())
	}
}

func storedERC20Transfer(job Job, contract, from, to Address, transactionHash Word, logIndex, amount uint64) []driver.Value {
	raw := fmt.Sprintf(`{
		"removed":false,"logIndex":"0x%x","transactionIndex":"0x%x",
		"transactionHash":%q,"blockHash":%q,"blockNumber":"0x%x",
		"address":%q,"data":%q,"topics":[%q,%q,%q]
	}`,
		logIndex, logIndex, transactionHash.String(), job.BlockHash.String(), job.BlockNumber,
		contract.String(), uintWord(amount).String(), topicTransfer.String(), addressWord(from).String(), addressWord(to).String(),
	)
	return []driver.Value{int64(logIndex), transactionHash[:], contract[:], []byte(raw)}
}
