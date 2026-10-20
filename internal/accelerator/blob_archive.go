package accelerator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const ArchiveBlobBytes = 131072

var ErrArchiveUnavailable = errors.New("blob_storage_unavailable")
var ErrArchiveIntegrity = errors.New("blob_storage_integrity")

// BlobArchive owns durable binary content, separately from disposable trace caches.
// References become public only through the caller's fenced PostgreSQL publisher.
type BlobArchive struct{ store *S3BlobStore }

func NewBlobArchive(ctx context.Context, endpoint string, options S3Options) (*BlobArchive, error) {
	options.MaxObjectBytes = ArchiveBlobBytes
	store, err := NewS3BlobStore(ctx, endpoint, options)
	if err != nil {
		return nil, ErrArchiveUnavailable
	}
	return &BlobArchive{store: store}, nil
}
func (a *BlobArchive) Read(ctx context.Context, key, digest string) ([]byte, error) {
	name, err := a.store.objectName(key)
	if err != nil {
		return nil, ErrArchiveIntegrity
	}
	request, cancel := context.WithTimeout(ctx, a.store.operationTimeout)
	defer cancel()
	result, err := a.store.client.GetObject(request, &s3.GetObjectInput{Bucket: aws.String(a.store.bucket), Key: aws.String(name)})
	if err != nil {
		return nil, ErrArchiveUnavailable
	}
	defer func() { _ = result.Body.Close() }()
	if result.ContentLength == nil || *result.ContentLength != ArchiveBlobBytes {
		return nil, ErrArchiveIntegrity
	}
	data, err := io.ReadAll(io.LimitReader(result.Body, ArchiveBlobBytes+1))
	if err != nil {
		return nil, ErrArchiveUnavailable
	}
	sum := sha256.Sum256(data)
	if len(data) != ArchiveBlobBytes || hex.EncodeToString(sum[:]) != strings.TrimPrefix(digest, "0x") {
		return nil, ErrArchiveIntegrity
	}
	return data, nil
}
func (a *BlobArchive) Create(ctx context.Context, key string, data []byte) (string, error) {
	if len(data) != ArchiveBlobBytes {
		return "", ErrArchiveIntegrity
	}
	name, err := a.store.objectName(key)
	if err != nil {
		return "", ErrArchiveIntegrity
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	request, cancel := context.WithTimeout(ctx, a.store.operationTimeout)
	defer cancel()
	_, err = a.store.client.PutObject(request, &s3.PutObjectInput{Bucket: aws.String(a.store.bucket), Key: aws.String(name), Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))), ContentType: aws.String("application/octet-stream"), IfNoneMatch: aws.String("*"), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(sum[:]))})
	if err != nil {
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != "PreconditionFailed" {
			return "", ErrArchiveUnavailable
		}
	}
	// A successful PUT or existing-key response is never sufficient publication evidence.
	if _, err := a.Read(ctx, key, digest); err != nil {
		return "", err
	}
	return "0x" + digest, nil
}
