package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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

type testCaller struct{}

func (testCaller) Call(_ context.Context, method string, _ []any, result any) error {
	value := any("0x0")
	switch method {
	case "eth_getBalance":
		value = "0xde0b6b3a7640000"
	case "eth_getTransactionCount":
		value = "0x2"
	case "eth_getCode":
		value = "0x6000"
	}
	encoded, _ := json.Marshal(value)
	return json.Unmarshal(encoded, result)
}

type failingStateCaller struct{ err error }

func (caller failingStateCaller) Call(context.Context, string, []any, any) error { return caller.err }

func (caller testCaller) BatchCall(ctx context.Context, elements []ethrpc.BatchElem) error {
	for index := range elements {
		elements[index].Error = caller.Call(ctx, elements[index].Method, elements[index].Params, elements[index].Result)
	}
	return nil
}

func TestReaderQueriesFixedCanonicalState(t *testing.T) {
	t.Parallel()
	hash := testStateHash(1)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: testCaller{},
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
	if model.Address != "0x000000000000000000000000000000000000dEaD" || model.AtBlock != hash.String() {
		t.Fatalf("unexpected identity: %+v", model)
	}
	if model.CodeHash == nil || model.Completeness.State != gen.StageStateComplete {
		t.Fatalf("missing code/completeness: %+v", model)
	}
}

func TestClassifyDelegatedEOA(t *testing.T) {
	t.Parallel()
	typeValue, hash, err := classifyCode("0xef01000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if typeValue != gen.AddressSummaryTypeDelegatedEoa || hash == nil {
		t.Fatalf("type=%q hash=%v", typeValue, hash)
	}
	typeValue, hash, err = classifyCode("0x")
	if err != nil || typeValue != gen.AddressSummaryTypeEoa || hash != nil {
		t.Fatalf("empty code type=%q hash=%v err=%v", typeValue, hash, err)
	}
}

func TestReaderReportsUnsupportedFixedBlockStateAsUnavailable(t *testing.T) {
	t.Parallel()
	secret := "https://operator:rpc-secret@example.invalid"
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "legacy-state", Client: failingStateCaller{err: &ethrpc.RPCError{Code: -32602, Message: secret}},
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

func testStateHash(value byte) ethrpc.Hash {
	bytes := make([]byte, 32)
	bytes[31] = value
	hash, err := ethrpc.ParseHash(ethrpc.DataFromBytes(bytes).String())
	if err != nil {
		panic(err)
	}
	return hash
}
