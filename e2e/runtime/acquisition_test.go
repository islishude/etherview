//go:build runtimee2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (h *harness) assertSyncOfflineReplay(ctx context.Context) {
	h.t.Helper()
	h.compose(ctx, "stop", "sync")
	defer h.compose(ctx, "start", "sync")
	execution, beacon := h.rpcProxy.executionRequests.Load(), h.rpcProxy.beaconRequests.Load()
	hash := strings.TrimPrefix(h.fixture.blockOneHash, "0x")
	const publication = `SELECT job_generation FROM published_block_stage_results WHERE chain_id=1 AND stage='trace' AND block_hash=decode($1,'hex') AND state='complete'`
	var before int64
	if err := h.db.QueryRow(ctx, publication, hash).Scan(&before); err != nil {
		h.t.Fatal(err)
	}
	h.compose(ctx, "exec", "-T", "maintenance", "/etherview", "reindex", "--config=/etc/etherview/config.yaml", "--stage", "trace", "--from", "1", "--to", "1", "--reason", "offline input replay")
	waitFor(h.t, ctx, "trace replay from retained inputs", func() (bool, string, error) {
		var current int64
		err := h.db.QueryRow(ctx, publication, hash).Scan(&current)
		return current > before, strconv.FormatInt(current, 10), err
	})
	response := h.requireHTTPStatus(ctx, "/api/v1/addresses/0xc0ffee0000000000000000000000000000000001", http.StatusServiceUnavailable)
	_ = response.Body.Close()
	if h.rpcProxy.executionRequests.Load() != execution || h.rpcProxy.beaconRequests.Load() != beacon {
		h.t.Fatal("node received requests while sync was stopped")
	}
}

func rpcRequestMethod(body []byte) string {
	var request struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	return request.Method
}
