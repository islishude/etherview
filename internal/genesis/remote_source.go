package genesis

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/netpolicy"
)

const (
	minimumRemoteFetchTimeout  = time.Second
	maximumRemoteFetchTimeout  = 5 * time.Minute
	maximumRemoteDocumentBytes = int64(64 << 20)
)

type remoteFailureKind string

const (
	remoteFailureUnavailable remoteFailureKind = "unavailable"
	remoteFailureFailed      remoteFailureKind = "failed"
)

// remoteFetchError deliberately retains only a stable classification and
// code. In particular, it must never wrap a URL, response body, resolver
// error, or transport error that could expose credentials or hostile input.
type remoteFetchError struct {
	Kind remoteFailureKind
	Code string
}

func (failure *remoteFetchError) Error() string {
	if failure == nil || failure.Code == "" {
		return "genesis_remote_fetch_failed"
	}
	return failure.Code
}

func remoteFailure(kind remoteFailureKind, code string) error {
	return &remoteFetchError{Kind: kind, Code: code}
}

func remoteErrorDetails(err error) (remoteFailureKind, string, bool) {
	var failure *remoteFetchError
	if !errors.As(err, &failure) {
		return "", "", false
	}
	return failure.Kind, failure.Code, true
}

type remoteResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type remoteDialContext func(context.Context, string, string) (net.Conn, error)

type remoteSourceOptions struct {
	resolver        remoteResolver
	dialContext     remoteDialContext
	tlsClientConfig *tls.Config
}

type remoteDocument struct {
	Bytes  []byte
	SHA256 [sha256.Size]byte
}

type remoteSource struct {
	endpoint       *url.URL
	expectedDigest *[sha256.Size]byte
	resolver       remoteResolver
	dialContext    remoteDialContext
	http           *http.Client
	maximumBytes   int64
}

func newRemoteSource(
	rawURL string,
	expectedSHA256 string,
	timeout time.Duration,
) (*remoteSource, error) {
	return newRemoteSourceWithOptions(rawURL, expectedSHA256, timeout, remoteSourceOptions{})
}

func newRemoteSourceWithOptions(
	rawURL string,
	expectedSHA256 string,
	timeout time.Duration,
	options remoteSourceOptions,
) (*remoteSource, error) {
	endpoint, err := parseRemoteGenesisURL(rawURL)
	if err != nil {
		return nil, err
	}
	expectedDigest, err := parseRemoteGenesisDigest(expectedSHA256)
	if err != nil {
		return nil, err
	}
	if timeout < minimumRemoteFetchTimeout || timeout > maximumRemoteFetchTimeout {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_timeout_invalid")
	}
	if options.resolver == nil {
		options.resolver = net.DefaultResolver
	}
	if options.dialContext == nil {
		dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		options.dialContext = dialer.DialContext
	}
	source := &remoteSource{
		endpoint:       endpoint,
		expectedDigest: expectedDigest,
		resolver:       options.resolver,
		dialContext:    options.dialContext,
		maximumBytes:   maximumRemoteDocumentBytes,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy can resolve or reach a private endpoint on the application's
	// behalf, bypassing the DNS and direct-dial checks below.
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.DialContext = source.safeDial
	transport.MaxIdleConns = 2
	transport.MaxIdleConnsPerHost = 1
	transport.ResponseHeaderTimeout = timeout
	transport.TLSHandshakeTimeout = timeout
	if options.tlsClientConfig != nil {
		transport.TLSClientConfig = options.tlsClientConfig.Clone()
	}
	source.http = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return remoteFailure(remoteFailureFailed, "genesis_remote_redirect_not_allowed")
		},
	}
	return source, nil
}

func parseRemoteGenesisURL(rawURL string) (*url.URL, error) {
	if rawURL == "" || len(rawURL) > 4096 || rawURL != strings.TrimSpace(rawURL) {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_url_invalid")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(rawURL, "#") {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_url_invalid")
	}
	port := parsed.Port()
	if (port != "" && port != "443") || strings.HasSuffix(parsed.Host, ":") {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_url_invalid")
	}
	if remotePathTraversesParent(parsed.EscapedPath()) {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_url_invalid")
	}
	return parsed, nil
}

