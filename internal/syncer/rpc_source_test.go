package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

type hostileHeadRPCError struct {
	code    int
	message string
}

func (err hostileHeadRPCError) Error() string  { return err.message }
func (err hostileHeadRPCError) ErrorCode() int { return err.code }

type hostileHeadRPCService struct{ secret string }

func (service hostileHeadRPCService) BlockNumber(context.Context) (hexutil.Uint64, error) {
	return 0, hostileHeadRPCError{code: -32000, message: service.secret}
}

func TestRPCSourceSanitizesProviderJSONRPCError(t *testing.T) {
	t.Parallel()
	const secret = "sync-provider-secret"
	server := rpc.NewServer()
	if err := server.RegisterName(
		"eth",
		hostileHeadRPCService{secret: secret},
	); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "hostile-head",
		Client:   client,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHead: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := RPCSource{Pool: pool}
	_, err = source.Head(t.Context())
	if err == nil ||
		strings.Contains(err.Error(), secret) ||
		err.Error() != "JSON-RPC error code -32000" {
		t.Fatalf("Head() error = %v", err)
	}
}

type observedHeadRPC struct {
	bundle                     chainbundle.Bundle
	height                     uint64
	headers, bundles, receipts atomic.Int32
}

func (s *observedHeadRPC) BlockNumber(context.Context) (hexutil.Uint64, error) {
	return hexutil.Uint64(s.height), nil
}
func (s *observedHeadRPC) GetBlockByNumber(_ context.Context, tag string, full bool) (json.RawMessage, error) {
	if full || tag != hexutil.EncodeUint64(s.height) {
		return nil, errors.New("expected lightweight exact-height header")
	}
	s.headers.Add(1)
	return s.bundle.RawBlock, nil
}
func (s *observedHeadRPC) GetBlockByHash(_ context.Context, hash common.Hash, full bool) (json.RawMessage, error) {
	if !full || hash != s.bundle.Block.Hash() {
		return nil, errors.New("expected full exact-hash block")
	}
	s.bundles.Add(1)
	return s.bundle.RawBlock, nil
}
func (s *observedHeadRPC) GetBlockReceipts(_ context.Context, hash common.Hash) ([]json.RawMessage, error) {
	if hash != s.bundle.Block.Hash() {
		return nil, errors.New("expected receipts pinned to observed hash")
	}
	s.receipts.Add(1)
	return s.bundle.RawReceipts, nil
}

func TestRPCHeadObservationPinsHeaderBundleAndReceipts(t *testing.T) {
	t.Parallel()
	services := []*observedHeadRPC{
		{bundle: testBundle(7, common.Hash{}, 1), height: 7},
		{bundle: testBundle(7, common.Hash{}, 2), height: 7},
	}
	endpoints := make([]ethrpc.Endpoint, len(services))
	for index, service := range services {
		server := rpc.NewServer()
		if err := server.RegisterName("eth", service); err != nil {
			t.Fatal(err)
		}
		client := rpc.DialInProc(server)
		t.Cleanup(func() { client.Close(); server.Stop() })
		endpoints[index] = ethrpc.Endpoint{Name: fmt.Sprint(index), Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHead: true}}
	}
	pool, err := ethrpc.NewPool(endpoints, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := RPCSource{Pool: pool}
	observation, err := source.ObserveHead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner := services[0]
	if owner.headers.Load() == 0 {
		owner = services[1]
	}
	if owner.bundles.Load() != 0 || owner.receipts.Load() != 0 {
		t.Fatal("lightweight observation fetched a bundle")
	}
	// A different consumer may rotate the pool between observation and fetch.
	if _, err := pool.Acquire(ethrpc.PurposeHead); err != nil {
		t.Fatal(err)
	}
	bundle, err := observation.Fetch(t.Context())
	if err != nil || bundle.Block.Hash() != observation.Reference.Hash || owner.bundles.Load() != 1 || owner.receipts.Load() != 1 {
		t.Fatalf("pinned fetch=%v bundle calls=%d receipt calls=%d", err, owner.bundles.Load(), owner.receipts.Load())
	}
	for _, service := range services {
		if service != owner && service.bundles.Load()+service.receipts.Load() != 0 {
			t.Fatal("fetch switched endpoints")
		}
	}
}

func TestRPCHeadObservationRejectsMismatchedHeight(t *testing.T) {
	t.Parallel()
	server := rpc.NewServer()
	service := &observedHeadRPC{bundle: testBundle(6, common.Hash{}, 1), height: 7}
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() { client.Close(); server.Stop() })
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "wrong-height", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHead: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := RPCSource{Pool: pool}
	if _, err := source.ObserveHead(t.Context()); err == nil || service.bundles.Load() != 0 {
		t.Fatalf("accepted mismatched head: %v", err)
	}
}
