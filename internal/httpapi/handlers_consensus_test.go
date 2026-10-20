package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/publicquery"
)

type consensusReaderStub struct {
	calls int
	err   error
}

func (*consensusReaderStub) BlockConsensus(context.Context, string) (gen.ConsensusSummary, error) {
	return gen.ConsensusSummary{BlockHash: "0x" + strings.Repeat("11", 32), State: gen.ConsensusStateComplete, Slot: new("9007199254740993")}, nil
}
func (*consensusReaderStub) BlockBlobs(context.Context, string) (gen.BlockBlobs, error) {
	return gen.BlockBlobs{BlockHash: "0x" + strings.Repeat("11", 32), State: gen.ConsensusStatePending, Items: []gen.BlobInfo{}}, nil
}
func (r *consensusReaderStub) BlobContent(_ context.Context, hash string, index uint64) ([]byte, error) {
	r.calls++
	return bytes.Repeat([]byte{7}, 131072), r.err
}
func TestConsensusRoutesAndExactBinaryDownload(t *testing.T) {
	reader := &consensusReaderStub{}
	cfg := config.Default()
	cfg.Consensus.Enabled = true
	cfg.Consensus.URL = "http://secret-beacon.internal"
	cfg.Consensus.Headers = map[string]string{"Authorization": "very-private"}
	cfg.Consensus.Blobs.Enabled = true
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}, ConsensusReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil))
		return r
	}
	response := request("/blocks/1/consensus")
	var summary gen.ConsensusResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &summary) != nil || summary.Data.Slot == nil || *summary.Data.Slot != "9007199254740993" {
		t.Fatalf("summary=%s", response.Body)
	}
	response = request("/config")
	if response.Code != 200 || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "beacon.internal") {
		t.Fatalf("config=%s", response.Body)
	}
	hash := "0x" + strings.Repeat("11", 32)
	for _, path := range []string{"/blocks/1/blobs/0/content", "/blocks/" + hash + "/blobs/01/content", "/blocks/" + hash + "/blobs/-1/content"} {
		if response = request(path); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid path status=%d", response.Code)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid request reached archive")
	}
	response = request("/blocks/" + hash + "/blobs/0/content")
	if response.Code != 200 || response.Body.Len() != 131072 || response.Header().Get("Content-Type") != "application/octet-stream" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download=%d %v", response.Code, response.Header())
	}
	reader.err = publicquery.NewCapabilityUnavailableError("blob", "unavailable", "blob_storage_integrity")
	response = request("/blocks/" + hash + "/blobs/0/content")
	if response.Code != 503 || !strings.Contains(response.Body.String(), "blob_storage_integrity") {
		t.Fatalf("integrity error=%d %s", response.Code, response.Body)
	}
}

func TestHomeConsensusFreshnessAgesWithoutMutatingPublication(t *testing.T) {
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	overview := &gen.ConsensusOverview{State: gen.ConsensusStateComplete, BlobState: gen.ConsensusStateDisabled, StaleAt: &expired}
	data := gen.HomeSnapshot{Status: gen.Status{Consensus: overview}, Blocks: []gen.Block{}, Transactions: []gen.Transaction{}}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	publication := HomePublication{EventID: 1, Data: data, EncodedData: encoded}
	raw, err := handler.encodeHomeSnapshotResponse(httptest.NewRequest(http.MethodGet, "/api/v1/home", nil), publication)
	if err != nil {
		t.Fatal(err)
	}
	var response gen.HomeSnapshotResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.Status.Consensus.Stale || publication.Data.Status.Consensus.Stale {
		t.Fatalf("freshness or immutability failed: %s", raw)
	}
}
