package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultHealthcheckURL     = "http://127.0.0.1:9090/health/ready"
	defaultHealthcheckTimeout = 2 * time.Second
	maximumHealthcheckTimeout = 10 * time.Second
)

func (p Program) runHealthcheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("url", defaultHealthcheckURL, "loopback operational readiness URL")
	timeout := fs.Duration("timeout", defaultHealthcheckTimeout, "request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("healthcheck does not accept positional arguments")
	}
	if *timeout <= 0 || *timeout > maximumHealthcheckTimeout {
		return fmt.Errorf(
			"healthcheck timeout must be greater than zero and no more than %s",
			maximumHealthcheckTimeout,
		)
	}
	parsed, err := parseHealthcheckURL(*endpoint)
	if err != nil {
		return err
	}

	requestContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return errors.New("create healthcheck request")
	}
	dialer := &net.Dialer{Timeout: *timeout}
	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: *timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("health endpoint unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func parseHealthcheckURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "/health/ready" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, errors.New(
			"healthcheck URL must be an absolute loopback HTTP /health/ready URL without credentials, query, or fragment",
		)
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("healthcheck URL host must be a numeric loopback address")
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return nil, errors.New("healthcheck URL must include a valid TCP port")
	}
	return parsed, nil
}
