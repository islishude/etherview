package accelerator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/endpointcreds"
	"github.com/aws/aws-sdk-go-v2/credentials/processcreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const blobChecksumMetadata = "X-Amz-Meta-Etherview-Sha256"
const blobChecksumMetadataKey = "etherview-sha256"

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
	client           *s3.Client
	bucket           string
	prefix           string
	operationTimeout time.Duration
	maxObjectBytes   int64
}

func NewS3BlobStore(ctx context.Context, rawEndpoint string, options S3Options) (*S3BlobStore, error) {
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
	configuration, err := s3Configuration(ctx, options)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = options.OperationTimeout
	transport.TLSHandshakeTimeout = options.OperationTimeout
	transport.ExpectContinueTimeout = options.OperationTimeout
	client := s3.NewFromConfig(configuration, func(clientOptions *s3.Options) {
		clientOptions.BaseEndpoint = awssdk.String(strings.TrimSuffix(endpoint.String(), "/"))
		clientOptions.UsePathStyle = options.PathStyle
		clientOptions.RetryMaxAttempts = 1
		clientOptions.HTTPClient = &http.Client{Transport: transport, Timeout: options.OperationTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	})
	return &S3BlobStore{
		client: client, bucket: options.Bucket, prefix: strings.Trim(options.Prefix, "/"),
		operationTimeout: options.OperationTimeout, maxObjectBytes: options.MaxObjectBytes,
	}, nil
}

func s3Configuration(ctx context.Context, options S3Options) (awssdk.Config, error) {
	if (options.AccessKey == "") != (options.SecretKey == "") {
		return awssdk.Config{}, errors.New("configure static S3-compatible credentials")
	}
	if options.SessionToken != "" && options.AccessKey == "" {
		return awssdk.Config{}, errors.New("configure static S3-compatible session credentials")
	}
	// Fully explicit settings must not depend on unrelated shared AWS profiles.
	// Without an explicit region, load AWS configuration to preserve region
	// precedence before falling back to us-east-1.
	if options.AccessKey != "" && options.Region != "" {
		return awssdk.Config{
			Region:      options.Region,
			Credentials: credentials.NewStaticCredentialsProvider(options.AccessKey, options.SecretKey, options.SessionToken),
			Retryer:     func() awssdk.Retryer { return awssdk.NopRetryer{} },
		}, nil
	}
	credentialHTTP := &http.Client{
		Timeout:       options.OperationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(credentialHTTP),
		awsconfig.WithRetryer(func() awssdk.Retryer { return awssdk.NopRetryer{} }),
		// Endpoint credentials construct their own client rather than inheriting
		// the config HTTP client. Bound background cache refreshes as well.
		awsconfig.WithEndpointCredentialOptions(func(provider *endpointcreds.Options) {
			provider.HTTPClient = credentialHTTP
			provider.Retryer = awssdk.NopRetryer{}
		}),
		awsconfig.WithProcessCredentialOptions(func(provider *processcreds.Options) {
			provider.Timeout = options.OperationTimeout
		}),
	}
	if options.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(options.Region))
	}
	if options.AccessKey != "" {
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(options.AccessKey, options.SecretKey, options.SessionToken)))
	}
	loadCtx, cancel := context.WithTimeout(ctx, options.OperationTimeout)
	defer cancel()
	configuration, err := awsconfig.LoadDefaultConfig(loadCtx, loadOptions...)
	if err != nil {
		return awssdk.Config{}, errors.New("configure AWS credential provider")
	}
	if configuration.Region == "" {
		configuration.Region = "us-east-1"
	}
	configuration.Credentials = boundedAWSCredentials{provider: configuration.Credentials, timeout: options.OperationTimeout}
	return configuration, nil
}

// The SDK owns credential selection, caching and refresh. Each caller has a
// deadline even when the SDK cache shares a refresh with other callers.
type boundedAWSCredentials struct {
	provider awssdk.CredentialsProvider
	timeout  time.Duration
}

func (provider boundedAWSCredentials) Retrieve(ctx context.Context) (awssdk.Credentials, error) {
	if provider.provider == nil {
		return awssdk.Credentials{}, errors.New("retrieve AWS credentials")
	}
	ctx, cancel := context.WithTimeout(ctx, provider.timeout)
	defer cancel()
	value, err := provider.provider.Retrieve(ctx)
	if err != nil || !value.HasKeys() {
		return awssdk.Credentials{}, errors.New("retrieve AWS credentials")
	}
	return value, nil
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
	info, err := store.client.GetObject(operationCtx, &s3.GetObjectInput{Bucket: awssdk.String(store.bucket), Key: awssdk.String(objectName)})
	if err != nil {
		var apiError smithy.APIError
		var responseError *smithyhttp.ResponseError
		if (errors.As(err, &apiError) && (apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NoSuchObject")) || (errors.As(err, &responseError) && responseError.HTTPStatusCode() == http.StatusNotFound) {
			return nil, false, nil
		}
		return nil, false, errors.New("read S3-compatible cache object")
	}
	reader := info.Body
	defer func() { _ = reader.Close() }()
	if info.ContentLength == nil || *info.ContentLength < 0 || *info.ContentLength > store.maxObjectBytes {
		return nil, false, errors.New("S3-compatible cache object exceeds configured limit")
	}
	value, err := io.ReadAll(io.LimitReader(reader, store.maxObjectBytes+1))
	if err != nil {
		return nil, false, errors.New("read S3-compatible cache object body")
	}
	if int64(len(value)) > store.maxObjectBytes || int64(len(value)) != *info.ContentLength {
		return nil, false, errors.New("S3-compatible cache object length is invalid")
	}
	expected := info.Metadata[blobChecksumMetadataKey]
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
	_, err = store.client.PutObject(operationCtx, &s3.PutObjectInput{
		Bucket: awssdk.String(store.bucket), Key: awssdk.String(objectName),
		Body: bytes.NewReader(value), ContentLength: awssdk.Int64(int64(len(value))),
		ContentType:    awssdk.String("application/json"),
		ChecksumSHA256: awssdk.String(base64.StdEncoding.EncodeToString(digest[:])),
		Metadata:       map[string]string{blobChecksumMetadataKey: hex.EncodeToString(digest[:])},
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
