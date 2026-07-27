package enrich

import "context"

// rpcCaller is the narrow go-ethereum RPC client surface used by enrichment.
// Production supplies *rpc.Client directly; the interface only keeps focused
// tests independent from an HTTP server.
type rpcCaller interface {
	CallContext(context.Context, any, string, ...any) error
}
