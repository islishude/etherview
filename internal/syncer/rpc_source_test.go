package syncer

import (
	"context"
	"strings"
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
