package httpapi

import (
	"net/http"
	"sync"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
)

// One admission pool covers both public SSE routes for the API process. Entries
// exist only while a stream is active, bounding both connections and map size.
type streamAdmission struct {
	mu                 sync.Mutex
	total              int
	clients            map[string]int
	maximum, perClient int
	proxies            auth.TrustedProxySet
}

func newStreamAdmission(security config.SecurityConfig, proxies auth.TrustedProxySet) *streamAdmission {
	return &streamAdmission{clients: make(map[string]int), maximum: security.SSEMaxConnections,
		perClient: security.SSEMaxConnectionsPerClient, proxies: proxies}
}

func (a *streamAdmission) acquire(r *http.Request) (func(), bool) {
	key := a.proxies.ClientIdentity(r)
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Context().Err() != nil || a.total >= a.maximum || a.clients[key] >= a.perClient {
		return nil, false
	}
	a.total++
	a.clients[key]++
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.total--
		a.clients[key]--
		if a.clients[key] == 0 {
			delete(a.clients, key)
		}
	}), true
}
