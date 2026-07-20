package ethrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
)

const defaultMaxResponseBytes int64 = 32 << 20

type Caller interface {
	Call(ctx context.Context, method string, params []any, result any) error
}

type BatchCaller interface {
	Caller
	BatchCall(ctx context.Context, elements []BatchElem) error
}

type BatchElem struct {
	Method string
	Params []any
	Result any
	Error  error
}

type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

func IsMethodNotFound(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32601
}

type HTTPStatusError struct{ StatusCode int }

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("JSON-RPC HTTP status %d", e.StatusCode)
}

func (e *HTTPStatusError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

type ProtocolError struct{ Message string }

func (e *ProtocolError) Error() string { return "JSON-RPC protocol error: " + e.Message }

type HTTPClient struct {
	endpoint         string
	httpClient       *http.Client
	maxResponseBytes int64
	nextID           atomic.Uint64
}

type HTTPClientOptions struct {
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

func NewHTTPClient(endpoint string, options HTTPClientOptions) (*HTTPClient, error) {
	if endpoint == "" {
		return nil, errors.New("JSON-RPC endpoint is empty")
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	return &HTTPClient{
		endpoint:         endpoint,
		httpClient:       client,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (c *HTTPClient) Call(ctx context.Context, method string, params []any, result any) error {
	if result == nil {
		return errors.New("JSON-RPC result destination is nil")
	}
	id := c.nextID.Add(1)
	request := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: nonNilParams(params)}
	var response rpcResponse
	if err := c.roundTrip(ctx, request, &response); err != nil {
		return err
	}
	if response.JSONRPC != "2.0" {
		return &ProtocolError{Message: "response has an invalid jsonrpc version"}
	}
	responseID, err := parseResponseID(response.ID)
	if err != nil || responseID != id {
		return &ProtocolError{Message: "response ID does not match request"}
	}
	if response.Error != nil {
		return response.Error
	}
	if response.Result == nil {
		return &ProtocolError{Message: "response contains neither result nor error"}
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode JSON-RPC result for %s: %w", method, err)
	}
	return nil
}

func (c *HTTPClient) BatchCall(ctx context.Context, elements []BatchElem) error {
	if len(elements) == 0 {
		return nil
	}
	requests := make([]rpcRequest, len(elements))
	byID := make(map[uint64]int, len(elements))
	for index := range elements {
		if elements[index].Result == nil {
			return fmt.Errorf("batch element %d has a nil result destination", index)
		}
		elements[index].Error = nil
		id := c.nextID.Add(1)
		requests[index] = rpcRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  elements[index].Method,
			Params:  nonNilParams(elements[index].Params),
		}
		byID[id] = index
	}
	var responses []rpcResponse
	if err := c.roundTrip(ctx, requests, &responses); err != nil {
		return err
	}
	if len(responses) != len(requests) {
		return &ProtocolError{Message: fmt.Sprintf("batch response count %d does not match request count %d", len(responses), len(requests))}
	}
	seen := make(map[uint64]struct{}, len(responses))
	for _, response := range responses {
		if response.JSONRPC != "2.0" {
			return &ProtocolError{Message: "batch response has an invalid jsonrpc version"}
		}
		id, err := parseResponseID(response.ID)
		if err != nil {
			return &ProtocolError{Message: "batch response has an invalid ID"}
		}
		index, exists := byID[id]
		if !exists {
			return &ProtocolError{Message: "batch response contains an unknown ID"}
		}
		if _, duplicate := seen[id]; duplicate {
			return &ProtocolError{Message: "batch response contains a duplicate ID"}
		}
		seen[id] = struct{}{}
		if response.Error != nil {
			elements[index].Error = response.Error
			continue
		}
		if response.Result == nil {
			return &ProtocolError{Message: "batch response contains neither result nor error"}
		}
		if err := json.Unmarshal(response.Result, elements[index].Result); err != nil {
			return fmt.Errorf("decode JSON-RPC batch result for %s: %w", elements[index].Method, err)
		}
	}
	return nil
}

func (c *HTTPClient) roundTrip(ctx context.Context, payload any, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode JSON-RPC request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create JSON-RPC request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send JSON-RPC request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return &HTTPStatusError{StatusCode: response.StatusCode}
	}
	limited := io.LimitReader(response.Body, c.maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read JSON-RPC response: %w", err)
	}
	if int64(len(responseBody)) > c.maxResponseBytes {
		return &ProtocolError{Message: "response exceeds configured size limit"}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON-RPC response envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &ProtocolError{Message: "response contains multiple JSON values"}
		}
		return fmt.Errorf("decode trailing JSON-RPC response data: %w", err)
	}
	return nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

func parseResponseID(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing ID")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.ParseUint(number.String(), 10, 64)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return strconv.ParseUint(text, 10, 64)
}

func nonNilParams(params []any) []any {
	if params == nil {
		return []any{}
	}
	return params
}
