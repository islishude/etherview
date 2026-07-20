package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/metadata"
)

const priceObservationKey = "native"

type JSONFetcher interface {
	Fetch(context.Context, string, metadata.Kind) (metadata.Result, error)
}

type PriceOptions struct {
	BaseURL    string
	Freshness  time.Duration
	FailureTTL time.Duration
	Now        func() time.Time
}

type NativePrice struct {
	USD        string    `json:"native_usd"`
	BTC        string    `json:"native_btc"`
	ObservedAt time.Time `json:"observed_at"`
}

type PriceService struct {
	repository repository
	fetcher    JSONFetcher
	baseURL    string
	freshness  time.Duration
	failureTTL time.Duration
	now        func() time.Time
}

func NewPostgresPriceService(db *sql.DB, chainID uint64, fetcher JSONFetcher, options PriceOptions) (*PriceService, error) {
	repository, err := newRepository(db, chainID)
	if err != nil {
		return nil, err
	}
	if fetcher == nil {
		return nil, errors.New("price adapter fetcher is nil")
	}
	baseURL, err := strictHTTPSURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.Freshness <= 0 || options.Freshness > 24*time.Hour {
		return nil, errors.New("price freshness must be between 1ns and 24h")
	}
	if options.FailureTTL <= 0 || options.FailureTTL > time.Hour {
		return nil, errors.New("price failure TTL must be between 1ns and 1h")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &PriceService{
		repository: repository, fetcher: fetcher, baseURL: baseURL,
		freshness: options.Freshness, failureTTL: options.FailureTTL, now: options.Now,
	}, nil
}

func (s *PriceService) NativePrice(ctx context.Context) (NativePrice, error) {
	if s == nil {
		return NativePrice{}, errors.New("price adapter is nil")
	}
	now := s.now().UTC()
	if cached, exists, err := s.repository.fresh(ctx, "price", defaultProviderKey, priceObservationKey, now); err != nil {
		return NativePrice{}, err
	} else if exists {
		if cached.State != "complete" {
			return NativePrice{}, CapabilityError{Capability: "price", State: cached.State, Code: cached.Code}
		}
		return decodeNativePrice(cached.Value, now, s.freshness)
	}
	result, err := s.fetcher.Fetch(ctx, s.baseURL, metadata.KindJSON)
	if err != nil {
		code, state := classifyFetchFailure(err)
		if persistErr := s.repository.failure(ctx, "price", defaultProviderKey, priceObservationKey, state, code, now, now.Add(s.failureTTL)); persistErr != nil {
			return NativePrice{}, persistErr
		}
		return NativePrice{}, CapabilityError{Capability: "price", State: state, Code: code}
	}
	price, err := decodeNativePrice(result.Body, now, s.freshness)
	if err != nil {
		if persistErr := s.repository.failure(ctx, "price", defaultProviderKey, priceObservationKey, "failed", "invalid_response", now, now.Add(s.failureTTL)); persistErr != nil {
			return NativePrice{}, persistErr
		}
		return NativePrice{}, CapabilityError{Capability: "price", State: "failed", Code: "invalid_response"}
	}
	encoded, _ := json.Marshal(price)
	err = dbaccess.WithQueries(ctx, s.repository.db, func(queries *dbgen.Queries) error {
		return queries.RecordPriceAdapterSuccess(ctx, dbgen.RecordPriceAdapterSuccessParams{
			ChainID: s.repository.chainID, Value: encoded,
			ObservedAt: timestamptz(price.ObservedAt), ExpiresAt: timestamptz(price.ObservedAt.Add(s.freshness)),
		})
	})
	if err != nil {
		return NativePrice{}, err
	}
	return price, nil
}

func decodeNativePrice(raw []byte, now time.Time, freshness time.Duration) (NativePrice, error) {
	var value NativePrice
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || !canonicalFixedDecimal(value.USD) || !canonicalFixedDecimal(value.BTC) {
		return NativePrice{}, errors.New("invalid price response")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return NativePrice{}, errors.New("invalid trailing price response")
	}
	if value.ObservedAt.IsZero() || value.ObservedAt.After(now.Add(time.Minute)) || !value.ObservedAt.Add(freshness).After(now) {
		return NativePrice{}, errors.New("price response is outside the freshness window")
	}
	value.ObservedAt = value.ObservedAt.UTC()
	return value, nil
}

func strictHTTPSURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("adapter URL must be absolute HTTPS without credentials or fragments")
	}
	if len(parsed.String()) > 4096 {
		return "", errors.New("adapter URL exceeds 4096 bytes")
	}
	return parsed.String(), nil
}

func canonicalFixedDecimal(value string) bool {
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 78 || len(parts[0]) > 1 && parts[0][0] == '0' {
		return false
	}
	for _, character := range parts[0] {
		if character < '0' || character > '9' {
			return false
		}
	}
	if len(parts) == 1 {
		return true
	}
	if len(parts[1]) == 0 || len(parts[1]) > 18 || parts[1][len(parts[1])-1] == '0' {
		return false
	}
	for _, character := range parts[1] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func classifyFetchFailure(err error) (string, string) {
	var fetchError *metadata.FetchError
	if errors.As(err, &fetchError) {
		switch fetchError.Kind {
		case metadata.FailureUnsafeURL, metadata.FailureUnsafeContent:
			return string(fetchError.Kind), "unavailable"
		case metadata.FailureUnavailable:
			return string(fetchError.Kind), "unavailable"
		default:
			return string(fetchError.Kind), "failed"
		}
	}
	return "transport_failure", "failed"
}
