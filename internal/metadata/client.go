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
	Kind       FailureKind
	Err        error
	Diagnostic FetchDiagnostic
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
	Diagnostic  FetchDiagnostic
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
			collector := fetchDiagnosticFromContext(request.Context())
			if collector == nil && len(via) > 0 {
				collector = fetchDiagnosticFromContext(via[0].Context())
			}
			if collector != nil {
				collector.setTarget(request.URL, "", true)
			}
			if len(via) >= policy.MaxRedirects {
				if collector != nil {
					collector.setPhase(FetchPhaseRedirect)
				}
				return fetchFailure(FailureUnsafeURL, errors.New("metadata redirect limit exceeded"))
			}
			if err := client.validateURL(request.URL); err != nil {
				if collector != nil {
					collector.setPhase(FetchPhaseRedirect)
				}
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
	collector := newFetchDiagnosticCollector(rawURL)
	collector.allowUnsafePrivateNetworks(c.policy.UnsafeAllowPrivateNetworks)
	target, err := c.resolveTarget(rawURL)
	if err != nil {
		collector.setPhase(FetchPhaseURL)
		if classified, ok := errors.AsType[*FetchError](err); ok {
			return Result{}, fetchErrorWithDiagnostic(classified, collector.snapshot())
		}
		return Result{}, fetchFailureWithDiagnostic(FailureUnsafeURL, err, collector.snapshot())
	}
	collector.setTarget(target.URL, target.PublicIPFSPath, false)
	requestCtx := withFetchDiagnostic(ctx, collector)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.URL.String(), nil)
	if err != nil {
		collector.setPhase(FetchPhaseURL)
		return Result{}, fetchFailureWithDiagnostic(
			FailureUnsafeURL, fmt.Errorf("create metadata request: %w", err), collector.snapshot(),
		)
	}
	request.Header.Set("Accept", acceptHeader(kind))
	request.Header.Set("User-Agent", c.policy.UserAgent)
	response, err := c.http.Do(request)
	if err != nil {
		if classified, ok := errors.AsType[*FetchError](err); ok {
			return Result{}, fetchErrorWithDiagnostic(classified, collector.snapshot())
		}
		collector.setPhase(FetchPhaseTransport)
		return Result{}, fetchFailureWithDiagnostic(
			FailureTemporary, fmt.Errorf("fetch metadata: %w", err), collector.snapshot(),
		)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		collector.setPhase(FetchPhaseHTTP)
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		kind := FailureUnavailable
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly ||
			response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			kind = FailureTemporary
		}
		return Result{}, fetchFailureWithDiagnostic(
			kind, fmt.Errorf("metadata server returned HTTP %d", response.StatusCode), collector.snapshot(),
		)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !allowedContentType(kind, strings.ToLower(mediaType)) {
		collector.setPhase(FetchPhaseContent)
		return Result{}, fetchFailureWithDiagnostic(
			FailureUnsafeContent,
			fmt.Errorf("metadata content type %q is not allowed for %s", response.Header.Get("Content-Type"), kind),
			collector.snapshot(),
		)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.policy.MaxBytes+1))
	if err != nil {
		collector.setPhase(FetchPhaseContent)
		return Result{}, fetchFailureWithDiagnostic(
			FailureTemporary, fmt.Errorf("read metadata: %w", err), collector.snapshot(),
		)
	}
	if int64(len(body)) > c.policy.MaxBytes {
		collector.setPhase(FetchPhaseContent)
		return Result{}, fetchFailureWithDiagnostic(
			FailureTooLarge, errors.New("metadata response exceeds size limit"), collector.snapshot(),
		)
	}
	if kind == KindJSON && !json.Valid(body) {
		collector.setPhase(FetchPhaseContent)
		return Result{}, fetchFailureWithDiagnostic(
			FailureInvalid, errors.New("metadata response is not valid JSON"), collector.snapshot(),
		)
	}
	if kind == KindImage && !validImageSignature(mediaType, body) {
		collector.setPhase(FetchPhaseContent)
		return Result{}, fetchFailureWithDiagnostic(
			FailureUnsafeContent,
			errors.New("metadata image bytes do not match the declared safe image type"),
			collector.snapshot(),
		)
	}
	return Result{
		URL: response.Request.URL.String(), ContentType: mediaType, Body: body,
		FetchedAt: time.Now().UTC(), Diagnostic: collector.snapshot(),
	}, nil
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
		maximum := min(len(body), 64)
		return bytes.Contains(body[8:maximum], []byte("avif")) || bytes.Contains(body[8:maximum], []byte("avis"))
	default:
		return false
	}
}

