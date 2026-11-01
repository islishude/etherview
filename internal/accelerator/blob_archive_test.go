package accelerator

import (
	"bytes"
	"encoding/xml"
	"github.com/klauspost/compress/s2"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func TestArchiveS2RoundTripAndBounds(t *testing.T) {
	for _, kind := range []string{"zero", "sparse", "random"} {
		t.Run(kind, func(t *testing.T) {
			blob := make([]byte, ArchiveBlobBytes)
			if kind == "sparse" {
				copy(blob, bytes.Repeat([]byte{7}, 1024))
			}
			if kind == "random" {
				_, _ = rand.New(rand.NewSource(1)).Read(blob)
			}
			var stored []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					stored, _ = io.ReadAll(r.Body)
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(stored)))
				_, _ = w.Write(stored)
			}))
			defer server.Close()
			archive, err := NewBlobArchive(t.Context(), server.URL, S3Options{Bucket: "archive", AccessKey: "test", SecretKey: "test", Region: "us-east-1", PathStyle: true, OperationTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			digest, err := archive.Create(t.Context(), "test.s2", blob)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d -> %d bytes, create/readback %s", kind, len(blob), len(stored), time.Since(start))
			if kind != "random" && len(stored) >= len(blob)/10 {
				t.Fatal("sparse blob did not compress")
			}
			got, err := archive.Read(t.Context(), "test.s2", digest)
			if err != nil || !bytes.Equal(got, blob) {
				t.Fatalf("roundtrip: %v", err)
			}
			valid := bytes.Clone(stored)
			for name, bad := range map[string][]byte{
				"truncated":    valid[:len(valid)/2],
				"oversized":    make([]byte, s2.MaxEncodedLen(ArchiveBlobBytes)+1),
				"wrong_length": s2.EncodeBetter(nil, make([]byte, ArchiveBlobBytes+1)),
				"malformed":    {255, 255, 255, 255, 255, 255},
			} {
				stored = bad
				if _, err := archive.Read(t.Context(), "test.s2", digest); err != ErrArchiveIntegrity {
					t.Fatalf("%s: %v", name, err)
				}
			}
			stored = valid
			if _, err := archive.Read(t.Context(), "test.s2", strings.Repeat("f", 64)); err != ErrArchiveIntegrity {
				t.Fatal("bad digest accepted")
			}
		})
	}
}
