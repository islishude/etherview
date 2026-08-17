package metadata

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/islishude/etherview/internal/netpolicy"
)

const (
	maximumDiagnosticIPs      = 8
	maximumDiagnosticHost     = 253
	maximumClearIPFSPathBytes = 1024
)

// FetchPhase is a closed hostile-boundary phase used only for bounded
// operational diagnostics.
type FetchPhase string

const (
	FetchPhaseNone          FetchPhase = "none"
	FetchPhaseURL           FetchPhase = "url"
	FetchPhaseRedirect      FetchPhase = "redirect"
	FetchPhaseDNS           FetchPhase = "dns"
	FetchPhaseNetworkPolicy FetchPhase = "network_policy"
	FetchPhaseTransport     FetchPhase = "transport"
	FetchPhaseHTTP          FetchPhase = "http"
	FetchPhaseContent       FetchPhase = "content"
	FetchPhaseDocument      FetchPhase = "document"
	FetchPhaseCanonicality  FetchPhase = "canonicality"
)

// FetchFailureReason is a closed, credential-free diagnosis for one failed
// hostile-boundary operation. It must never contain text derived from an
// upstream error, URL, response body, or host.
type FetchFailureReason string

const (
	FetchFailureDNSLookupFailed       FetchFailureReason = "dns_lookup_failed"
	FetchFailureDNSTimeout            FetchFailureReason = "dns_timeout"
	FetchFailureNetworkPolicyRejected FetchFailureReason = "network_policy_rejected"
	FetchFailureConnectTimeout        FetchFailureReason = "connect_timeout"
	FetchFailureConnectionRefused     FetchFailureReason = "connection_refused"
	FetchFailureNetworkUnreachable    FetchFailureReason = "network_unreachable"
	FetchFailureTLSHandshakeTimeout   FetchFailureReason = "tls_handshake_timeout"
	FetchFailureTLSCertificateInvalid FetchFailureReason = "tls_certificate_invalid"
	FetchFailureTLSProtocolError      FetchFailureReason = "tls_protocol_error"
	FetchFailureRequestTimeout        FetchFailureReason = "request_timeout"
	FetchFailureTransportError        FetchFailureReason = "transport_error"
)

type fetchTransportStage uint8

const (
	fetchTransportNone fetchTransportStage = iota
	fetchTransportDNS
	fetchTransportConnect
	fetchTransportTLS
	fetchTransportRequest
)

// FetchDiagnostic contains only bounded, credential-free facts about one
// metadata fetch. Arbitrary HTTPS paths are represented by length and digest;
// RequestPath is populated only for the public IPFS content path.
type FetchDiagnostic struct {
	SourceScheme string

	RequestMethod     string
	RequestScheme     string
	RequestHost       string
	RequestPort       string
	RequestPath       string
	RequestPathLength int
	RequestPathSHA256 string
	RequestPathHidden bool
	QueryPresent      bool
	RedirectCount     int

	ResolvedIPs           []string
	ResolvedIPCount       int
	ResolvedIPsTruncated  bool
	ConnectedIP           string
	RejectedIPs           []string
	RejectedIPCount       int
	RejectedIPsTruncated  bool
	RejectedReasons       []string
	RejectedPrefixes      []string
	NetworkPolicyBypassed bool

	Phase  FetchPhase
	Reason FetchFailureReason
}

type fetchDiagnosticContextKey struct{}

type fetchDiagnosticCollector struct {
	mu                   sync.Mutex
	unsafePrivateNetwork bool
	transportStage       fetchTransportStage
	diagnostic           FetchDiagnostic
}

func newFetchDiagnosticCollector(rawSource string) *fetchDiagnosticCollector {
	return &fetchDiagnosticCollector{diagnostic: FetchDiagnostic{
		SourceScheme:  normalizedSourceScheme(rawSource),
		RequestMethod: http.MethodGet,
		Phase:         FetchPhaseNone,
	}}
}

func withFetchDiagnostic(ctx context.Context, collector *fetchDiagnosticCollector) context.Context {
	ctx = context.WithValue(ctx, fetchDiagnosticContextKey{}, collector)
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if collector != nil && info.Conn != nil {
				collector.recordConnectedAddress(info.Conn.RemoteAddr())
			}
		},
		TLSHandshakeStart: func() {
			collector.setTransportStage(fetchTransportTLS)
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				collector.recordTLSFailure(err)
				return
			}
			collector.setTransportStage(fetchTransportRequest)
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			collector.setTransportStage(fetchTransportRequest)
		},
	})
}

func fetchDiagnosticFromContext(ctx context.Context) *fetchDiagnosticCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(fetchDiagnosticContextKey{}).(*fetchDiagnosticCollector)
	return collector
}

func (collector *fetchDiagnosticCollector) setTarget(target *url.URL, publicIPFSPath string, redirect bool) {
	if collector == nil || target == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if redirect {
		collector.diagnostic.RedirectCount++
	}
	collector.diagnostic.RequestScheme = normalizedRequestScheme(target.Scheme)
	collector.diagnostic.RequestHost = boundedDiagnosticHost(target.Hostname())
	collector.diagnostic.RequestPort = normalizedRequestPort(target)
	collector.diagnostic.QueryPresent = target.RawQuery != ""
	collector.diagnostic.RequestPath = ""
	collector.diagnostic.RequestPathHidden = true

	escapedPath := target.EscapedPath()
	if escapedPath == "" {
		escapedPath = "/"
	}
	collector.diagnostic.RequestPathLength = len(escapedPath)
	digest := sha256.Sum256([]byte(escapedPath))
	collector.diagnostic.RequestPathSHA256 = hex.EncodeToString(digest[:])
	if publicIPFSPath == "" {
		return
	}
	clearPath := (&url.URL{Path: publicIPFSPath}).EscapedPath()
	if clearPath == "" {
		clearPath = "/"
	}
	if len(clearPath) <= maximumClearIPFSPathBytes {
		collector.diagnostic.RequestPath = clearPath
		collector.diagnostic.RequestPathHidden = false
	}
}

