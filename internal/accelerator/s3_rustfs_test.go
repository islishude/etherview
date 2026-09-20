//go:build rustfs

package accelerator

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/islishude/etherview/internal/testcompose"
)

// TestRustFS uses the production service definition with only a loopback port
// overlay. Its unique project owns every container and volume it cleans up.
func TestRustFS(t *testing.T) {
	isolateAWSCredentialEnvironment(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "ports.yaml")
	if err := os.WriteFile(overlay, []byte("services:\n  object-storage:\n    ports:\n      - '127.0.0.1::9000'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(root, testcompose.UniqueProjectName("etherview-rustfs"), "compose.yaml", overlay)
	project.Profiles = []string{"accelerators"}
	access, secret := rand.Text(), rand.Text()
	project.Env = map[string]string{"RUSTFS_ACCESS_KEY": access, "RUSTFS_SECRET_KEY": secret}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		if t.Failed() {
			t.Log(project.Logs(cleanupCtx))
		}
		if err := project.Down(cleanupCtx); err != nil {
			t.Error(err)
		}
	})
	if err := project.Up(ctx, "object-storage"); err != nil {
		t.Fatal(err)
	}
	options := S3Options{Bucket: "etherview-cache", Prefix: "acceptance", AccessKey: access, SecretKey: secret, Region: "us-east-1", PathStyle: true, OperationTimeout: 2 * time.Second}
	connect := func() *S3BlobStore {
		t.Helper()
		address, err := project.Port(ctx, "object-storage", 9000)
		if err != nil {
			t.Fatal(err)
		}
		store, err := NewS3BlobStore(ctx, "http://"+address, options)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	store := connect()
	if _, err := store.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(options.Bucket)}); err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{"trace.json": []byte(`{"frames":[]}`), "empty.json": {}}
	for key, value := range payloads {
		if err := store.Put(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	verify := func() {
		t.Helper()
		for key, value := range payloads {
			got, found, err := store.Get(ctx, key)
			if err != nil || !found || !bytes.Equal(got, value) {
				t.Fatalf("%s: found=%v err=%v payload=%q", key, found, err, got)
			}
			head, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(options.Bucket), Key: aws.String(options.Prefix + "/" + key)})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(value)
			if head.Metadata[blobChecksumMetadataKey] != hex.EncodeToString(digest[:]) || aws.ToString(head.ContentType) != "application/json" {
				t.Fatalf("%s: metadata or content type not retained", key)
			}
		}
	}
	verify()
	if _, found, err := store.Get(ctx, "missing.json"); err != nil || found {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
	if _, err := store.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(options.Bucket), Key: aws.String(options.Prefix + "/corrupt.json"), Body: bytes.NewReader([]byte("corrupt")), Metadata: map[string]string{blobChecksumMetadataKey: "invalid"}}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(ctx, "corrupt.json"); err == nil || found {
		t.Fatalf("corrupt: found=%v err=%v", found, err)
	}
	if _, err := project.Run(ctx, "up", "-d", "--wait", "--wait-timeout", "90", "--force-recreate", "object-storage"); err != nil {
		t.Fatal(err)
	}
	store = connect()
	verify()
	if _, err := project.Run(ctx, "stop", "object-storage"); err != nil {
		t.Fatal(err)
	}
	for _, write := range []bool{false, true} {
		started := time.Now()
		if write {
			err = store.Put(ctx, "offline.json", []byte("{}"))
		} else {
			_, _, err = store.Get(ctx, "trace.json")
		}
		if err == nil || time.Since(started) > 3*time.Second {
			t.Fatalf("outage write=%v err=%v elapsed=%s", write, err, time.Since(started))
		}
	}
}
