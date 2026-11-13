package ethrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/bal"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

const BALRawMethod = "eth_getBlockAccessListRaw"
const BALJSONMethod = "eth_getBlockAccessList"

var ErrBALNotApplicable = errors.New("bal_not_applicable")
var ErrBALUnavailable = errors.New("bal_unavailable")
var ErrBALInvalid = errors.New("bal_invalid")

// BALFetcher probes only an exact, authenticated, BAL-bearing block. Unsupported
// methods are cached independently of normal state health, and periodically retried.
type BALFetcher struct {
	Pool        *Pool
	mu          sync.Mutex
	unsupported map[string]time.Time
}

type BALResult struct {
	Raw    json.RawMessage
	Method string
	List   bal.BlockAccessList
}

func (f *BALFetcher) Fetch(ctx context.Context, block *types.Block) (BALResult, error) {
	if block == nil {
		return BALResult{}, ErrBALInvalid
	}
	// This check precedes pool access and every capability request.
	if block.Header().BlockAccessListHash == nil {
		return BALResult{}, ErrBALNotApplicable
	}
	if block.Header().SlotNumber == nil {
		return BALResult{}, ErrBALInvalid
	}
	if f.Pool == nil {
		return BALResult{}, ErrBALUnavailable
	}
	// Honor pool health before trying alternatives. Each endpoint must supply
	// one complete body for the same hash; partial results are never combined.
	preferred, err := f.Pool.Acquire(PurposeState)
	if err != nil {
		return BALResult{}, ErrBALUnavailable
	}
	names := f.Pool.Names(PurposeState)
	for i, name := range names {
		if name == preferred.Name {
			names[0], names[i] = names[i], names[0]
			break
		}
	}
	transportFailed := false
	for _, name := range names {
		if ctx.Err() != nil {
			return BALResult{}, ErrTransport
		}
		endpoint, err := f.Pool.AcquireNamed(PurposeState, name)
		if err != nil {
			continue
		}
		for _, method := range []string{BALRawMethod, BALJSONMethod} {
			key := name + ":" + method
			f.mu.Lock()
			disabled := time.Now().Before(f.unsupported[key])
			f.mu.Unlock()
			if disabled {
				continue
			}
			var raw json.RawMessage
			err = endpoint.CallContext(ctx, &raw, method, block.Hash())
			if IsMethodNotFound(err) {
				f.mu.Lock()
				if f.unsupported == nil {
					f.unsupported = make(map[string]time.Time)
				}
				f.unsupported[key] = time.Now().Add(time.Minute)
				f.mu.Unlock()
				continue
			}
			if err != nil {
				f.Pool.ReportFailure(name)
				transportFailed = true
				break
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				break
			}
			result, err := DecodeBAL(raw, method, block)
			if err != nil {
				return BALResult{}, err
			}
			f.Pool.ReportSuccess(name)
			return result, nil
		}
	}
	if transportFailed {
		return BALResult{}, ErrTransport
	}
	return BALResult{}, ErrBALUnavailable
}

// DecodeBAL is also used for retained bodies during replay. A matching hash
// authenticates the entire BAL, not only its balance projection.
func DecodeBAL(raw json.RawMessage, method string, block *types.Block) (BALResult, error) {
	if block == nil {
		return BALResult{}, ErrBALInvalid
	}
	header := block.Header()
	if header.BlockAccessListHash == nil {
		return BALResult{}, ErrBALNotApplicable
	}
	if header.SlotNumber == nil || len(raw) > int(defaultMaxResponseBytes) {
		return BALResult{}, ErrBALInvalid
	}
	var encoded []byte
	switch method {
	case BALRawMethod:
		var value hexutil.Bytes
		if json.Unmarshal(raw, &value) != nil {
			return BALResult{}, ErrBALInvalid
		}
		encoded = value
	case BALJSONMethod:
		var err error
		encoded, err = encodeBALJSON(raw)
		if err != nil {
			return BALResult{}, ErrBALInvalid
		}
	default:
		return BALResult{}, ErrBALInvalid
	}
	if crypto.Keccak256Hash(encoded) != *header.BlockAccessListHash {
		return BALResult{}, ErrBALInvalid
	}
	var list bal.BlockAccessList
	if rlp.DecodeBytes(encoded, &list) != nil || list.Validate(header.GasLimit, len(block.Transactions())) != nil {
		return BALResult{}, ErrBALInvalid
	}
	return BALResult{Raw: bytes.Clone(raw), Method: method, List: list}, nil
}
