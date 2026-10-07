package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const streamIdleTimeout = 30 * time.Second

type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *idleConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func proxyTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &idleConn{Conn: c, timeout: streamIdleTimeout}, nil
		}, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 60 * time.Second,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 64,
		MaxResponseHeaderBytes: 1 << 20, DisableCompression: true,
	}
}

type streamWriter struct {
	http.ResponseWriter
	idle time.Duration
}

func (w *streamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *streamWriter) Write(p []byte) (int, error) {
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(w.idle)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}
func (w *streamWriter) FlushError() error {
	c := http.NewResponseController(w.ResponseWriter)
	if err := c.SetWriteDeadline(time.Now().Add(w.idle)); err != nil {
		return err
	}
	return c.Flush()
}

// Validate decoded segments without cleaning or redirecting the original URL.
func allowedPath(u *url.URL) bool {
	if !strings.HasPrefix(u.EscapedPath(), "/ipfs/") {
		return false
	}
	for part := range strings.SplitSeq(strings.TrimPrefix(u.EscapedPath(), "/ipfs/"), "/") {
		// Check nested escapes too, but retain ordinary percent signs in names.
		for depth := 0; ; depth++ {
			if depth == 8 {
				return false
			}
			decoded, err := url.PathUnescape(part)
			if err != nil {
				break
			}
			if decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\\x00\r\n") {
				return false
			}
			if decoded == part {
				break
			}
			part = decoded
		}
	}
	return true
}

func gatewayHandler(upstream *url.URL, transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(upstream)
			// The gateway does not interpret queries; preserve their wire encoding.
			p.Out.URL.RawQuery = p.In.URL.RawQuery
			p.Out.Host = "ipfs.preview.test"
			p.Out.Header.Del("Forwarded")
			p.Out.Header.Del("X-Forwarded-Host")
			p.Out.Header.Del("X-Forwarded-Proto")
			p.Out.Header.Del("X-Forwarded-For")
		}, Transport: transport, FlushInterval: -1,
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "upstream_unavailable", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &streamWriter{ResponseWriter: w, idle: streamIdleTimeout}
		if !allowedPath(r.URL) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method_forbidden", http.StatusForbidden)
			return
		}
		// Read-only requests never forward a body or protocol upgrade to Kubo.
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.Header.Get("Upgrade") != "" {
			http.Error(w, "invalid_request", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
