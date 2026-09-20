package accelerator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3RegionSelection(t *testing.T) {
	for _, test := range []struct{ name, explicit, environment, profile, want string }{
		{name: "default", want: "us-east-1"},
		{name: "profile", profile: "eu-west-1", want: "eu-west-1"},
		{name: "environment", environment: "ap-southeast-1", profile: "eu-west-1", want: "ap-southeast-1"},
		{name: "explicit", explicit: "us-west-2", environment: "ap-southeast-1", profile: "eu-west-1", want: "us-west-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateAWSCredentialEnvironment(t)
			t.Setenv("AWS_REGION", test.environment)
			if test.profile != "" {
				configFile := filepath.Join(t.TempDir(), "config")
				if err := os.WriteFile(configFile, []byte("[default]\nregion="+test.profile+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AWS_CONFIG_FILE", configFile)
			}
			store, err := NewS3BlobStore(context.Background(), "https://s3.example.invalid", S3Options{Bucket: "cache", Region: test.explicit, AccessKey: "access", SecretKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			if got := store.client.Options().Region; got != test.want {
				t.Fatalf("region=%s want=%s", got, test.want)
			}
		})
	}
}

type s3RoundTripFunc func(*http.Request) (*http.Response, error)

func (f s3RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestS3AddressingAndWireMetadata(t *testing.T) {
	for _, pathStyle := range []bool{true, false} {
		t.Run(fmt.Sprint(pathStyle), func(t *testing.T) {
			isolateAWSCredentialEnvironment(t)
			store, err := NewS3BlobStore(context.Background(), "https://objects.example.invalid", S3Options{Bucket: "cache", Prefix: "test", Region: "us-east-1", PathStyle: pathStyle, AccessKey: "access", SecretKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			value := []byte(`{"frames":[]}`)
			digest := sha256.Sum256(value)
			options := store.client.Options()
			options.HTTPClient = &http.Client{Transport: s3RoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				wantHost, wantPath := "objects.example.invalid", "/cache/test/trace.json"
				if !pathStyle {
					wantHost, wantPath = "cache.objects.example.invalid", "/test/trace.json"
				}
				if request.URL.Host != wantHost || request.URL.Path != wantPath {
					t.Errorf("url=%s", request.URL)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil || !bytes.Equal(body, value) {
					t.Errorf("body=%q err=%v", body, err)
				}
				if request.Header.Get(blobChecksumMetadata) != hex.EncodeToString(digest[:]) ||
					request.Header.Get("X-Amz-Checksum-Sha256") != base64.StdEncoding.EncodeToString(digest[:]) ||
					request.Header.Get("Content-Type") != "application/json" || request.ContentLength != int64(len(value)) {
					t.Error("upload metadata, checksum or length changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"etag"`}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
			})}
			store.client = s3.New(options)
			if err := store.Put(context.Background(), "trace.json", value); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestS3ReadLengthAndChecksumFailures(t *testing.T) {
	for _, test := range []struct {
		name, body, checksum string
		length               int
		chunked, valid       bool
	}{
		{name: "empty", length: 0, checksum: fmt.Sprintf("%x", sha256.Sum256(nil)), valid: true},
		{name: "missing-checksum", body: "{}", length: 2},
		{name: "truncated", body: "{}", length: 4},
		{name: "oversized", body: strings.Repeat("x", 33), length: 33},
		{name: "unknown-length", body: "{}", chunked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !test.chunked {
					w.Header().Set("Content-Length", fmt.Sprint(test.length))
				}
				w.Header().Set(blobChecksumMetadata, test.checksum)
				w.WriteHeader(200)
				if test.chunked {
					w.(http.Flusher).Flush()
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{Bucket: "cache", Region: "us-east-1", PathStyle: true, AccessKey: "access", SecretKey: "secret", MaxObjectBytes: 32})
			if err != nil {
				t.Fatal(err)
			}
			value, found, err := store.Get(context.Background(), "trace.json")
			if test.valid {
				if err != nil || !found || len(value) != 0 {
					t.Fatalf("valid empty: found=%v err=%v", found, err)
				}
			} else if err == nil || found {
				t.Fatalf("invalid response: found=%v err=%v", found, err)
			}
		})
	}
}

func TestS3ErrorsAreSingleAttemptAndRedacted(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("Location", "http://secret.example.invalid/redirect")
				w.WriteHeader(code)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>http://access:secret@example.invalid/private</Message></Error>`)
			}))
			defer server.Close()
			store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{Bucket: "cache", Region: "us-east-1", PathStyle: true, AccessKey: "access", SecretKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.Get(context.Background(), "trace.json"); found || err == nil || err.Error() != "read S3-compatible cache object" {
				t.Fatalf("read: found=%v err=%v", found, err)
			}
			if err := store.Put(context.Background(), "trace.json", []byte("{}")); err == nil || err.Error() != "write S3-compatible cache object" {
				t.Fatalf("write: %v", err)
			}
			if calls.Load() != 2 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestS3CredentialRefreshTimeout(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	var calls atomic.Int32
	stop := make(chan struct{})
	canceled := make(chan struct{}, 1)
	credentialsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			select {
			case <-r.Context().Done():
				canceled <- struct{}{}
			case <-stop:
			}
			return
		}
		_, _ = fmt.Fprintf(w, `{"AccessKeyId":"access","SecretAccessKey":"secret","Token":"token","Expiration":"%s"}`, time.Now().Add(time.Second).UTC().Format(time.RFC3339))
	}))
	defer func() { close(stop); credentialsServer.Close() }()
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", credentialsServer.URL)
	var objects atomic.Int32
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { objects.Add(1); w.WriteHeader(200) }))
	defer objectServer.Close()
	store, err := NewS3BlobStore(context.Background(), objectServer.URL, S3Options{Bucket: "cache", PathStyle: true, OperationTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "first", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := store.Put(context.Background(), "second", []byte("{}")); err == nil || err.Error() != "write S3-compatible cache object" {
		t.Fatalf("refresh error=%v", err)
	}
	if time.Since(started) > time.Second || objects.Load() != 1 {
		t.Fatalf("elapsed=%s object calls=%d", time.Since(started), objects.Load())
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("background credential refresh was not canceled")
	}
	if calls.Load() != 2 {
		t.Fatalf("credential calls=%d", calls.Load())
	}
}

func TestS3RequestTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{Bucket: "cache", Region: "us-east-1", PathStyle: true, AccessKey: "access", SecretKey: "secret", OperationTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelEarly := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelEarly {
			cancel()
		}
		started := time.Now()
		_, found, err := store.Get(ctx, "trace.json")
		cancel()
		if err == nil || found || time.Since(started) > time.Second {
			t.Fatalf("cancel=%v found=%v err=%v elapsed=%s", cancelEarly, found, err, time.Since(started))
		}
	}
}
