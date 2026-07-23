package auth

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
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
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewMemoryLimiter(now func() time.Time) *MemoryLimiter {
	if now == nil {
		now = time.Now
	}
	return &MemoryLimiter{buckets: make(map[string]*bucket), now: now}
}

func (l *MemoryLimiter) Allow(_ context.Context, key string, limit Limit) (bool, time.Duration) {
	if limit.Rate <= 0 || limit.Burst <= 0 {
		return false, time.Second
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.buckets[key]
	if state == nil {
		state = &bucket{tokens: float64(limit.Burst), last: now}
		l.buckets[key] = state
	}
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

type RateMiddleware struct {
	Limiter   Limiter
	Anonymous Limit
	Observer  RateObserver
}

func (m RateMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := IdentityFrom(r.Context())
		key := "anonymous:" + remoteIP(r.RemoteAddr)
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

func remoteIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}