func (c *Client) resolveURL(rawURL string) (*url.URL, error) {
	target, err := c.resolveTarget(rawURL)
	if err != nil {
		return nil, err
	}
	return target.URL, nil
}

type resolvedMetadataTarget struct {
	URL            *url.URL
	PublicIPFSPath string
}

func (c *Client) resolveTarget(rawURL string) (resolvedMetadataTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return resolvedMetadataTarget{}, errors.New("invalid metadata URL")
	}
	publicIPFSPath := ""
	if parsed.Scheme == "ipfs" {
		if c.policy.IPFSGateway == "" {
			return resolvedMetadataTarget{}, fetchFailure(FailureUnavailable, errors.New("IPFS metadata is unavailable without a gateway"))
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
			return resolvedMetadataTarget{}, errors.New("invalid IPFS CID or path")
		}
		publicIPFSPath = path.Join("/ipfs", cid, parsed.Path)
		gateway, _ := url.Parse(c.policy.IPFSGateway)
		gateway.Path = path.Join(gateway.Path, publicIPFSPath)
		gateway.RawQuery = parsed.RawQuery
		parsed = gateway
	}
	if err := c.validateURL(parsed); err != nil {
		return resolvedMetadataTarget{}, err
	}
	return resolvedMetadataTarget{URL: parsed, PublicIPFSPath: publicIPFSPath}, nil
}

func (c *Client) validateURL(parsed *url.URL) error {
	if parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("metadata URL must be absolute and cannot contain credentials or fragments")
	}
	if len(parsed.String()) > 4096 {
		return errors.New("metadata URL exceeds 4096 bytes")
	}
	if parsed.Scheme != "https" && (!c.policy.AllowHTTP || parsed.Scheme != "http") {
		return errors.New("metadata URL scheme is not allowed")
	}
	if containsParentSegment(parsed.EscapedPath()) {
		return errors.New("metadata URL path cannot contain parent traversal")
	}
	return nil
}

func (c *Client) safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	collector := fetchDiagnosticFromContext(ctx)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		collector.setPhase(FetchPhaseTransport)
		return nil, fmt.Errorf("split metadata address: %w", err)
	}
	addresses, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		collector.setPhase(FetchPhaseDNS)
		if err != nil {
			return nil, fmt.Errorf("resolve metadata host: %w", err)
		}
		return nil, errors.New("resolve metadata host: resolver returned no addresses")
	}
	collector.recordResolved(addresses)
	var safe []net.IPAddr
	for _, candidate := range addresses {
		decision := netpolicy.ClassifyIP(candidate.IP)
		if decision.Allowed {
			safe = append(safe, candidate)
			continue
		}
		if c.policy.UnsafeAllowPrivateNetworks {
			collector.recordNetworkPolicyBypass()
			safe = append(safe, candidate)
		}
	}
	if len(safe) != len(addresses) || len(safe) == 0 {
		collector.setPhase(FetchPhaseNetworkPolicy)
		return nil, fetchFailure(FailureUnsafeURL, errors.New("metadata host resolves to a disallowed network"))
	}
	dialer := net.Dialer{Timeout: c.policy.Timeout, KeepAlive: 30 * time.Second}
	var dialErr error
	for _, candidate := range safe {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			collector.recordConnectedIP(candidate.IP)
			return connection, nil
		}
		dialErr = err
	}
	collector.setPhase(FetchPhaseTransport)
	return nil, fmt.Errorf("dial metadata host: %w", dialErr)
}

func fetchFailureWithDiagnostic(kind FailureKind, err error, diagnostic FetchDiagnostic) error {
	return &FetchError{Kind: kind, Err: err, Diagnostic: diagnostic}
}

func fetchErrorWithDiagnostic(failure *FetchError, diagnostic FetchDiagnostic) error {
	if failure == nil {
		return fetchFailureWithDiagnostic(FailureTemporary, errors.New("metadata fetch failed"), diagnostic)
	}
	if failure.Diagnostic.SourceScheme != "" {
		diagnostic = failure.Diagnostic
	}
	return &FetchError{Kind: failure.Kind, Err: failure.Err, Diagnostic: diagnostic}
}

func publicIP(ip net.IP) bool {
	return netpolicy.PublicIP(ip)
}

func containsParentSegment(value string) bool {
	for segment := range strings.SplitSeq(value, "/") {
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
