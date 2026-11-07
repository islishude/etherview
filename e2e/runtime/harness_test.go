//go:build runtimee2e

package runtimee2e

import (
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/islishude/etherview/internal/testdbguard"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"sync"
	"testing"
)

type harness struct {
	statusAdminMu   sync.Mutex
	statusCookie    *http.Cookie
	deadlockGuard   *testdbguard.Guard
	pushURL         string
	pushHTTP        *http.Client
	pushInput       gen.PushSubscriptionInput
	t               *testing.T
	root            string
	mode            string
	phase           string
	project         *testcompose.Project
	apiService      string
	baseURL         string
	rpc             *rpc.Client
	db              *pgxpool.Pool
	http            *http.Client
	artifacts       string
	fixture         fixture
	baseTimestamp   uint64
	rpcProxy        *receiptProxy
	ensTransactions []string
	ensOwner        string
	secrets         []string
}