func (collector *fetchDiagnosticCollector) setPhase(phase FetchPhase) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.diagnostic.Phase = phase
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) setFailure(phase FetchPhase, reason FetchFailureReason) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.diagnostic.Phase = phase
	collector.diagnostic.Reason = reason
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) setTransportStage(stage fetchTransportStage) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.transportStage = stage
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) transportStageSnapshot() fetchTransportStage {
	if collector == nil {
		return fetchTransportNone
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.transportStage
}

func (collector *fetchDiagnosticCollector) allowUnsafePrivateNetworks(allowed bool) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.unsafePrivateNetwork = allowed
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) recordResolved(addresses []net.IPAddr) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.diagnostic.ResolvedIPCount += len(addresses)
	for _, candidate := range addresses {
		address, ok := canonicalDiagnosticIP(candidate.IP)
		if !ok {
			continue
		}
		collector.diagnostic.ResolvedIPs = appendBoundedUnique(
			collector.diagnostic.ResolvedIPs, address,
		)
		decision := netpolicy.ClassifyIP(candidate.IP)
		if decision.Allowed {
			continue
		}
		collector.diagnostic.RejectedIPCount++
		collector.diagnostic.RejectedIPs = appendBoundedUnique(
			collector.diagnostic.RejectedIPs, address,
		)
		collector.diagnostic.RejectedReasons = appendBoundedUnique(
			collector.diagnostic.RejectedReasons, string(decision.Classification),
		)
		if decision.Prefix != "" {
			collector.diagnostic.RejectedPrefixes = appendBoundedUnique(
				collector.diagnostic.RejectedPrefixes, decision.Prefix,
			)
		}
	}
	collector.diagnostic.ResolvedIPsTruncated =
		collector.diagnostic.ResolvedIPCount > len(collector.diagnostic.ResolvedIPs)
	collector.diagnostic.RejectedIPsTruncated =
		collector.diagnostic.RejectedIPCount > len(collector.diagnostic.RejectedIPs)
}

func (collector *fetchDiagnosticCollector) recordConnectedIP(ip net.IP) {
	if collector == nil {
		return
	}
	address, ok := canonicalDiagnosticIP(ip)
	if !ok {
		return
	}
	collector.mu.Lock()
	collector.diagnostic.ConnectedIP = address
	if collector.unsafePrivateNetwork && !netpolicy.ClassifyIP(ip).Allowed {
		collector.diagnostic.NetworkPolicyBypassed = true
	}
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) recordNetworkPolicyBypass() {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.diagnostic.NetworkPolicyBypassed = true
	collector.mu.Unlock()
}

func (collector *fetchDiagnosticCollector) recordConnectedAddress(address net.Addr) {
	if address == nil {
		return
	}
	switch value := address.(type) {
	case *net.TCPAddr:
		collector.recordConnectedIP(value.IP)
		return
	case *net.UDPAddr:
		collector.recordConnectedIP(value.IP)
		return
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return
	}
	collector.recordConnectedIP(net.ParseIP(host))
}

func (collector *fetchDiagnosticCollector) snapshot() FetchDiagnostic {
	if collector == nil {
		return FetchDiagnostic{}
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	diagnostic := collector.diagnostic
	diagnostic.ResolvedIPs = append([]string(nil), diagnostic.ResolvedIPs...)
	diagnostic.RejectedIPs = append([]string(nil), diagnostic.RejectedIPs...)
	diagnostic.RejectedReasons = append([]string(nil), diagnostic.RejectedReasons...)
	diagnostic.RejectedPrefixes = append([]string(nil), diagnostic.RejectedPrefixes...)
	return diagnostic
}

func diagnosticForSource(rawSource string, phase FetchPhase) FetchDiagnostic {
	collector := newFetchDiagnosticCollector(rawSource)
	collector.setPhase(phase)
	return collector.snapshot()
}

func diagnosticWithPhase(diagnostic FetchDiagnostic, phase FetchPhase) FetchDiagnostic {
	diagnostic.Phase = phase
	return diagnostic
}

func normalizedSourceScheme(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "invalid"
	}
	switch strings.ToLower(parsed.Scheme) {
	case "ipfs", "https", "http":
		return strings.ToLower(parsed.Scheme)
	case "":
		return "invalid"
	default:
		return "other"
	}
}

func normalizedRequestScheme(raw string) string {
	switch strings.ToLower(raw) {
	case "https", "http":
		return strings.ToLower(raw)
	case "":
		return "invalid"
	default:
		return "other"
	}
}

func normalizedRequestPort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if port := target.Port(); port != "" {
		if len(port) <= 5 {
			return port
		}
		return ""
	}
	switch strings.ToLower(target.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func boundedDiagnosticHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(host) > maximumDiagnosticHost {
		return ""
	}
	for _, character := range host {
		if character <= 0x20 || character == 0x7f || character == '/' || character == '@' {
			return ""
		}
	}
	return host
}

func canonicalDiagnosticIP(ip net.IP) (string, bool) {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "", false
	}
	return address.Unmap().String(), true
}

func appendBoundedUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	if slices.Contains(values, value) {
		return values
	}
	if len(values) >= maximumDiagnosticIPs {
		return values
	}
	return append(values, value)
}
