package main

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

func serve(ctx context.Context, o options) error {
	u, err := upstreamURL(o.upstream)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(o.cert, o.key)
	if err != nil {
		return gatewayError("invalid_tls_certificate")
	}
	listener, err := net.Listen("tcp", o.address)
	if err != nil {
		return gatewayError("https_listen_failed")
	}
	defer func() { _ = listener.Close() }()
	healthListener, err := net.Listen("tcp", o.healthAddress)
	if err != nil {
		return gatewayError("health_listen_failed")
	}
	defer func() { _ = healthListener.Close() }()
	transport := proxyTransport()
	defer transport.CloseIdleConnections()
	server := &http.Server{Handler: gatewayHandler(u, transport), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	var ready atomic.Bool
	health := &http.Server{ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		if ctx.Err() != nil || !ready.Load() {
			http.Error(w, "not_ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ready\n")
	})}
	// ServeTLS configures HTTP/2 before accepting connections. The wrapped listener
	// publishes readiness only once the HTTPS serving loop reaches Accept.
	result := make(chan error, 2)
	go func() { result <- server.ServeTLS(&readyListener{Listener: listener, ready: &ready}, "", "") }()
	go func() { result <- health.Serve(healthListener) }()
	var failed bool
	select {
	case <-ctx.Done():
	case <-result:
		failed = true
	}
	ready.Store(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthErr := health.Shutdown(shutdownCtx)
	serverErr := server.Shutdown(shutdownCtx)
	_ = health.Close()
	_ = server.Close()
	if failed {
		return gatewayError("listener_failed")
	}
	if healthErr != nil || serverErr != nil {
		return gatewayError("shutdown_timeout")
	}
	return nil
}

type readyListener struct {
	net.Listener
	ready   *atomic.Bool
	started atomic.Bool
}

func (l *readyListener) Accept() (net.Conn, error) {
	if l.started.CompareAndSwap(false, true) {
		l.ready.Store(true)
	}
	return l.Listener.Accept()
}
