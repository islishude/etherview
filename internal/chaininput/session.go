// Package chaininput reads immutable, endpoint-pinned stage inputs from PostgreSQL.
package chaininput

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrPending = errors.New("sync_input_pending")
var ErrInvalid = errors.New("invalid_state_input")
var ErrUnavailable = errors.New("state_input_unavailable")

// exactStateUnavailable replays a selector capability failure without retaining provider text.
type exactStateUnavailable struct{}

func (exactStateUnavailable) Error() string  { return "exact_state_unavailable" }
func (exactStateUnavailable) ErrorCode() int { return -32602 }

type Reverted struct{ Data []byte }

func (e Reverted) ErrorData() any { return hexutil.Encode(e.Data) }

func (Reverted) Error() string  { return "execution reverted" }
func (Reverted) ErrorCode() int { return 3 }

type Request struct {
	Kind    string
	Address common.Address
	Slot    *common.Hash    `json:",omitempty"`
	Data    hexutil.Bytes   `json:",omitempty"`
	From    *common.Address `json:",omitempty"`
	Gas     hexutil.Uint64  `json:",omitempty"`
}

type Session struct {
	q     *dbgen.Queries
	group int64
	hash  common.Hash
	mu    sync.Mutex
	issue error
}

func Open(ctx context.Context, db dbaccess.Database, chain, height pgtype.Numeric, hash common.Hash, consumer string) (*Session, error) {
	q := dbgen.New(db)
	key := dbgen.StateInputLookupGroupParams{ChainID: chain, BlockNumber: height, BlockHash: hash[:], Consumer: consumer}
	group, err := q.StateInputLookupGroup(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		group, err = q.StateInputGroup(ctx, dbgen.StateInputGroupParams(key))
	}
	if err != nil {
		return nil, err
	}
	return &Session{q: q, group: group, hash: hash}, nil
}

// Err reports an incomplete input even when an optional probe swallowed its error.
// The caller checks it before committing any projection or checkpoint.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issue
}
func (s *Session) record(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.issue == nil || errors.Is(s.issue, ErrPending) {
		s.issue = err
	}
	return err
}

func (s *Session) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if len(args) < 2 {
		return s.record(ErrInvalid)
	}
	block, ok := args[len(args)-1].(rpc.BlockNumberOrHash)
	if !ok || block.BlockHash == nil || *block.BlockHash != s.hash || !block.RequireCanonical {
		return s.record(ErrInvalid)
	}
	var r Request
	switch method {
	case "eth_getCode", "eth_getStorageAt":
		address, ok := args[0].(common.Address)
		if !ok {
			return s.record(ErrInvalid)
		}
		r.Address = address
		r.Kind = "code"
		if method == "eth_getStorageAt" {
			if len(args) != 3 {
				return s.record(ErrInvalid)
			}
			slot, ok := args[1].(common.Hash)
			if !ok {
				return s.record(ErrInvalid)
			}
			r.Slot = &slot
			r.Kind = "storage"
		} else if len(args) != 2 {
			return s.record(ErrInvalid)
		}
	case "eth_call":
		object, ok := args[0].(map[string]any)
		if !ok || len(args) != 2 {
			return s.record(ErrInvalid)
		}
		raw, err := json.Marshal(object)
		if err != nil {
			return s.record(ErrInvalid)
		}
		var call struct {
			To   *common.Address
			Data hexutil.Bytes
			From *common.Address
			Gas  hexutil.Uint64
		}
		if json.Unmarshal(raw, &call) != nil || call.To == nil || len(call.Data) < 4 || len(call.Data) > 128*1024 || call.Gas > 30000000 {
			return s.record(ErrInvalid)
		}
		r = Request{Kind: "call", Address: *call.To, Data: call.Data, From: call.From, Gas: call.Gas}
	default:
		return s.record(ErrInvalid)
	}
	key, err := json.Marshal(r)
	if err != nil {
		return s.record(err)
	}
	row, err := s.q.StageInputRead(ctx, s.group, key)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = s.q.StageInputSubmit(ctx, s.group, key); err != nil {
			return s.record(err)
		}
		return s.record(ErrPending)
	}
	if err != nil {
		return s.record(err)
	}
	switch row.Outcome {
	case "":
		return s.record(ErrPending)
	case "exact_unavailable":
		return exactStateUnavailable{}
	case "reverted":
		return Reverted{Data: row.Result}
	case "ready":
		encoded, err := json.Marshal(hexutil.Bytes(row.Result))
		if err != nil {
			return s.record(err)
		}
		if err = json.Unmarshal(encoded, result); err != nil {
			return s.record(ErrInvalid)
		}
		return nil
	default:
		return ErrUnavailable
	}
}
func (s *Session) BatchCallContext(ctx context.Context, batch []rpc.BatchElem) error {
	for i := range batch {
		el := &batch[i]
		el.Error = s.CallContext(ctx, el.Result, el.Method, el.Args...)
	}
	return s.Err()
}

func (s *Session) Endpoint(ctx context.Context) (string, error) {
	return s.q.StateInputEndpoint(ctx, s.group)
}
