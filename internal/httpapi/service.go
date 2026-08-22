package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/observability"
)

func NewService(cfg config.Config, handler http.Handler, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return &Service{
		server: &http.Server{
			Addr:              cfg.Server.Address,
			Handler:           handler,
			ErrorLog:          observability.HTTPServerErrorLog(logger),
			ReadHeaderTimeout: cfg.Server.ReadTimeout,
			ReadTimeout:       cfg.Server.ReadTimeout,
			WriteTimeout:      cfg.Server.WriteTimeout,
			IdleTimeout:       60 * time.Second,
		},
		listen:          net.Listen,
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		tlsCertFile:     cfg.Server.TLSCertFile,
		tlsKeyFile:      cfg.Server.TLSKeyFile,
	}
}

func (s *Service) Name() string { return "http-api" }

func (s *Service) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("API service context is nil")
	}
	if (s.tlsCertFile == "") != (s.tlsKeyFile == "") {
		return errors.New("API TLS certificate and key must be configured together")
	}
	tlsEnabled := s.tlsCertFile != "" && s.tlsKeyFile != ""
	if tlsEnabled {
		certificate, err := tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("load API TLS key pair: %w", err)
		}
		s.server.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
	}
	// Every accepted request inherits the component lifecycle. Canceling the
	// service therefore terminates long-lived SSE handlers before Shutdown waits
	// for active connections to become idle.
	s.server.BaseContext = func(net.Listener) context.Context { return ctx }
	listener, err := s.listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		if tlsEnabled {
			done <- s.server.ServeTLS(listener, "", "")
			return
		}
		done <- s.server.Serve(listener)
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		timeout := s.shutdownTimeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			shutdownErr := fmt.Errorf("shutdown: %w", err)
			closeErr := s.server.Close()
			serveErr := <-done
			if errors.Is(closeErr, http.ErrServerClosed) {
				closeErr = nil
			}
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
			return errors.Join(shutdownErr, closeErr, serveErr)
		}
		err := <-done
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	}
}

type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func randomRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(value[:])
}
