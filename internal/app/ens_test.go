package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/state"
	"github.com/jackc/pgx/v5/pgxpool"
)

type localENSCanonical struct{}

func (localENSCanonical) Tip(context.Context) (state.CanonicalRef, error) {
	return state.CanonicalRef{}, nil
}
func (localENSCanonical) IsCanonical(context.Context, state.CanonicalRef) (bool, error) {
	return true, nil
}

func TestCustomOnlyENSBuilderNeverProbesOfficialRPC(t *testing.T) {
	var requests atomic.Int64
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "forbidden official source", 500)
	}))
	defer forbidden.Close()
	client := rpc.DialInProc(rpc.NewServer())
	defer client.Close()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "local", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Chain.ID = 48815
	cfg.Features.ENS = true
	cfg.ENS.Mode = "custom_only"
	cfg.ENS.OfficialRPCEndpoints = []config.RPCEndpoint{{Name: "must-not-be-probed", URL: forbidden.URL}}
	cfg.ENS.OfficialGateways = []string{forbidden.URL}
	cfg.ENS.Custom.Registry = "0x0000000000000000000000000000000000000001"
	cfg.ENS.Custom.UniversalResolver = "0x0000000000000000000000000000000000000002"
	cfg.ENS.Custom.Gateways = nil
	service, err := newENSService(t.Context(), &pgxpool.Pool{}, cfg, &RPCBuild{Pool: pool}, localENSCanonical{}, nil, nil)
	if err != nil || service == nil {
		t.Fatalf("construct local service: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("official requests: %d", requests.Load())
	}
}
