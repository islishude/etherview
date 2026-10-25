//go:build runtimee2e && verificatione2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const verificationE2ETimeout = 30 * time.Minute

func verificationEtherscanRequest(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, method string,
	values url.Values,
) (json.RawMessage, error) {
	t.Helper()
	values.Set("chainid", "1")
	values.Set("module", "contract")
	var request *http.Request
	var err error
	if method == http.MethodPost {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api", strings.NewReader(values.Encode()))
		if err == nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api?"+values.Encode(), nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", apiKey)
	response, err := h.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope etherscanEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	var result string
	if response.StatusCode != http.StatusOK || envelope.Status != "1" {
		if err := json.Unmarshal(envelope.Result, &result); err != nil {
			return nil, fmt.Errorf("decode Etherscan error result: %w", err)
		}
		switch result {
		case "proxy implementation source code not verified":
			return nil, errHardhatImplementationUnverified
		case "proxy verification target unavailable":
			return nil, errHardhatProxyTargetUnavailable
		default:
			return nil, fmt.Errorf("Etherscan %s: status=%s message=%s result=%s",
				values.Get("action"), envelope.Status, envelope.Message, result)
		}
	}
	return envelope.Result, nil
}

func waitVerificationCanonicalTip(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	latest := h.latestBlock(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, latest.Number), latest.Hash)
}

func waitVerificationContractCode(t *testing.T, ctx context.Context, h *harness, address string) {
	t.Helper()
	waitFor(t, ctx, "canonical contract code "+address, func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM contract_code_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.address = $1
			  AND observation.canonical
			  AND octet_length(observation.code) > 0`, common.HexToAddress(address).Bytes()).Scan(&count)
		return err == nil && count > 0, fmt.Sprintf("observations=%d", count), err
	})
}

func verificationImageID(t *testing.T, ctx context.Context) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, dockerCommand(), "image", "inspect", "--format", "{{.Id}}", valueOrDefault("IMAGE", "etherview:local")).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// Only the parent calls this, after both children and all their cleanups finish.
func writeApplicationResult[T any](t *testing.T, ctx context.Context, root, suite, imageID string, results [2]T) {
	t.Helper()
	if ctx.Err() != nil || t.Failed() || verificationImageID(t, ctx) != imageID {
		t.Fatal("application evidence incomplete or image changed")
	}
	raw, err := json.Marshal(map[string]any{
		"schema": "etherview-application-result-v1", "role": suite,
		"native_linux": runtime.GOOS == "linux", "architecture": runtime.GOARCH,
		"image_id": imageID, "cleanup": true,
		"topologies": map[string]T{"monolith": results[0], "distributed": results[1]},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, ".local/production-results")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, suite+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write application evidence: %v; close: %v", writeErr, closeErr)
	}
}
