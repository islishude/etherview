package auth

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAnonymousBucketLimit     = 16_384
	defaultAuthenticatedBucketLimit = 65_536
	defaultAnonymousBucketTTL       = 10 * time.Minute
	defaultAuthenticatedBucketTTL   = 10 * time.Minute
	maxForwardedForBytes            = 4 << 10
	maxForwardedForHops             = 32
)

type Limit struct {
	Rate  int
	Burst int
}

type Limiter interface {
	Allow(context.Context, string, Limit) (bool, time.Duration)
}

type RateObserver interface {
	RecordRateLimit(string)
}

type MemoryLimiter struct {
	mu                      sync.Mutex
	buckets                 map[string]*bucket
	anonymousOrder          *list.List
	authenticatedOrder      *list.List
	anonymousBuckets        int
	authenticatedBuckets    int
	maxAnonymousBuckets     int
	maxAuthenticatedBuckets int
	anonymousBucketTTL      time.Duration
	authenticatedBucketTTL  time.Duration
	now                     func() time.Time
}

type bucket struct {
	key                  string
	tokens               float64
	last                 time.Time
	lastSeen             time.Time
	retention            time.Duration
	anonymousElement     *list.Element
	authenticatedElement *list.Element
}

func NewMemoryLimiter(now func() time.Time) *MemoryLimiter {
	if now == nil {
		now = time.Now
	}
	return &MemoryLimiter{
		buckets:                 make(map[string]*bucket),
		anonymousOrder:          list.New(),
		authenticatedOrder:      list.New(),
		maxAnonymousBuckets:     defaultAnonymousBucketLimit,
		maxAuthenticatedBuckets: defaultAuthenticatedBucketLimit,
		anonymousBucketTTL:      defaultAnonymousBucketTTL,
		authenticatedBucketTTL:  defaultAuthenticatedBucketTTL,
		now:                     now,
	}
}

