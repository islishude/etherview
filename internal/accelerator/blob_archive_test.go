package accelerator

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestArchiveConditionalCreateAndIndependentReadback(t *testing.T) {
	var mu sync.Mutex
	var stored []byte
	gets := 0
	corrupt := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("archive write can overwrite")
			}
			if stored != nil {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusPreconditionFailed)
				_ = xml.NewEncoder(w).Encode(struct {
					XMLName xml.Name `xml:"Error"`
					Code    string   `xml:"Code"`
				}{Code: "PreconditionFailed"})
				return
			}
			stored, _ = io.ReadAll(r.Body)
			return
		}
		gets++
		body := bytes.Clone(stored)
		if corrupt {
			body[0] ^= 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer server.Close()
	archive, err := NewBlobArchive(t.Context(), server.URL, S3Options{Bucket: "archive", AccessKey: "test", SecretKey: "test", Region: "us-east-1", PathStyle: true, OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, ArchiveBlobBytes)
	digest, err := archive.Create(t.Context(), "content.bin", blob)
	if err != nil {
		t.Fatal(err)
	}
	second, err := archive.Create(t.Context(), "content.bin", blob)
	if err != nil || second != digest {
		t.Fatalf("idempotent create: %v", err)
	}
	mu.Lock()
	if gets != 2 {
		t.Errorf("readbacks=%d", gets)
	}
	corrupt = true
	mu.Unlock()
	if _, err := archive.Create(t.Context(), "content.bin", blob); err != ErrArchiveIntegrity {
		t.Fatalf("corrupt existing object accepted: %v", err)
	}
}
