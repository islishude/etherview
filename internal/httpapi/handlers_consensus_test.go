package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/userauth"
)

type consensusReaderStub struct {
	calls int
	err   error
}

type blobBlockReader struct {
	fakeReader
	blockIDs []string
}

func (r *blobBlockReader) Block(_ context.Context, id string) (gen.Block, error) {
	r.blockIDs = append(r.blockIDs, id)
	return gen.Block{Number: "9007199254740993"}, r.err
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
	blocks := &blobBlockReader{}
	handler, _, _ := browserVerificationHandler(t)
	handler.cfg.Consensus = cfg.Consensus
	handler.reader, handler.consensusReader = blocks, reader
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		req.Header.Set("Origin", testAuthOrigin)
		req.Header.Set("X-CSRF-Token", "csrf")
		req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
		handler.ServeHTTP(r, req)
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
	if got := response.Header().Get("Content-Disposition"); got != "attachment; filename=blob-9007199254740993-0.bin" {
		t.Fatalf("download filename=%q", got)
	}
	if len(blocks.blockIDs) != 1 || blocks.blockIDs[0] != hash {
		t.Fatalf("block lookup must use the exact hash: %v", blocks.blockIDs)
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

func TestBrowserBlobGETProtection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, mode := range []string{"anonymous", "api_key", "csrf", "origin", "no_origin", "no_provenance", "cross_site", "same_site", "duplicate_origin", "duplicate_site", "accepted", "old"} {
			t.Run(strconv.FormatBool(enabled)+"/"+mode, func(t *testing.T) {
				h, _, _ := browserVerificationHandler(t)
				reader := &consensusReaderStub{}
				h.reader, h.consensusReader = &blobBlockReader{}, reader
				h.cfg.Consensus.Blobs.Enabled = true
				h.cfg.Turnstile.Enabled = enabled
				validations := 0
				h.turnstile = challengeValidator(func(_ context.Context, _, _ string) error {
					validations++
					return nil
				})
				hash := "0x" + strings.Repeat("11", 32)
				req := httptest.NewRequest("GET", "/api/v1/blocks/"+hash+"/blobs/0/content", nil)
				req.Header.Set("Origin", testAuthOrigin)
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("X-CSRF-Token", "csrf")
				if mode != "anonymous" && mode != "api_key" {
					req.AddCookie(&http.Cookie{Name: userauth.SessionCookieName, Value: "session"})
				}
				switch mode {
				case "api_key":
					req.Header.Set("X-API-Key", "not-a-session")
				case "csrf":
					req.Header.Del("X-CSRF-Token")
				case "origin":
					req.Header.Set("Origin", "https://other.example")
				case "no_origin":
					req.Header.Del("Origin")
				case "no_provenance":
					req.Header.Del("Origin")
					req.Header.Del("Sec-Fetch-Site")
				case "cross_site":
					req.Header.Del("Origin")
					req.Header.Set("Sec-Fetch-Site", "cross-site")
				case "same_site":
					req.Header.Set("Sec-Fetch-Site", "same-site")
				case "duplicate_origin":
					req.Header.Add("Origin", testAuthOrigin)
				case "duplicate_site":
					req.Header.Add("Sec-Fetch-Site", "same-origin")
				case "old":
					req.Method = http.MethodPost
				}
				out := httptest.NewRecorder()
				h.ServeHTTP(out, req)
				accepted := mode == "accepted" || mode == "no_origin" || mode == "no_provenance"
				if accepted {
					if out.Code != 200 || reader.calls != 1 || validations != 0 {
						t.Fatalf("status=%d reads=%d", out.Code, reader.calls)
					}
				} else if out.Code == 200 || reader.calls != 0 || validations != 0 {
					t.Fatalf("rejected request accessed content: status=%d reads=%d validations=%d", out.Code, reader.calls, validations)
				}
			})
		}
	}
}
