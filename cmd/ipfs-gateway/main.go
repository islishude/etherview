// Command ipfs-gateway serves the Preview Kubo read-only HTTPS boundary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// gatewayError is a stable, redacted process failure code.
type gatewayError string

func (e gatewayError) Error() string { return string(e) }

type options struct{ address, healthAddress, upstream, cert, key string }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: ipfs-gateway serve|healthcheck")
		return 2
	}
	if args[0] == "healthcheck" && len(args) == 1 {
		if err := healthcheck(ctx); err != nil {
			_, _ = fmt.Fprintln(stderr, "gateway_not_ready")
			return 1
		}
		return 0
	}
	if args[0] != "serve" {
		_, _ = fmt.Fprintln(stderr, "invalid_command")
		return 2
	}
	o := options{}
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.StringVar(&o.address, "listen", ":8443", "HTTPS listener")
	f.StringVar(&o.upstream, "upstream", "http://ipfs:8080", "Kubo HTTP origin")
	f.StringVar(&o.cert, "tls-cert", "/run/ipfs-tls/tls.crt", "PEM certificate")
	f.StringVar(&o.key, "tls-key", "/run/ipfs-tls/tls.key", "PEM private key")
	o.healthAddress = "127.0.0.1:8081"
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "invalid_arguments")
		return 2
	}
	if err := serve(ctx, o); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func upstreamURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, gatewayError("invalid_upstream")
	}
	return u, nil
}

func healthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8081/health", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return gatewayError("gateway_not_ready")
	}
	return nil
}
