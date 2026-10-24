//go:build runtimee2e

package runtimee2e

import (
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"testing"
)

type harness struct {
	pushURL       string
	pushHTTP      *http.Client
	pushInput     gen.PushSubscriptionInput
	t             *testing.T
	root          string
	mode          string
	phase         string
	project       *testcompose.Project
	apiService    string
	baseURL       string
	rpc           *rpc.Client
	db            *pgxpool.Pool
	http          *http.Client
	artifacts     string
	fixture       fixture
	baseTimestamp uint64
	rpcProxy      *receiptProxy
	ensRPC        *ensMainnetRPC
	secrets       []string
}
