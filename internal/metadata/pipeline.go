package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	JobKind               = "metadata"
	NFTStage              = "nft-metadata"
	NFTStageVersion       = uint32(1)
	DefaultMaxAttempts    = uint32(5)
	MaximumMaxAttempts    = uint32(100)
	MaxDocumentBytes      = int64(2 << 20)
	MaxSourceURIBytes     = 4096
	MaxStoredErrorBytes   = 1024
	maxDocumentDepth      = 32
	maxDocumentNodes      = 10_000
	maxDocumentStringSize = 256 << 10
)

type State string

const (
	StatePending     State = "pending"
	StateAvailable   State = "available"
	StateUnavailable State = "unavailable"
	StateUnsafe      State = "unsafe"
	StateError       State = "error"
)

// NFTRequest is an immutable source observation for one canonical NFT. A
// later block or URI creates a new durable job while retaining prior attempts.
type NFTRequest struct {
	ChainID     string
	Token       ethrpc.Address
	TokenID     string
	BlockNumber uint64
	BlockHash   ethrpc.Hash
	SourceURI   string
	Priority    int32
	MaxAttempts uint32
}

func (request NFTRequest) Validate() error {
	if err := validateDecimal(request.ChainID, 78, "chain ID"); err != nil {
		return err
	}
	if _, err := ethrpc.ParseAddress(request.Token.String()); err != nil {
		return fmt.Errorf("metadata token address: %w", err)
	}
	if err := validateDecimal(request.TokenID, 78, "token ID"); err != nil {
		return err
	}
	if _, err := ethrpc.ParseHash(request.BlockHash.String()); err != nil {
		return fmt.Errorf("metadata observed block hash: %w", err)
	}
	if len(request.SourceURI) == 0 || len(request.SourceURI) > MaxSourceURIBytes {
		return fmt.Errorf("metadata source URI must contain between 1 and %d bytes", MaxSourceURIBytes)
	}
	for _, character := range request.SourceURI {
		if character < 0x20 || character == 0x7f {
			return errors.New("metadata source URI contains a control character")
		}
	}
	parsed, err := url.Parse(request.SourceURI)
	if err != nil {
		return errors.New("metadata source URI is malformed")
	}
	if parsed.User != nil {
		return errors.New("metadata source URI cannot contain credentials")
	}
	if request.MaxAttempts > MaximumMaxAttempts {
		return fmt.Errorf("metadata max attempts exceeds %d", MaximumMaxAttempts)
	}
	return nil
}

func (request NFTRequest) resourceKey() string {
	return strings.ToLower(request.Token.String()) + ":" + request.TokenID
}

func (request NFTRequest) idempotencyKey() (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		request.ChainID,
		request.resourceKey(),
		strconv.FormatUint(request.BlockNumber, 10),
		strings.ToLower(request.BlockHash.String()),
		request.SourceURI,
	}, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

type EnqueueResult struct {
	JobID   int64
	Created bool
}

type Lease struct {
	JobID       int64
	Token       string
	Request     NFTRequest
	Attempt     uint32
	MaxAttempts uint32
}

func (lease Lease) Validate() error {
	if lease.JobID <= 0 {
		return errors.New("metadata lease job ID must be positive")
	}
	if strings.TrimSpace(lease.Token) == "" {
		return errors.New("metadata lease token is empty")
	}
	if err := lease.Request.Validate(); err != nil {
		return err
	}
	if lease.Attempt == 0 || lease.MaxAttempts == 0 || lease.Attempt > lease.MaxAttempts {
		return errors.New("metadata lease attempt counters are invalid")
	}
	return nil
}

type Current struct {
	Resource  bool
	Canonical bool
}

type Outcome struct {
	State       State
	Code        string
	Message     string
	ResolvedURI string
	MediaType   string
	Document    json.RawMessage
	ContentHash [32]byte
	ContentSize int64
}

func (outcome Outcome) validate() error {
	switch outcome.State {
	case StateAvailable:
		if outcome.Code != "" || outcome.Message != "" {
			return errors.New("available metadata outcome contains an error")
		}
		if len(outcome.Document) == 0 || int64(len(outcome.Document)) != outcome.ContentSize || outcome.ContentSize > MaxDocumentBytes {
			return errors.New("available metadata outcome has inconsistent content size")
		}
		if outcome.ResolvedURI == "" || len(outcome.ResolvedURI) > MaxSourceURIBytes || outcome.MediaType == "" {
			return errors.New("available metadata outcome is missing bounded source information")
		}
		digest := sha256.Sum256(outcome.Document)
		if digest != outcome.ContentHash {
			return errors.New("available metadata outcome content hash differs")
		}
	case StateUnavailable, StateUnsafe, StateError:
		if err := validateErrorCode(outcome.Code); err != nil {
			return err
		}
		if strings.TrimSpace(outcome.Message) == "" || len(outcome.Message) > MaxStoredErrorBytes {
			return fmt.Errorf("metadata terminal error must contain between 1 and %d bytes", MaxStoredErrorBytes)
		}
		if len(outcome.Document) != 0 || outcome.ContentSize != 0 {
			return errors.New("metadata terminal error contains a document")
		}
	default:
		return fmt.Errorf("invalid terminal metadata state %q", outcome.State)
	}
	return nil
}

func validateErrorCode(code string) error {
	if len(code) == 0 || len(code) > 64 {
		return errors.New("metadata error code must contain between 1 and 64 bytes")
	}
	for _, character := range code {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return fmt.Errorf("metadata error code %q contains an unsupported character", code)
		}
	}
	return nil
}

func validateDecimal(value string, digits int, name string) error {
	if value == "" || len(value) > digits {
		return fmt.Errorf("metadata %s must be a canonical non-negative decimal with at most %d digits", name, digits)
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 || integer.String() != value {
		return fmt.Errorf("metadata %s must be a canonical non-negative decimal", name)
	}
	return nil
}

type Repository interface {
	Claim(context.Context, string, time.Duration) (Lease, bool, error)
	Renew(context.Context, Lease, time.Duration) error
	Current(context.Context, Lease) (Current, error)
	Finish(context.Context, Lease, Outcome) error
	Retry(context.Context, Lease, string, string, time.Duration) error
}

type Fetcher interface {
	Fetch(context.Context, string, Kind) (Result, error)
}
