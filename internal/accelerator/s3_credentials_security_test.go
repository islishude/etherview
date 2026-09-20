package accelerator

import (
	"context"
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
)

func TestS3CredentialRedirectsRejected(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			isolateAWSCredentialEnvironment(t)
			var originCalls, targetCalls, objectCalls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				_, _ = io.WriteString(w, `{"AccessKeyId":"redirect-access","SecretAccessKey":"redirect-secret","Token":"redirect-session","Expiration":"2030-01-01T00:00:00Z"}`)
			}))
			defer target.Close()
			// Go forwards Authorization to another port on the same hostname unless
			// redirects are explicitly rejected. No request may reach this target.
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originCalls.Add(1)
				if r.Header.Get("Authorization") != "pod-token" {
					t.Error("credential request omitted its authorization token")
				}
				http.Redirect(w, r, target.URL, status)
			}))
			defer origin.Close()
			objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				objectCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer objects.Close()
			t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", origin.URL)
			t.Setenv("AWS_CONTAINER_AUTHORIZATION_TOKEN", "pod-token")
			store, err := NewS3BlobStore(context.Background(), objects.URL, S3Options{
				Bucket: "cache", Region: "us-east-1", PathStyle: true, OperationTimeout: 100 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			err = store.Put(context.Background(), "trace.json", []byte("{}"))
			if err == nil || err.Error() != "write S3-compatible cache object" {
				t.Errorf("redirect error = %v", err)
			}
			if time.Since(started) > time.Second {
				t.Error("credential redirect failure exceeded operation bound")
			}
			if originCalls.Load() != 1 || targetCalls.Load() != 0 || objectCalls.Load() != 0 {
				t.Fatalf("requests: origin=%d redirect=%d object=%d", originCalls.Load(), targetCalls.Load(), objectCalls.Load())
			}
		})
	}
}

func TestS3CompleteStaticConfigurationIgnoresInvalidAWSProfiles(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			isolateAWSCredentialEnvironment(t)
			if malformed {
				configFile := filepath.Join(t.TempDir(), "malformed-config")
				if err := os.WriteFile(configFile, []byte("[default]\nrole_arn=arn:aws:iam::123456789012:role/Test\nsource_profile=missing-source\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("AWS_CONFIG_FILE", configFile)
			} else {
				t.Setenv("AWS_PROFILE", "missing-profile")
			}
			var calls atomic.Int32
			objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				authorization := r.Header.Get("Authorization")
				if !strings.Contains(authorization, "Credential=explicit-access/") || !strings.Contains(authorization, "/eu-west-1/s3/aws4_request") || r.Header.Get("X-Amz-Security-Token") != "explicit-session" {
					t.Error("request did not use the complete explicit configuration")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer objects.Close()
			options := S3Options{Bucket: "cache", Region: "eu-west-1", PathStyle: true, AccessKey: "explicit-access", SecretKey: "explicit-secret", SessionToken: "explicit-session"}
			store, err := NewS3BlobStore(context.Background(), objects.URL, options)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(context.Background(), "trace.json", []byte("{}")); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("object requests = %d", calls.Load())
			}
			// Selecting the default credential chain must still report malformed
			// configuration instead of hiding it or attempting anonymous requests.
			options.AccessKey, options.SecretKey, options.SessionToken = "", "", ""
			if _, err := NewS3BlobStore(context.Background(), objects.URL, options); err == nil || err.Error() != "configure AWS credential provider" {
				t.Fatalf("default-chain configuration error = %v", err)
			}
		})
	}
}