func remotePathTraversesParent(escapedPath string) bool {
	current := escapedPath
	for {
		normalized := strings.ReplaceAll(current, `\`, "/")
		if slices.Contains(strings.Split(normalized, "/"), "..") {
			return true
		}
		decoded, err := url.PathUnescape(current)
		if err != nil || strings.ContainsRune(decoded, '\x00') {
			return true
		}
		if decoded == current {
			return false
		}
		current = decoded
	}
}

func parseRemoteGenesisDigest(value string) (*[sha256.Size]byte, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_checksum_invalid")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_checksum_invalid")
	}
	var digest [sha256.Size]byte
	copy(digest[:], decoded)
	if digest == ([sha256.Size]byte{}) {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_checksum_invalid")
	}
	return &digest, nil
}

func (source *remoteSource) fetch(ctx context.Context) (remoteDocument, error) {
	if source == nil || source.endpoint == nil || source.http == nil {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_source_invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.endpoint.String(), nil)
	if err != nil {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_request_invalid")
	}
	request.Header.Set("Accept", "application/json, application/*+json, application/octet-stream, text/plain")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "etherview-genesis/1")
	response, err := source.http.Do(request)
	if err != nil {
		if kind, code, ok := remoteErrorDetails(err); ok {
			return remoteDocument{}, remoteFailure(kind, code)
		}
		return remoteDocument{}, remoteFailure(remoteFailureUnavailable, "genesis_remote_unavailable")
	}
	defer response.Body.Close() //nolint:errcheck

	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return remoteDocument{}, remoteFailure(remoteFailureUnavailable, "genesis_remote_http_unavailable")
		}
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_http_status")
	}
	encoding := strings.TrimSpace(strings.ToLower(response.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_content_encoding")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !allowedRemoteGenesisMediaType(strings.ToLower(mediaType)) {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_content_type")
	}
	if response.ContentLength > source.maximumBytes {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_too_large")
	}
	document, err := io.ReadAll(io.LimitReader(response.Body, source.maximumBytes+1))
	if err != nil {
		return remoteDocument{}, remoteFailure(remoteFailureUnavailable, "genesis_remote_read_unavailable")
	}
	if int64(len(document)) > source.maximumBytes {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_too_large")
	}
	digest := sha256.Sum256(document)
	if source.expectedDigest != nil && digest != *source.expectedDigest {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_checksum_mismatch")
	}
	if !json.Valid(document) {
		return remoteDocument{}, remoteFailure(remoteFailureFailed, "genesis_remote_json_invalid")
	}
	return remoteDocument{Bytes: document, SHA256: digest}, nil
}

func allowedRemoteGenesisMediaType(mediaType string) bool {
	if mediaType == "application/json" || mediaType == "application/octet-stream" ||
		mediaType == "text/plain" {
		return true
	}
	const applicationPrefix = "application/"
	if !strings.HasPrefix(mediaType, applicationPrefix) || !strings.HasSuffix(mediaType, "+json") {
		return false
	}
	subtype := strings.TrimPrefix(mediaType, applicationPrefix)
	return len(subtype) > len("+json")
}

func (source *remoteSource) safeDial(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, remoteFailure(remoteFailureFailed, "genesis_remote_address_invalid")
	}
	addresses, err := source.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, remoteFailure(remoteFailureUnavailable, "genesis_remote_dns_unavailable")
	}
	for _, candidate := range addresses {
		if candidate.Zone != "" || !netpolicy.PublicIP(candidate.IP) {
			return nil, remoteFailure(remoteFailureFailed, "genesis_remote_address_not_public")
		}
	}
	for _, candidate := range addresses {
		connection, dialErr := source.dialContext(
			ctx,
			network,
			net.JoinHostPort(candidate.IP.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, remoteFailure(remoteFailureUnavailable, "genesis_remote_dial_unavailable")
}