func (l *MemoryLimiter) Allow(_ context.Context, key string, limit Limit) (bool, time.Duration) {
	if l == nil || limit.Rate <= 0 || limit.Burst <= 0 {
		return false, time.Second
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	anonymous := strings.HasPrefix(key, "anonymous:")
	l.expireBuckets(now, true)
	l.expireBuckets(now, false)
	state := l.buckets[key]
	if state == nil {
		if !l.hasBucketRoom(anonymous) {
			return false, l.bucketRoomRetry(now, anonymous)
		}
		state = &bucket{
			key: key, tokens: float64(limit.Burst), last: now, lastSeen: now,
			retention: l.bucketRetention(limit, anonymous),
		}
		if anonymous {
			state.anonymousElement = l.anonymousOrder.PushBack(state)
			l.anonymousBuckets++
		} else {
			state.authenticatedElement = l.authenticatedOrder.PushBack(state)
			l.authenticatedBuckets++
		}
		l.buckets[key] = state
	} else if state.anonymousElement != nil {
		l.anonymousOrder.MoveToBack(state.anonymousElement)
	} else if state.authenticatedElement != nil {
		l.authenticatedOrder.MoveToBack(state.authenticatedElement)
	}
	state.lastSeen = now
	state.retention = l.bucketRetention(limit, anonymous)
	state.tokens = math.Min(float64(limit.Burst), state.tokens)
	elapsed := now.Sub(state.last).Seconds()
	if elapsed > 0 {
		state.tokens = math.Min(float64(limit.Burst), state.tokens+elapsed*float64(limit.Rate))
		state.last = now
	}
	if state.tokens >= 1 {
		state.tokens--
		return true, 0
	}
	retry := time.Duration(math.Ceil((1-state.tokens)/float64(limit.Rate)*1000)) * time.Millisecond
	return false, retry
}

func (l *MemoryLimiter) expireBuckets(now time.Time, anonymous bool) {
	order := l.authenticatedOrder
	if anonymous {
		order = l.anonymousOrder
	}
	if order == nil {
		return
	}
	for element := order.Front(); element != nil; element = order.Front() {
		state, ok := element.Value.(*bucket)
		if !ok || state == nil {
			order.Remove(element)
			continue
		}
		if now.Sub(state.lastSeen) < state.retention {
			return
		}
		l.removeBucket(state)
	}
}

func (l *MemoryLimiter) hasBucketRoom(anonymous bool) bool {
	maximum := l.maxAuthenticatedBuckets
	count := l.authenticatedBuckets
	if anonymous {
		maximum = l.maxAnonymousBuckets
		count = l.anonymousBuckets
	}
	if maximum <= 0 {
		maximum = defaultAuthenticatedBucketLimit
		if anonymous {
			maximum = defaultAnonymousBucketLimit
		}
	}
	return count < maximum
}

func (l *MemoryLimiter) bucketRoomRetry(now time.Time, anonymous bool) time.Duration {
	order := l.authenticatedOrder
	if anonymous {
		order = l.anonymousOrder
	}
	if order == nil || order.Front() == nil {
		return time.Second
	}
	state, _ := order.Front().Value.(*bucket)
	if state == nil {
		return time.Second
	}
	retry := state.lastSeen.Add(state.retention).Sub(now)
	if retry <= 0 {
		return time.Millisecond
	}
	return retry
}

func (l *MemoryLimiter) bucketRetention(limit Limit, anonymous bool) time.Duration {
	minimum := l.authenticatedBucketTTL
	if anonymous {
		minimum = l.anonymousBucketTTL
	}
	if minimum <= 0 {
		minimum = defaultAuthenticatedBucketTTL
		if anonymous {
			minimum = defaultAnonymousBucketTTL
		}
	}
	fullRefillNanos := math.Ceil(
		float64(limit.Burst) * float64(time.Second) / float64(limit.Rate),
	)
	fullRefill := time.Duration(math.MaxInt64)
	if fullRefillNanos < float64(math.MaxInt64) {
		fullRefill = time.Duration(fullRefillNanos)
	}
	return max(minimum, fullRefill)
}

func (l *MemoryLimiter) removeBucket(state *bucket) {
	if state == nil {
		return
	}
	delete(l.buckets, state.key)
	if state.anonymousElement != nil {
		l.anonymousOrder.Remove(state.anonymousElement)
		state.anonymousElement = nil
		l.anonymousBuckets--
	}
	if state.authenticatedElement != nil {
		l.authenticatedOrder.Remove(state.authenticatedElement)
		state.authenticatedElement = nil
		l.authenticatedBuckets--
	}
}

// TrustedProxySet contains only canonical IP addresses and masked CIDR
// prefixes. Its zero value trusts no peer.
type TrustedProxySet struct {
	prefixes []netip.Prefix
}

// NewTrustedProxySet parses a trusted reverse-proxy allowlist. Hostnames,
// zones, IPv4-mapped IPv6 addresses, and non-canonical spellings are rejected
// so configuration and request identity have one stable representation.
func NewTrustedProxySet(values []string) (TrustedProxySet, error) {
	set := TrustedProxySet{prefixes: make([]netip.Prefix, 0, len(values))}
	for index, value := range values {
		prefix, err := canonicalTrustedProxy(value)
		if err != nil {
			return TrustedProxySet{}, fmt.Errorf("trusted proxy entry %d is invalid", index)
		}
		set.prefixes = append(set.prefixes, prefix)
	}
	return set, nil
}

func canonicalTrustedProxy(value string) (netip.Prefix, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return netip.Prefix{}, errors.New("trusted proxy is not canonical")
	}
	if address, err := netip.ParseAddr(value); err == nil {
		if address.Zone() != "" || address.Is4In6() || address.String() != value {
			return netip.Prefix{}, errors.New("trusted proxy address is not canonical")
		}
		return netip.PrefixFrom(address, address.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() ||
		prefix != prefix.Masked() || prefix.String() != value {
		return netip.Prefix{}, errors.New("trusted proxy prefix is not canonical")
	}
	return prefix, nil
}

func (set TrustedProxySet) contains(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	for _, prefix := range set.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

type RateMiddleware struct {
	Limiter        Limiter
	Anonymous      Limit
	Observer       RateObserver
	TrustedProxies TrustedProxySet
}

func (m RateMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := IdentityFrom(r.Context())
		key := "anonymous:" + m.anonymousIdentity(r)
		limit := m.Anonymous
		if identity.Authenticated {
			key = "key:" + identity.Prefix
			limit = Limit{Rate: identity.Rate, Burst: identity.Burst}
		}
		allowed, retry := m.Limiter.Allow(r.Context(), key, limit)
		if m.Observer != nil {
			decision := "allowed"
			if !allowed {
				decision = "rejected"
			}
			m.Observer.RecordRateLimit(decision)
		}
		if !allowed {
			seconds := max(int(math.Ceil(retry.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeAuthError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m RateMiddleware) anonymousIdentity(request *http.Request) string {
	directAddress, validDirect := parseDirectPeer(request.RemoteAddr)
	directIdentity := remoteIP(request.RemoteAddr)
	if !validDirect || !m.TrustedProxies.contains(directAddress) {
		return directIdentity
	}
	forwarded, ok := parseForwardedFor(request.Header.Values("X-Forwarded-For"))
	if !ok || len(forwarded) == 0 {
		return directIdentity
	}
	for _, f := range slices.Backward(forwarded) {
		if !m.TrustedProxies.contains(f) {
			return f.String()
		}
	}
	return forwarded[0].String()
}

func parseForwardedFor(values []string) ([]netip.Addr, bool) {
	if len(values) == 0 {
		return nil, true
	}
	totalBytes := 0
	addresses := make([]netip.Addr, 0, min(len(values), maxForwardedForHops))
	for _, value := range values {
		totalBytes += len(value)
		if totalBytes > maxForwardedForBytes {
			return nil, false
		}
		for raw := range strings.SplitSeq(value, ",") {
			if len(addresses) >= maxForwardedForHops {
				return nil, false
			}
			raw = strings.TrimSpace(raw)
			address, err := netip.ParseAddr(raw)
			if err != nil || address.Zone() != "" {
				return nil, false
			}
			addresses = append(addresses, address.Unmap())
		}
	}
	return addresses, len(addresses) > 0
}

func parseDirectPeer(remote string) (netip.Addr, bool) {
	if addressPort, err := netip.ParseAddrPort(remote); err == nil && addressPort.Addr().Zone() == "" {
		return addressPort.Addr().Unmap(), true
	}
	if address, err := netip.ParseAddr(remote); err == nil && address.Zone() == "" {
		return address.Unmap(), true
	}
	return netip.Addr{}, false
}

func remoteIP(remote string) string {
	if address, ok := parseDirectPeer(remote); ok {
		return address.String()
	}
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}
