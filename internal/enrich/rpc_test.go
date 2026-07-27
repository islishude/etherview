package enrich

import (
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
)

type testRPCError struct {
	code    int
	message string
}

func (err testRPCError) Error() string  { return err.message }
func (err testRPCError) ErrorCode() int { return err.code }

func inProcessRPCClient(t *testing.T, services map[string]any) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	for namespace, service := range services {
		if err := server.RegisterName(namespace, service); err != nil {
			t.Fatalf("register %s RPC test service: %v", namespace, err)
		}
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}
