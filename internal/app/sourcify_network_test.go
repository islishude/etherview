package app

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

func TestSourcifyClientPrivateNetworkConfigurationReachesTransport(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted TLS fixture must not receive an HTTP request")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	cfg := config.Default()
	cfg.Features.Sourcify = true
	cfg.Sourcify.BaseURL = server.URL
	cfg.Sourcify.Timeout = time.Second
	cfg.Sourcify.Attempts = 1
	for _, unsafe := range []bool{false, true} {
		cfg.Sourcify.UnsafeAllowPrivateNetworks = unsafe
		client, err := sourcifyClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		before := connections.Load()
		_, err = client.Lookup(t.Context(), 1, "0x"+strings.Repeat("11", 20))
		if !errors.Is(err, verify.ErrSourcifyUnavailable) {
			t.Fatalf("lookup error: %v", err)
		}
		// Without opt-in, address policy prevents dialing. With opt-in, the
		// connection reaches the local fixture and still fails TLS validation.
		if (connections.Load() > before) != unsafe {
			t.Fatalf("unsafe=%t connections=%d", unsafe, connections.Load()-before)
		}
	}
}
