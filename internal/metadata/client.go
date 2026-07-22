// Package metadata safely retrieves untrusted token and NFT metadata.
package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/netpolicy"
)

type Kind string

const (
	KindJSON  Kind = "json"
	KindImage Kind = "image"
)

// FailureKind is a stable, non-secret classification for hostile or
// unavailable metadata. Callers use it to decide whether a durable job should
// be retried without parsing transport error strings.
type FailureKind string

const (
	FailureUnsafeURL     FailureKind = "unsafe_url"
	FailureUnavailable   FailureKind = "unavailable"
	FailureTemporary     FailureKind = "temporary"
	FailureUnsafeContent FailureKind = "unsafe_content"
	FailureInvalid       FailureKind = "invalid_content"
	FailureTooLarge      FailureKind = "too_large"
)

// FetchError wraps fetch failures with a stable classification. Error retains
// the original human-readable message for operator diagnostics; callers must
// not persist or log raw URLs from nested transport errors.
type FetchError struct {
	Kind FailureKind
	Err  error
}

func (err *FetchError) Error() string {
	if err == nil || err.Err == nil {
		return "metadata fetch failed"
	}
	return err.Err.Error()
}

func (err *FetchError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func fetchFailure(kind FailureKind, err error) error {
	if err == nil {
		err = errors.New("metadata fetch failed")
	}
	return &FetchError{Kind: kind, Err: err}
}

var cidPattern = regexp.MustCompile(`^[A-Za-z0-9]{10,128}$`)

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Policy struct {
	Timeout                    time.Duration
	MaxBytes                   int64
	MaxRedirects               int
	IPFSGateway                string
	AllowHTTP                  bool
	UnsafeAllowPrivateNetworks bool
	UserAgent                  string
}

type Client struct {
	policy   Policy
	resolver Resolver
	http     *http.Client
}

type Result struct {
	URL         string
	ContentType string
	Body        []byte
	FetchedAt   time.Time
}

func New(policy Policy, resolver Resolver) (*Client, error) {
	if policy.Timeout <= 0 {
		policy.Timeout = 10 * time.Second
	}
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = 2 << 20
	}
	if policy.MaxRedirects <= 0 {
		policy.MaxRedirects = 3
	}
	if policy.UserAgent == "" {
		policy.UserAgent = "etherview-metadata/1"
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	client := &Client{policy: policy, resolver: resolver}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Environment proxies can resolve and reach a private target on the
	// application's behalf, bypassing safeDial's DNS/IP policy.
	transport.Proxy = nil
	transport.DialContext = client.safeDial
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 2
	transport.ResponseHeaderTimeout = policy.Timeout
	transport.TLSHandshakeTimeout = policy.Timeout
	client.http = &http.Client{
		Transport: transport,
		Timeout:   policy.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= policy.MaxRedirects {
				return fetchFailure(FailureUnsafeURL, errors.New("metadata redirect limit exceeded"))
			}
			if err := client.validateURL(request.URL); err != nil {
				return fetchFailure(FailureUnsafeURL, err)
			}
			return nil
		},
	}
	if policy.IPFSGateway != "" {
		gateway, err := url.Parse(policy.IPFSGateway)
		if err != nil || gateway.Scheme != "https" || gateway.Host == "" || gateway.User != nil {
			return nil, errors.New("IPFS gateway must be an absolute HTTPS URL without credentials")
		}
	}
	return client, nil
}

