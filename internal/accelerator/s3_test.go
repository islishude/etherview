package accelerator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3BlobStoreRoundTripAndChecksumValidation(t *testing.T) {
	t.Parallel()
	type object struct {
		body     []byte
		checksum string
	}
	var mu sync.Mutex
	objects := make(map[string]object)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch request.Method {
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read PUT: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			objects[request.URL.Path] = object{body: body, checksum: request.Header.Get(blobChecksumMetadata)}
			w.Header().Set("ETag", `"cache-etag"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			stored, ok := objects[request.URL.Path]
			if !ok {
				http.Error(w, `<Error><Code>NoSuchKey</Code></Error>`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", stringInt(len(stored.body)))
			w.Header().Set("Last-Modified", time.Unix(1, 0).UTC().Format(http.TimeFormat))
			w.Header().Set(blobChecksumMetadata, stored.checksum)
			_, _ = w.Write(stored.body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	store, err := NewS3BlobStore(server.URL, S3Options{
		Bucket: "cache", Prefix: "test", Region: "us-east-1", PathStyle: true,
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"trace":"large"}`)
	if err := store.Put(context.Background(), "trace/v1/object.json", payload); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Get(context.Background(), "trace/v1/object.json")
	if err != nil || !found || string(got) != string(payload) {
		t.Fatalf("got=%q found=%v err=%v", got, found, err)
	}
	mu.Lock()
	item := objects["/cache/test/trace/v1/object.json"]
	item.checksum = strings.Repeat("0", sha256.Size*2)
	objects["/cache/test/trace/v1/object.json"] = item
	mu.Unlock()
	if _, found, err := store.Get(context.Background(), "trace/v1/object.json"); err == nil || found {
		t.Fatalf("corrupt checksum found=%v err=%v", found, err)
	}
}

func TestS3BlobStoreMissLimitAndOutageAreBounded(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "missing") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
			return
		}
		body := []byte(strings.Repeat("x", 33))
		digest := sha256.Sum256(body)
		w.Header().Set("Content-Length", stringInt(len(body)))
		w.Header().Set("Last-Modified", time.Unix(1, 0).UTC().Format(http.TimeFormat))
		w.Header().Set(blobChecksumMetadata, hex.EncodeToString(digest[:]))
		_, _ = w.Write(body)
	}))
	store, err := NewS3BlobStore(server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: 100 * time.Millisecond, MaxObjectBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(context.Background(), "missing"); err != nil || found {
		t.Fatalf("miss found=%v err=%v", found, err)
	}
	if _, found, err := store.Get(context.Background(), "oversized"); err == nil || found {
		t.Fatalf("oversized found=%v err=%v", found, err)
	}
	if err := store.Put(context.Background(), "oversized", []byte(strings.Repeat("x", 33))); err == nil {
		t.Fatal("oversized write was accepted")
	}
	server.Close()
	started := time.Now()
	if _, found, err := store.Get(context.Background(), "outage"); err == nil || found {
		t.Fatalf("outage found=%v err=%v", found, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("outage fallback was not bounded: %s", elapsed)
	}
}

func TestS3BlobStoreRejectsUnscopedKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", "/absolute", "../escape", "a/../escape"} {
		if _, err := (&S3BlobStore{}).objectName(key); err == nil {
			t.Fatalf("key %q was accepted", key)
		}
	}
}

func stringInt(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var reversed [32]byte
	index := len(reversed)
	for value > 0 {
		index--
		reversed[index] = digits[value%10]
		value /= 10
	}
	return string(reversed[index:])
}
