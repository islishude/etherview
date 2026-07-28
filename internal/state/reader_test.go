package state

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type testBaseReader struct{}

func (testBaseReader) Status(context.Context) (httpapi.StatusSnapshot, error) {
	return httpapi.StatusSnapshot{}, nil
}
func (testBaseReader) Blocks(context.Context, string, int) ([]gen.Block, string, error) {
	return nil, "", nil
}
func (testBaseReader) Block(context.Context, string) (gen.Block, error) { return gen.Block{}, nil }
func (testBaseReader) Transactions(context.Context, string, int) ([]gen.Transaction, string, error) {
	return nil, "", nil
}
func (testBaseReader) Transaction(context.Context, string) (gen.Transaction, error) {
	return gen.Transaction{}, nil
}
func (testBaseReader) Address(context.Context, string) (gen.AddressSummary, error) {
	return gen.AddressSummary{}, nil
}
func (testBaseReader) Search(context.Context, string, string, int) ([]gen.SearchResult, string, error) {
	return nil, "", nil
}

type testCanonical struct {
	reference CanonicalRef
	canonical bool
}

func (c testCanonical) Tip(context.Context) (CanonicalRef, error) { return c.reference, nil }
func (c testCanonical) IsCanonical(context.Context, CanonicalRef) (bool, error) {
	return c.canonical, nil
}

type testStateRPC struct {
	method     string
	params     []any
	balance    *big.Int
	nonce      uint64
	code       []byte
	callResult []byte
	err        error
}

func (service *testStateRPC) GetBalance(_ context.Context, address common.Address, selector rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	service.method = "eth_getBalance"
	service.params = []any{address, selector}
	if service.err != nil {
		return nil, service.err
	}
	value := service.balance
	if value == nil {
		value = big.NewInt(1_000_000_000_000_000_000)
	}
	result := hexutil.Big(*value)
	return &result, nil
}

func (service *testStateRPC) GetTransactionCount(_ context.Context, address common.Address, selector rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	service.method = "eth_getTransactionCount"
	service.params = []any{address, selector}
	if service.err != nil {
		return 0, service.err
	}
	nonce := service.nonce
	if nonce == 0 {
		nonce = 2
	}
	return hexutil.Uint64(nonce), nil
}

func (service *testStateRPC) GetCode(_ context.Context, address common.Address, selector rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	service.method = "eth_getCode"
	service.params = []any{address, selector}
	if service.err != nil {
		return nil, service.err
	}
	code := service.code
	if code == nil {
		code = []byte{0x60, 0}
	}
	return hexutil.Bytes(code), nil
}

func (service *testStateRPC) Call(_ context.Context, call map[string]any, selector rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	service.method = "eth_call"
	service.params = []any{call, selector}
	if service.err != nil {
		return nil, service.err
	}
	return hexutil.Bytes(service.callResult), nil
}

type testRPCError struct {
	code    int
	message string
}

func (err *testRPCError) Error() string  { return err.message }
func (err *testRPCError) ErrorCode() int { return err.code }

func newTestStateClient(t *testing.T, service *testStateRPC) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func newTestStateEndpoint(t *testing.T, service *testStateRPC) *ethrpc.Endpoint {
	t.Helper()
	return &ethrpc.Endpoint{Name: "state", Client: newTestStateClient(t, service)}
}

func TestReaderQueriesFixedCanonicalState(t *testing.T) {
	t.Parallel()
	hash := testStateHash(1)
	service := &testStateRPC{}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{
		Base: testBaseReader{}, Pool: pool,
		Canonical: testCanonical{reference: CanonicalRef{Number: 10, Hash: hash}, canonical: true},
	}
	model, err := reader.Address(context.Background(), "0x000000000000000000000000000000000000dead")
	if err != nil {
		t.Fatal(err)
	}
	if model.Balance != "1000000000000000000" || model.Nonce != "2" || model.Type != gen.AddressSummaryTypeContract {
		t.Fatalf("unexpected model: %+v", model)
	}
	if model.Address != "0x000000000000000000000000000000000000dEaD" || model.AtBlock != hash.Hex() {
		t.Fatalf("unexpected identity: %+v", model)
	}
	if model.CodeHash == nil || model.Completeness.State != gen.StageStateComplete {
		t.Fatalf("missing code/completeness: %+v", model)
	}
}

func TestReaderRejectsStateObservedAcrossCanonicalChange(t *testing.T) {
	t.Parallel()
	service := &testStateRPC{}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{
		Base: testBaseReader{}, Pool: pool,
		Canonical: testCanonical{
			reference: CanonicalRef{Number: 169, Hash: testStateHash(169)},
			canonical: false,
		},
	}
	if _, err := reader.Address(t.Context(), "0x000000000000000000000000000000000000dead"); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("address state error = %v, want not ready", err)
	}
}

func TestClassifyDelegatedEOA(t *testing.T) {
	t.Parallel()
	typeValue, hash, err := classifyCode(hexutil.MustDecode("0xef01000000000000000000000000000000000000000000"))
	if err != nil {
		t.Fatal(err)
	}
	if typeValue != gen.AddressSummaryTypeDelegatedEoa || hash == nil {
		t.Fatalf("type=%q hash=%v", typeValue, hash)
	}
	typeValue, hash, err = classifyCode(hexutil.Bytes{})
	if err != nil || typeValue != gen.AddressSummaryTypeEoa || hash != nil {
		t.Fatalf("empty code type=%q hash=%v err=%v", typeValue, hash, err)
	}
}

func TestReaderReportsUnsupportedFixedBlockStateAsUnavailable(t *testing.T) {
	t.Parallel()
	secret := "https://operator:rpc-secret@example.invalid"
	service := &testStateRPC{err: &testRPCError{code: -32602, message: secret}}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "legacy-state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reader := &Reader{
		Base: testBaseReader{}, Pool: pool,
		Canonical: testCanonical{reference: CanonicalRef{Number: 10, Hash: testStateHash(1)}, canonical: true},
	}
	_, err = reader.Address(context.Background(), "0x000000000000000000000000000000000000dead")
	if !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	var capability CapabilityError
	if !errors.As(err, &capability) || capability.Code != "rpc_failure" || strings.Contains(err.Error(), secret) {
		t.Fatalf("capability=%+v err=%q", capability, err)
	}
}

func testStateHash(value byte) common.Hash {
	var hash common.Hash
	hash[common.HashLength-1] = value
	return hash
}