func (c *Client) Fetch(ctx context.Context, rawURL string, kind Kind) (Result, error) {
	resolved, err := c.resolveURL(rawURL)
	if err != nil {
		var classified *FetchError
		if errors.As(err, &classified) {
			return Result{}, classified
		}
		return Result{}, fetchFailure(FailureUnsafeURL, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return Result{}, fetchFailure(FailureUnsafeURL, fmt.Errorf("create metadata request: %w", err))
	}
	request.Header.Set("Accept", acceptHeader(kind))
	request.Header.Set("User-Agent", c.policy.UserAgent)
	response, err := c.http.Do(request)
	if err != nil {
		var classified *FetchError
		if errors.As(err, &classified) {
			return Result{}, classified
		}
		return Result{}, fetchFailure(FailureTemporary, fmt.Errorf("fetch metadata: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		kind := FailureUnavailable
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly ||
			response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			kind = FailureTemporary
		}
		return Result{}, fetchFailure(kind, fmt.Errorf("metadata server returned HTTP %d", response.StatusCode))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !allowedContentType(kind, strings.ToLower(mediaType)) {
		return Result{}, fetchFailure(FailureUnsafeContent, fmt.Errorf("metadata content type %q is not allowed for %s", response.Header.Get("Content-Type"), kind))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.policy.MaxBytes+1))
	if err != nil {
		return Result{}, fetchFailure(FailureTemporary, fmt.Errorf("read metadata: %w", err))
	}
	if int64(len(body)) > c.policy.MaxBytes {
		return Result{}, fetchFailure(FailureTooLarge, errors.New("metadata response exceeds size limit"))
	}
	if kind == KindJSON && !json.Valid(body) {
		return Result{}, fetchFailure(FailureInvalid, errors.New("metadata response is not valid JSON"))
	}
	if kind == KindImage && !validImageSignature(mediaType, body) {
		return Result{}, fetchFailure(FailureUnsafeContent, errors.New("metadata image bytes do not match the declared safe image type"))
	}
	return Result{URL: response.Request.URL.String(), ContentType: mediaType, Body: body, FetchedAt: time.Now().UTC()}, nil
}

func validImageSignature(mediaType string, body []byte) bool {
	switch mediaType {
	case "image/png":
		return bytes.HasPrefix(body, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	case "image/jpeg":
		return len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff
	case "image/gif":
		return bytes.HasPrefix(body, []byte("GIF87a")) || bytes.HasPrefix(body, []byte("GIF89a"))
	case "image/webp":
		return len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP"))
	case "image/avif":
		if len(body) < 16 || !bytes.Equal(body[4:8], []byte("ftyp")) {
			return false
		}
		brand := string(body[8:12])
		if brand == "avif" || brand == "avis" {
			return true
		}
		maximum := len(body)
		if maximum > 64 {
			maximum = 64
		}
		return bytes.Contains(body[8:maximum], []byte("avif")) || bytes.Contains(body[8:maximum], []byte("avis"))
	default:
		return false
	}
}

func (c *Client) resolveURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, errors.New("invalid metadata URL")
	}
	if parsed.Scheme == "ipfs" {
		if c.policy.IPFSGateway == "" {
			return nil, fetchFailure(FailureUnavailable, errors.New("IPFS metadata is unavailable without a gateway"))
		}
		cid := parsed.Host
		if cid == "" {
			parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 2)
			cid = parts[0]
			if len(parts) == 2 {
				parsed.Path = "/" + parts[1]
			} else {
				parsed.Path = ""
			}
		}
		if !cidPattern.MatchString(cid) || containsParentSegment(parsed.Path) {
			return nil, errors.New("invalid IPFS CID or path")
		}
		gateway, _ := url.Parse(c.policy.IPFSGateway)
		gateway.Path = path.Join(gateway.Path, "ipfs", cid, parsed.Path)
		gateway.RawQuery = parsed.RawQuery
		parsed = gateway
	}
	if err := c.validateURL(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (c *Client) validateURL(parsed *url.URL) error {
	if parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("metadata URL must be absolute and cannot contain credentials or fragments")
	}
	if len(parsed.String()) > 4096 {
		return errors.New("metadata URL exceeds 4096 bytes")
	}
	if parsed.Scheme != "https" && !(c.policy.AllowHTTP && parsed.Scheme == "http") {
		return errors.New("metadata URL scheme is not allowed")
	}
	if containsParentSegment(parsed.EscapedPath()) {
		return errors.New("metadata URL path cannot contain parent traversal")
	}
	return nil
}

func (c *Client) safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split metadata address: %w", err)
	}
	addresses, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("resolve metadata host: %w", err)
	}
	var safe []net.IPAddr
	for _, candidate := range addresses {
		if c.policy.UnsafeAllowPrivateNetworks || publicIP(candidate.IP) {
			safe = append(safe, candidate)
		}
	}
	if len(safe) != len(addresses) || len(safe) == 0 {
		return nil, fetchFailure(FailureUnsafeURL, errors.New("metadata host resolves to a disallowed network"))
	}
	dialer := net.Dialer{Timeout: c.policy.Timeout, KeepAlive: 30 * time.Second}
	var dialErr error
	for _, candidate := range safe {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("dial metadata host: %w", dialErr)
}

func publicIP(ip net.IP) bool {
	return netpolicy.PublicIP(ip)
}

func containsParentSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == ".." {
			return true
		}
	}
	return false
}

func acceptHeader(kind Kind) string {
	if kind == KindImage {
		return "image/avif,image/webp,image/png,image/jpeg,image/gif"
	}
	return "application/json,application/*+json"
}

func allowedContentType(kind Kind, value string) bool {
	if kind == KindImage {
		switch value {
		case "image/avif", "image/webp", "image/png", "image/jpeg", "image/gif":
			return true
		default:
			return false
		}
	}
	return value == "application/json" || strings.HasPrefix(value, "application/") && strings.HasSuffix(value, "+json")
}
