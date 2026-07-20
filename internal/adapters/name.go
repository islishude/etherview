package adapters

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

type NameOptions struct {
	BaseURL    string
	Freshness  time.Duration
	FailureTTL time.Duration
	Now        func() time.Time
}

type nameObservation struct {
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Registry    string    `json:"registry"`
	Resolver    string    `json:"resolver,omitempty"`
	BlockNumber string    `json:"block_number"`
	BlockHash   string    `json:"block_hash"`
	ObservedAt  time.Time `json:"observed_at"`
}

type NameService struct {
	repository  repository
	fetcher     JSONFetcher
	baseURL     string
	providerKey string
	freshness   time.Duration
	failureTTL  time.Duration
	now         func() time.Time
}

func NewPostgresNameService(db *sql.DB, chainID uint64, fetcher JSONFetcher, options NameOptions) (*NameService, error) {
	repository, err := newRepository(db, chainID)
	if err != nil {
		return nil, err
	}
	if fetcher == nil {
		return nil, errors.New("name adapter fetcher is nil")
	}
	baseURL, err := strictHTTPSURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.Freshness <= 0 || options.Freshness > 30*24*time.Hour {
		return nil, errors.New("name freshness must be between 1ns and 720h")
	}
	if options.FailureTTL <= 0 || options.FailureTTL > time.Hour {
		return nil, errors.New("name failure TTL must be between 1ns and 1h")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &NameService{
		repository: repository, fetcher: fetcher, baseURL: baseURL, providerKey: nameProviderKey(baseURL),
		freshness: options.Freshness, failureTTL: options.FailureTTL, now: options.Now,
	}, nil
}

func (s *NameService) Resolve(ctx context.Context, rawName string) (string, error) {
	if s == nil {
		return "", errors.New("name adapter is nil")
	}
	name, err := normalizeName(rawName)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	if cached, exists, err := s.repository.fresh(ctx, "name", s.providerKey, name, now); err != nil {
		return "", err
	} else if exists {
		if cached.State != "complete" {
			return "", CapabilityError{Capability: "name", State: cached.State, Code: cached.Code}
		}
		observation, err := decodeNameObservation(cached.Value, name, now, s.freshness)
		if err != nil {
			return "", errors.New("stored name adapter observation is invalid")
		}
		return observation.Address, nil
	}
	endpoint, _ := url.Parse(s.baseURL)
	query := endpoint.Query()
	query.Set("chain_id", strconv.FormatUint(s.repository.chain, 10))
	query.Set("q", name)
	endpoint.RawQuery = query.Encode()
	result, err := s.fetcher.Fetch(ctx, endpoint.String(), metadata.KindJSON)
	if err != nil {
		code, state := classifyFetchFailure(err)
		if persistErr := s.repository.failure(ctx, "name", s.providerKey, name, state, code, now, now.Add(s.failureTTL)); persistErr != nil {
			return "", persistErr
		}
		return "", CapabilityError{Capability: "name", State: state, Code: code}
	}
	observation, err := decodeNameObservation(result.Body, name, now, s.freshness)
	if err != nil {
		if persistErr := s.repository.failure(ctx, "name", s.providerKey, name, "failed", "invalid_response", now, now.Add(s.failureTTL)); persistErr != nil {
			return "", persistErr
		}
		return "", CapabilityError{Capability: "name", State: "failed", Code: "invalid_response"}
	}
	address, _ := decodeHex(observation.Address, 20)
	registry, _ := decodeHex(observation.Registry, 20)
	blockHash, _ := decodeHex(observation.BlockHash, 32)
	var resolver []byte
	if observation.Resolver != "" {
		resolver, _ = decodeHex(observation.Resolver, 20)
	}
	blockNumber, _ := decimalNumeric(observation.BlockNumber)
	encoded, _ := json.Marshal(observation)
	var stored dbgen.RecordNameAdapterSuccessRow
	err = dbaccess.WithQueries(ctx, s.repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		stored, queryErr = queries.RecordNameAdapterSuccess(ctx, dbgen.RecordNameAdapterSuccessParams{
			ChainID: s.repository.chainID, ObservedBlockNumber: blockNumber,
			ObservedBlockHash: blockHash, Registry: registry, Name: name, Address: address,
			Resolver: resolver, ObservedAt: timestamptz(observation.ObservedAt), ProviderKey: s.providerKey, Value: encoded,
			ExpiresAt: timestamptz(observation.ObservedAt.Add(s.freshness)),
		})
		return queryErr
	})
	if err != nil {
		return "", err
	}
	switch stored.State {
	case "stored":
		if !strings.EqualFold("0x"+hex.EncodeToString(stored.Address), observation.Address) {
			return "", errors.New("stored name adapter identity is inconsistent")
		}
	case "stale_block", "identity_conflict":
		return "", CapabilityError{Capability: "name", State: "failed", Code: stored.State}
	default:
		return "", errors.New("stored name adapter state is invalid")
	}
	return observation.Address, nil
}

func nameProviderKey(baseURL string) string {
	digest := sha256.Sum256([]byte(baseURL))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func decodeNameObservation(raw []byte, expectedName string, now time.Time, freshness time.Duration) (nameObservation, error) {
	var value nameObservation
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nameObservation{}, errors.New("invalid name response")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nameObservation{}, errors.New("invalid trailing name response")
	}
	name, err := normalizeName(value.Name)
	if err != nil || name != expectedName {
		return nameObservation{}, errors.New("name response identity mismatch")
	}
	address, err := ethrpc.ParseAddress(value.Address)
	if err != nil {
		return nameObservation{}, errors.New("invalid name response address")
	}
	if _, err := ethrpc.ParseAddress(value.Registry); err != nil {
		return nameObservation{}, errors.New("invalid name response registry")
	}
	if value.Resolver != "" {
		if _, err := ethrpc.ParseAddress(value.Resolver); err != nil {
			return nameObservation{}, errors.New("invalid name response resolver")
		}
	}
	if _, err := decimalNumeric(value.BlockNumber); err != nil {
		return nameObservation{}, errors.New("invalid name response block number")
	}
	if _, err := ethrpc.ParseHash(value.BlockHash); err != nil {
		return nameObservation{}, errors.New("invalid name response block hash")
	}
	if value.ObservedAt.IsZero() || value.ObservedAt.After(now.Add(time.Minute)) || !value.ObservedAt.Add(freshness).After(now) {
		return nameObservation{}, errors.New("name response is outside the freshness window")
	}
	value.Name, value.Address, value.ObservedAt = name, strings.ToLower(address.String()), value.ObservedAt.UTC()
	value.Registry = strings.ToLower(value.Registry)
	value.Resolver = strings.ToLower(value.Resolver)
	value.BlockHash = strings.ToLower(value.BlockHash)
	return value, nil
}

func normalizeName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 255 || !strings.Contains(value, ".") || strings.ContainsAny(value, "\x00\r\n\t /\\") {
		return "", errors.New("invalid external name")
	}
	return value, nil
}

func decodeHex(value string, size int) ([]byte, error) {
	if len(value) != 2+size*2 || !strings.HasPrefix(value, "0x") {
		return nil, errors.New("invalid fixed hex value")
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, errors.New("invalid fixed hex value")
	}
	return decoded, nil
}
