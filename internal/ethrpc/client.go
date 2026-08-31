package ethrpc

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

const (
	defaultMaxResponseBytes  int64 = 32 << 20
	maximumRequestsPerSecond       = 1_000_000_000
)

var (
	ErrInvalidResponse  = errors.New("invalid JSON-RPC response")
	ErrResponseTooLarge = errors.New("JSON-RPC response exceeds configured size limit")
	ErrTransport        = errors.New("JSON-RPC transport is unavailable")
)

type ClientOptions struct {
	HTTPClient        *http.Client
	MaxResponseBytes  int64
	RequestsPerSecond int
}

// NewClient constructs the upstream go-ethereum RPC client and installs the
// application transport boundary beneath it. No endpoint, request parameter,
// response body, or nested upstream error is retained in boundary errors.
func NewClient(ctx context.Context, endpoint string, options ClientOptions) (*rpc.Client, error) {
	if endpoint == "" {
		return nil, errors.New("JSON-RPC endpoint is empty")
	}
	parsedEndpoint, err := url.ParseRequestURI(endpoint)
	if err != nil || parsedEndpoint.Host == "" ||
		parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return nil, errors.New("JSON-RPC endpoint is invalid")
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	if options.RequestsPerSecond < 0 || options.RequestsPerSecond > maximumRequestsPerSecond {
		return nil, errors.New("RPC request rate must be between 1 and 1000000000, or zero to disable limiting")
	}
	baseClient := options.HTTPClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	clientCopy := *baseClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	next := baseClient.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	guard := &guardedRoundTripper{
		next:             next,
		maxResponseBytes: maxResponseBytes,
		endpoint:         parsedEndpoint,
	}
	if options.RequestsPerSecond > 0 {
		guard.limiter = newRequestLimiter(options.RequestsPerSecond)
	}
	clientCopy.Transport = guard
	// rpc.Client and net/http use this credential-free URL in their own error
	// wrappers. The guarded transport substitutes the real endpoint only on the
	// outbound clone, so even local/network failures cannot echo URL secrets.
	safeEndpoint := parsedEndpoint.Scheme + "://rpc.endpoint.invalid"
	return rpc.DialOptions(ctx, safeEndpoint, rpc.WithHTTPClient(&clientCopy))
}

func IsMethodNotFound(err error) bool {
	var rpcErr rpc.Error
	return errors.As(err, &rpcErr) && rpcErr.ErrorCode() == -32601
}

func IsRetryableHTTP(err error) bool {
	var status rpc.HTTPError
	if !errors.As(err, &status) {
		return false
	}
	return status.StatusCode == http.StatusTooManyRequests || status.StatusCode >= 500
}

type guardedRoundTripper struct {
	next             http.RoundTripper
	maxResponseBytes int64
	limiter          *requestLimiter
	endpoint         *url.URL
}

func (transport *guardedRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport == nil || transport.next == nil {
		scrubRequestURL(request)
		return nil, ErrTransport
	}
	expected, batch, err := requestIDs(request)
	if err != nil {
		scrubRequestURL(request)
		return nil, ErrInvalidResponse
	}
	if transport.limiter != nil {
		if err := transport.limiter.acquire(request.Context()); err != nil {
			scrubRequestURL(request)
			return nil, err
		}
	}
	outbound := request.Clone(request.Context())
	outbound.URL = cloneURL(transport.endpoint)
	outbound.Host = transport.endpoint.Host
	if transport.endpoint.User != nil {
		password, _ := transport.endpoint.User.Password()
		outbound.SetBasicAuth(transport.endpoint.User.Username(), password)
	}
	response, err := transport.next.RoundTrip(outbound)
	if err != nil {
		scrubRequestURL(request)
		if contextErr := request.Context().Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, ErrTransport
	}
	if response == nil {
		scrubRequestURL(request)
		return nil, ErrTransport
	}
	response.Request = request
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainAndClose(response.Body, 4<<10)
		response.Body = io.NopCloser(bytes.NewReader(nil))
		response.ContentLength = 0
		response.Status = strconv.Itoa(response.StatusCode)
		if statusText := http.StatusText(response.StatusCode); statusText != "" {
			response.Status += " " + statusText
		}
		return response, nil
	}
	body, readErr := readBoundedResponse(response.Body, transport.maxResponseBytes)
	if readErr != nil {
		scrubRequestURL(request)
		return nil, readErr
	}
	if err := validateResponse(body, expected, batch); err != nil {
		scrubRequestURL(request)
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

func requestIDs(request *http.Request) (map[string]struct{}, bool, error) {
	if request == nil || request.GetBody == nil {
		return nil, false, ErrInvalidResponse
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, false, ErrInvalidResponse
	}
	defer body.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return nil, false, ErrInvalidResponse
	}
	raw, err := oneJSONValue(data)
	if err != nil {
		return nil, false, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, ErrInvalidResponse
	}
	if trimmed[0] == '[' {
		var requests []map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &requests); err != nil || len(requests) == 0 {
			return nil, true, ErrInvalidResponse
		}
		ids := make(map[string]struct{}, len(requests))
		for _, envelope := range requests {
			id, ok := envelope["id"]
			if !ok || isNullJSON(id) {
				return nil, true, ErrInvalidResponse
			}
			key := string(bytes.TrimSpace(id))
			if _, duplicate := ids[key]; duplicate {
				return nil, true, ErrInvalidResponse
			}
			ids[key] = struct{}{}
		}
		return ids, true, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, false, ErrInvalidResponse
	}
	id, ok := envelope["id"]
	if !ok || isNullJSON(id) {
		return nil, false, ErrInvalidResponse
	}
	return map[string]struct{}{string(bytes.TrimSpace(id)): {}}, false, nil
}

