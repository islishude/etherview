package accelerator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const blobChecksumMetadata = "X-Amz-Meta-Etherview-Sha256"

// BlobStore is an optional cache for generation-bound derived objects. A miss
// or error must always fall back to the PostgreSQL representation.
type BlobStore interface {
	Get(context.Context, string) ([]byte, bool, error)
	Put(context.Context, string, []byte) error
}

type S3Options struct {
	Bucket           string
	Prefix           string
	Region           string
	AccessKey        string
	SecretKey        string
	SessionToken     string
	PathStyle        bool
	OperationTimeout time.Duration
	MaxObjectBytes   int64
}

// S3BlobStore stores only disposable cache objects. It verifies a bounded
// length and an application checksum on every read before returning bytes to a
// decoder.
type S3BlobStore struct {
	client           *minio.Core
	bucket           string
	prefix           string
	operationTimeout time.Duration
	maxObjectBytes   int64
}

func NewS3BlobStore(rawEndpoint string, options S3Options) (*S3BlobStore, error) {
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, errors.New("parse S3-compatible endpoint")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("S3-compatible endpoint contains unsupported URL components")
	}
	if strings.TrimSpace(options.Bucket) == "" {
		return nil, errors.New("S3-compatible bucket is empty")
	}
	if options.OperationTimeout <= 0 {
		options.OperationTimeout = 2 * time.Second
	}
	if options.MaxObjectBytes <= 0 {
		options.MaxObjectBytes = 16 << 20
	}
	transport, err := minio.DefaultTransport(endpoint.Scheme == "https")
	if err != nil {
		return nil, errors.New("configure S3-compatible transport")
	}
	transport.ResponseHeaderTimeout = options.OperationTimeout
	transport.TLSHandshakeTimeout = options.OperationTimeout
	transport.ExpectContinueTimeout = options.OperationTimeout
	lookup := minio.BucketLookupAuto
	if options.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.NewCore(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(options.AccessKey, options.SecretKey, options.SessionToken),
		Secure: endpoint.Scheme == "https", Transport: transport, Region: options.Region,
		BucketLookup: lookup, MaxRetries: 1,
	})
	if err != nil {
		return nil, errors.New("configure S3-compatible client")
	}
	client.SetAppInfo("etherview", "trace-cache")
	return &S3BlobStore{
		client: client, bucket: options.Bucket, prefix: strings.Trim(options.Prefix, "/"),
		operationTimeout: options.OperationTimeout, maxObjectBytes: options.MaxObjectBytes,
	}, nil
}

func (store *S3BlobStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if store == nil || store.client == nil {
		return nil, false, errors.New("S3-compatible blob store is nil")
	}
	objectName, err := store.objectName(key)
	if err != nil {
		return nil, false, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, store.operationTimeout)
	defer cancel()
	reader, info, _, err := store.client.GetObject(operationCtx, store.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.StatusCode == 404 {
			return nil, false, nil
		}
		return nil, false, errors.New("read S3-compatible cache object")
	}
	defer reader.Close()
	if info.Size < 0 || info.Size > store.maxObjectBytes {
		return nil, false, errors.New("S3-compatible cache object exceeds configured limit")
	}
	value, err := io.ReadAll(io.LimitReader(reader, store.maxObjectBytes+1))
	if err != nil {
		return nil, false, errors.New("read S3-compatible cache object body")
	}
	if int64(len(value)) > store.maxObjectBytes || int64(len(value)) != info.Size {
		return nil, false, errors.New("S3-compatible cache object length is invalid")
	}
	expected := info.Metadata.Get(blobChecksumMetadata)
	digest := sha256.Sum256(value)
	if expected == "" || !strings.EqualFold(expected, hex.EncodeToString(digest[:])) {
		return nil, false, errors.New("S3-compatible cache object checksum is invalid")
	}
	return value, true, nil
}

func (store *S3BlobStore) Put(ctx context.Context, key string, value []byte) error {
	if store == nil || store.client == nil {
		return errors.New("S3-compatible blob store is nil")
	}
	if int64(len(value)) > store.maxObjectBytes {
		return errors.New("S3-compatible cache object exceeds configured limit")
	}
	objectName, err := store.objectName(key)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(value)
	operationCtx, cancel := context.WithTimeout(ctx, store.operationTimeout)
	defer cancel()
	_, err = store.client.PutObject(operationCtx, store.bucket, objectName, bytes.NewReader(value), int64(len(value)), "", hex.EncodeToString(digest[:]), minio.PutObjectOptions{
		ContentType:  "application/json",
		UserMetadata: map[string]string{blobChecksumMetadata: hex.EncodeToString(digest[:])},
	})
	if err != nil {
		return errors.New("write S3-compatible cache object")
	}
	return nil
}

func (store *S3BlobStore) objectName(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || len(key) > 900 || path.Clean(key) != key || strings.HasPrefix(key, "../") {
		return "", fmt.Errorf("invalid S3-compatible cache key")
	}
	if store.prefix == "" {
		return key, nil
	}
	return store.prefix + "/" + key, nil
}
