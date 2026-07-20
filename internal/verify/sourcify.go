package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SourcifyClient struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxBytes   int64
	// AllowHTTP exists only for isolated tests and explicitly private
	// development. Production Sourcify traffic must remain HTTPS.
	AllowHTTP bool
}

type SourcifyContract struct {
	Match       string          `json:"match"`
	Creation    json.RawMessage `json:"creation,omitempty"`
	RuntimeCode json.RawMessage `json:"runtimeCode,omitempty"`
	Sources     json.RawMessage `json:"sources,omitempty"`
	Compilation json.RawMessage `json:"compilation,omitempty"`
}

type SourcifyTicket struct {
	VerificationID string `json:"verificationId"`
}

type SourcifyJob struct {
	VerificationID string          `json:"verificationId"`
	Status         string          `json:"status"`
	Error          json.RawMessage `json:"error,omitempty"`
}

func (c SourcifyClient) Lookup(ctx context.Context, chainID uint64, address string) (SourcifyContract, error) {
	var result SourcifyContract
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v2/contract/%d/%s?fields=all", chainID, url.PathEscape(address)), nil, &result)
	return result, err
}

func (c SourcifyClient) Submit(ctx context.Context, request Request, consent bool) (SourcifyTicket, error) {
	if !consent || !request.SubmitToSourcify {
		return SourcifyTicket{}, ErrConsentRequired
	}
	payload := struct {
		StandardJSON       json.RawMessage `json:"stdJsonInput"`
		CompilerVersion    string          `json:"compilerVersion"`
		ContractIdentifier string          `json:"contractIdentifier"`
	}{request.StandardJSON, request.CompilerVersion, request.ContractIdentifier}
	var ticket SourcifyTicket
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v2/verify/%d/%s", request.ChainID, url.PathEscape(request.Address)), payload, &ticket)
	if err == nil && ticket.VerificationID == "" {
		err = errors.New("Sourcify response did not contain a verification ID")
	}
	return ticket, err
}

func (c SourcifyClient) Status(ctx context.Context, verificationID string) (SourcifyJob, error) {
	if verificationID == "" || strings.ContainsAny(verificationID, "/?#") {
		return SourcifyJob{}, errors.New("invalid Sourcify verification ID")
	}
	var job SourcifyJob
	err := c.do(ctx, http.MethodGet, "/v2/verify/"+url.PathEscape(verificationID), nil, &job)
	return job, err
}

func (c SourcifyClient) do(ctx context.Context, method, path string, payload, target any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://sourcify.dev/server"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "https" && !(c.AllowHTTP && parsed.Scheme == "http")) {
		return errors.New("invalid Sourcify base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + strings.Split(path, "?")[0]
	if queryAt := strings.IndexByte(path, '?'); queryAt >= 0 {
		parsed.RawQuery = path[queryAt+1:]
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Sourcify request: %w", err)
	}
	defer response.Body.Close()
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return errors.New("Sourcify response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Sourcify HTTP %s: %s", strconv.Itoa(response.StatusCode), strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode Sourcify response: %w", err)
	}
	return nil
}