func validateResponse(body []byte, expected map[string]struct{}, batch bool) error {
	raw, err := oneJSONValue(body)
	if err != nil {
		return err
	}
	if batch {
		var responses []json.RawMessage
		if err := json.Unmarshal(raw, &responses); err != nil || len(responses) != len(expected) {
			return ErrInvalidResponse
		}
		seen := make(map[string]struct{}, len(responses))
		for _, response := range responses {
			id, err := validateResponseEnvelope(response)
			if err != nil {
				return err
			}
			if _, exists := expected[id]; !exists {
				return ErrInvalidResponse
			}
			if _, duplicate := seen[id]; duplicate {
				return ErrInvalidResponse
			}
			seen[id] = struct{}{}
		}
		if len(seen) != len(expected) {
			return ErrInvalidResponse
		}
		return nil
	}
	id, err := validateResponseEnvelope(raw)
	if err != nil {
		return err
	}
	if _, exists := expected[id]; !exists || len(expected) != 1 {
		return ErrInvalidResponse
	}
	return nil
}

func validateResponseEnvelope(raw json.RawMessage) (string, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return "", ErrInvalidResponse
	}
	var version string
	if err := json.Unmarshal(envelope["jsonrpc"], &version); err != nil || version != "2.0" {
		return "", ErrInvalidResponse
	}
	id, exists := envelope["id"]
	if !exists || isNullJSON(id) {
		return "", ErrInvalidResponse
	}
	_, hasResult := envelope["result"]
	errorValue, hasError := envelope["error"]
	if hasResult == hasError || hasError && isNullJSON(errorValue) {
		return "", ErrInvalidResponse
	}
	return string(bytes.TrimSpace(id)), nil
}

func oneJSONValue(data []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, ErrInvalidResponse
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidResponse
	}
	return raw, nil
}

func readBoundedResponse(body io.ReadCloser, maximum int64) ([]byte, error) {
	if body == nil {
		return nil, ErrInvalidResponse
	}
	defer body.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil {
		return nil, ErrTransport
	}
	if int64(len(data)) > maximum {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

func drainAndClose(body io.ReadCloser, maximum int64) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maximum))
	_ = body.Close()
}

func scrubRequestURL(request *http.Request) {
	if request == nil {
		return
	}
	request.URL = &url.URL{Scheme: "rpc", Host: "endpoint"}
}

func cloneURL(input *url.URL) *url.URL {
	if input == nil {
		return &url.URL{}
	}
	copy := *input
	if input.User != nil {
		user := *input.User
		copy.User = &user
	}
	return &copy
}

func isNullJSON(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

type requestLimiter struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time
	waiters  list.List
	dispatch bool
}

type requestLimiterWaiter struct {
	ready   chan struct{}
	element *list.Element
}

func newRequestLimiter(requestsPerSecond int) *requestLimiter {
	return &requestLimiter{
		interval: time.Second / time.Duration(requestsPerSecond),
		now:      time.Now,
		after:    time.After,
	}
}

func (limiter *requestLimiter) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waiter := &requestLimiterWaiter{ready: make(chan struct{})}
	limiter.mu.Lock()
	if err := ctx.Err(); err != nil {
		limiter.mu.Unlock()
		return err
	}
	now := limiter.now()
	if !limiter.dispatch && limiter.waiters.Len() == 0 && !limiter.next.After(now) {
		limiter.next = now.Add(limiter.interval)
		limiter.mu.Unlock()
		return nil
	}
	waiter.element = limiter.waiters.PushBack(waiter)
	if !limiter.dispatch {
		limiter.dispatch = true
		go limiter.dispatchWaiters()
	}
	limiter.mu.Unlock()
	select {
	case <-waiter.ready:
		return nil
	case <-ctx.Done():
		limiter.mu.Lock()
		if waiter.element != nil {
			limiter.waiters.Remove(waiter.element)
			waiter.element = nil
			limiter.mu.Unlock()
			return ctx.Err()
		}
		limiter.mu.Unlock()
		// The grant linearized before cancellation. Treat it as acquired so a
		// canceled select cannot discard a slot that later callers must honor.
		return nil
	}
}

func (limiter *requestLimiter) dispatchWaiters() {
	for {
		limiter.mu.Lock()
		front := limiter.waiters.Front()
		if front == nil {
			limiter.dispatch = false
			limiter.mu.Unlock()
			return
		}
		now := limiter.now()
		if limiter.next.After(now) {
			wait := limiter.next.Sub(now)
			after := limiter.after
			limiter.mu.Unlock()
			<-after(wait)
			continue
		}
		waiter, ok := front.Value.(*requestLimiterWaiter)
		if !ok || waiter == nil {
			limiter.waiters.Remove(front)
			limiter.mu.Unlock()
			continue
		}
		limiter.waiters.Remove(front)
		waiter.element = nil
		limiter.next = now.Add(limiter.interval)
		close(waiter.ready)
		limiter.mu.Unlock()
	}
}
