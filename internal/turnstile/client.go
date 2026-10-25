// Package turnstile validates single-use browser challenges without retaining tokens.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalid     = errors.New("turnstile_invalid")
	ErrUnavailable = errors.New("turnstile_unavailable")
)

const endpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Client only contacts Cloudflare's fixed HTTPS validation endpoint.
type Client struct {
	secret, hostname string
	http             *http.Client
}

func New(secret, publicURL string) *Client {
	hostname := ""
	if u, err := url.Parse(publicURL); err == nil {
		hostname = u.Hostname()
	}
	return &Client{secret: secret, hostname: hostname, http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Verify never exposes remote errors and never retries a consumed token.
func (c *Client) Verify(ctx context.Context, token, action string) error {
	if token == "" || len(token) > 2048 || strings.TrimSpace(token) != token {
		return ErrInvalid
	}
	if c.secret == "" || c.hostname == "" {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body := url.Values{"secret": {c.secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(data) > 16<<10 {
		return ErrUnavailable
	}
	var result struct {
		Success  *bool    `json:"success"`
		Action   string   `json:"action"`
		Hostname string   `json:"hostname"`
		Errors   []string `json:"error-codes"`
	}
	if json.Unmarshal(data, &result) != nil || result.Success == nil {
		return ErrUnavailable
	}
	if !*result.Success {
		for _, code := range result.Errors {
			switch code {
			case "internal-error", "missing-input-secret", "invalid-input-secret":
				return ErrUnavailable
			}
		}
		return ErrInvalid
	}
	if result.Action != action || !strings.EqualFold(result.Hostname, c.hostname) {
		return ErrInvalid
	}
	return nil
}
