package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBytes = 8 * 1024 * 1024

type response struct {
	Status int
	Body   json.RawMessage
}

type transport struct {
	client             *http.Client
	api, rpcURL, token string
}

func newTransport(api, rpc, token string) *transport {
	return &transport{client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect refused") }}, api: strings.TrimRight(api, "/"), rpcURL: rpc, token: token}
}

func (t *transport) json(ctx context.Context, url, token string, body any) (response, error) {
	method := http.MethodGet
	var data []byte
	if body != nil {
		method = http.MethodPost
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return response{}, errors.New("invalid request JSON")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return response{}, errors.New("invalid request endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-API-Key", token)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return response{}, errors.New("request failed")
	}
	defer func() { _ = res.Body.Close() }()
	data, err = io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return response{}, errors.New("response read failed")
	}
	if len(data) > maxResponseBytes {
		return response{}, errors.New("response too large")
	}
	if !json.Valid(data) {
		return response{}, errors.New("invalid response JSON")
	}
	return response{Status: res.StatusCode, Body: data}, nil
}

func (t *transport) request(ctx context.Context, path string, body any) (response, error) {
	return t.json(ctx, t.api+path, t.token, body)
}

func (t *transport) rpc(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	res, err := t.json(ctx, t.rpcURL, "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if res.Status != 200 || json.Unmarshal(res.Body, &body) != nil || present(body.Error) || !present(body.Result) {
		return nil, errors.New("genesis RPC unavailable")
	}
	return body.Result, nil
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
