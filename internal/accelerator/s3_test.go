package accelerator

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
			body, err := readS3TestRequestBody(request)
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
	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Prefix: "test", Region: "us-east-1", PathStyle: true,
		AccessKey: "test-access", SecretKey: "test-secret",
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
	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		AccessKey: "test-access", SecretKey: "test-secret",
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

func TestS3BlobStoreUsesAWSDefaultEnvironmentCredentials(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "auto-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "auto-secret")
	t.Setenv("AWS_SESSION_TOKEN", "auto-session")

	var authorization, sessionToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		sessionToken = request.Header.Get("X-Amz-Security-Token")
		w.Header().Set("ETag", `"cache-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "trace/v1/auto.json", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authorization, "Credential=auto-access/") {
		t.Fatalf("authorization = %q", authorization)
	}
	if sessionToken != "auto-session" {
		t.Fatalf("session token = %q", sessionToken)
	}
}

func TestS3BlobStoreStaticCredentialsOverrideAWSChain(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		w.Header().Set("ETag", `"cache-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		AccessKey: "explicit-access", SecretKey: "explicit-secret",
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "trace/v1/static.json", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authorization, "Credential=explicit-access/") || strings.Contains(authorization, "ambient-access") {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestS3BlobStoreUsesAWSSharedProfile(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	credentialsPath := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(credentialsPath, []byte("[trace-cache]\naws_access_key_id=profile-access\naws_secret_access_key=profile-secret\naws_session_token=profile-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_PROFILE", "trace-cache")

	var authorization, sessionToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		sessionToken = request.Header.Get("X-Amz-Security-Token")
		w.Header().Set("ETag", `"cache-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "trace/v1/profile.json", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authorization, "Credential=profile-access/") || sessionToken != "profile-session" {
		t.Fatalf("authorization=%q session_token=%q", authorization, sessionToken)
	}
}

func TestS3BlobStoreRefreshesEKSContainerCredentials(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	tokenPath := filepath.Join(t.TempDir(), "pod-identity-token")
	if err := os.WriteFile(tokenPath, []byte("pod-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	var credentialMu sync.Mutex
	credentialRequests := 0
	credentialServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "pod-token" {
			t.Errorf("credential authorization = %q", request.Header.Get("Authorization"))
		}
		credentialMu.Lock()
		credentialRequests++
		requestNumber := credentialRequests
		credentialMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"AccessKeyId":"pod-access-%d","SecretAccessKey":"pod-secret-%d","Token":"pod-session-%d","Expiration":"%s"}`,
			requestNumber, requestNumber, requestNumber, time.Now().Add(time.Second).UTC().Format(time.RFC3339))
	}))
	defer credentialServer.Close()
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", credentialServer.URL)
	t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE", tokenPath)

	var objectMu sync.Mutex
	var authorizations []string
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		objectMu.Lock()
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		objectMu.Unlock()
		w.Header().Set("ETag", `"cache-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer objectServer.Close()

	store, err := NewS3BlobStore(context.Background(), objectServer.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"trace/v1/first.json", "trace/v1/second.json"} {
		if err := store.Put(context.Background(), key, []byte(`{"ok":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	credentialMu.Lock()
	requests := credentialRequests
	credentialMu.Unlock()
	objectMu.Lock()
	gotAuthorizations := append([]string(nil), authorizations...)
	objectMu.Unlock()
	if requests < 2 || len(gotAuthorizations) != 2 {
		t.Fatalf("credential_requests=%d authorizations=%v", requests, gotAuthorizations)
	}
	if !strings.Contains(gotAuthorizations[0], "Credential=pod-access-1/") ||
		!strings.Contains(gotAuthorizations[1], "Credential=pod-access-2/") {
		t.Fatalf("authorizations = %v", gotAuthorizations)
	}
}

func TestS3BlobStoreMissingAWSCredentialsIsBoundedAndRedacted(t *testing.T) {
	isolateAWSCredentialEnvironment(t)

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	store, err := NewS3BlobStore(context.Background(), server.URL, S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: 100 * time.Millisecond, MaxObjectBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = store.Put(context.Background(), "trace/v1/missing.json", []byte(`{"ok":true}`))
	if err == nil || err.Error() != "write S3-compatible cache object" {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("credential failure was not bounded: %s", elapsed)
	}
	if requests != 0 {
		t.Fatalf("anonymous object requests = %d", requests)
	}
}

func TestS3BlobStoreRejectsUnsafeContainerCredentialURI(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "http://credentials.example.invalid/latest")
	_, err := NewS3BlobStore(context.Background(), "https://s3.example.invalid", S3Options{
		Bucket: "cache", Region: "us-east-1", PathStyle: true,
		OperationTimeout: time.Second, MaxObjectBytes: 1024,
	})
	if err == nil || err.Error() != "configure AWS credential provider" {
		t.Fatalf("error = %v", err)
	}
}

func isolateAWSCredentialEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config")
	credentialsPath := filepath.Join(directory, "credentials")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
}

func readS3TestRequestBody(request *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(request.Body)
	streamingPayload := strings.HasPrefix(request.Header.Get("X-Amz-Content-Sha256"), "STREAMING-")
	if err != nil || !streamingPayload {
		return raw, err
	}
	reader := bufio.NewReader(bytes.NewReader(raw))
	var decoded bytes.Buffer
	for {
		header, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, readErr
		}
		sizeText, _, _ := strings.Cut(strings.TrimSpace(header), ";")
		size, parseErr := strconv.ParseInt(sizeText, 16, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		if size == 0 {
			return decoded.Bytes(), nil
		}
		if _, readErr = io.CopyN(&decoded, reader, size); readErr != nil {
			return nil, readErr
		}
		if _, readErr = reader.Discard(2); readErr != nil {
			return nil, readErr
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
