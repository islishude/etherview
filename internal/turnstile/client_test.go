package turnstile

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSiteverify(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"success", `{"success":true,"hostname":"explorer.example","action":"verification"}`, 200, nil},
		{"expired", `{"success":false,"error-codes":["timeout-or-duplicate"]}`, 200, ErrInvalid},
		{"forged", `{"success":false,"error-codes":["invalid-input-response"]}`, 200, ErrInvalid},
		{"hostname", `{"success":true,"hostname":"evil.example","action":"verification"}`, 200, ErrInvalid},
		{"action", `{"success":true,"hostname":"explorer.example","action":"csv_export"}`, 200, ErrInvalid},
		{"secret", `{"success":false,"error-codes":["invalid-input-secret"]}`, 200, ErrUnavailable},
		{"internal", `{"success":false,"error-codes":["internal-error"]}`, 200, ErrUnavailable},
		{"missing success", `{}`, 200, ErrUnavailable},
		{"malformed", `not JSON`, 200, ErrUnavailable},
		{"large", strings.Repeat("x", 16385), 200, ErrUnavailable},
		{"trailing", `{"success":false} {}`, 200, ErrUnavailable},
		{"redirect", ``, 302, ErrUnavailable},
		{"upstream failure", `secret upstream error`, 500, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New("secret-key", "https://explorer.example:8443")
			calls := 0
			client.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != endpoint || r.Method != "POST" {
					t.Fatal("incorrect endpoint")
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("unbounded request")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("secret") != "secret-key" || r.Form.Get("response") != "token" || len(r.Form) != 2 {
					t.Fatal("incorrect payload")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://evil.example"}}}, nil
			})
			if err := client.Verify(t.Context(), "token", "verification"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if calls != 1 {
				t.Fatalf("requests=%d", calls)
			}
		})
	}
}

func TestTokenBoundsAndCancellation(t *testing.T) {
	c := New("secret", "https://explorer.example")
	calls := 0
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for _, token := range []string{"", strings.Repeat("x", 2049), " token "} {
		if err := c.Verify(t.Context(), token, "verification"); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid token contacted upstream")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Verify(ctx, "token", "verification"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := c.Verify(ctx, "token", "verification"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
