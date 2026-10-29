// Package ensread implements ENS reads from writer-owned publications. It has
// no network client, candidate scheduler or request-time snapshot writer.
package ensread

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adraffy/go-ens-normalize/ensip15"
	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Options struct {
	ChainID     uint64
	Policy      common.Hash
	MaxBatch    int
	SnapshotTTL time.Duration
	Sources     []enscontract.Namespace
}
type Reader struct {
	db      dbaccess.Database
	options Options
	chain   pgtype.Numeric
}

func New(db dbaccess.Database, o Options) (*Reader, error) {
	if db == nil || o.ChainID == 0 || o.Policy == (common.Hash{}) || o.MaxBatch < 1 || o.MaxBatch > 100 || o.SnapshotTTL < time.Minute || o.SnapshotTTL > 24*time.Hour {
		return nil, errors.New("invalid ENS reader options")
	}
	o.Sources = append([]enscontract.Namespace(nil), o.Sources...)
	return &Reader{db: db, options: o, chain: pgtype.Numeric{Int: new(big.Int).SetUint64(o.ChainID), Valid: true}}, nil
}

type snapshotToken struct {
	Format  int    `json:"f"`
	Chain   string `json:"c"`
	Policy  string `json:"p"`
	Version string `json:"v"`
	Expires int64  `json:"e"`
}

func (r *Reader) snapshot(ctx context.Context, q *dbgen.Queries, token string) (int64, string, error) {
	current, e := q.ReadENSEffectiveSnapshot(ctx, r.chain, r.options.Policy.Bytes())
	if e != nil {
		return 0, "", e
	}
	if token != "" {
		if len(token) > 512 {
			return 0, "", enscontract.ErrSnapshotInvalid
		}
		raw, e := base64.RawURLEncoding.DecodeString(token)
		if e != nil {
			return 0, "", enscontract.ErrSnapshotInvalid
		}
		var value snapshotToken
		if json.Unmarshal(raw, &value) != nil {
			return 0, "", enscontract.ErrSnapshotInvalid
		}
		version, e := strconv.ParseInt(value.Version, 10, 64)
		if e != nil || version < current.Minimum || version > current.Version || strconv.FormatInt(version, 10) != value.Version || value.Format != 1 || value.Chain != strconv.FormatUint(r.options.ChainID, 10) || value.Policy != r.options.Policy.Hex() || value.Expires <= time.Now().Unix() || value.Expires > time.Now().Add(r.options.SnapshotTTL).Unix()+1 {
			return 0, "", enscontract.ErrSnapshotInvalid
		}
		if version > 0 {
			valid, e := q.ValidateENSEffectiveSnapshotVersion(ctx, r.chain, r.options.Policy.Bytes(), version)
			if e != nil {
				return 0, "", e
			}
			if !valid {
				return 0, "", enscontract.ErrSnapshotInvalid
			}
		}
		return version, token, nil
	}
	value := snapshotToken{Format: 1, Chain: strconv.FormatUint(r.options.ChainID, 10), Policy: r.options.Policy.Hex(), Version: strconv.FormatInt(current.Version, 10), Expires: time.Now().Add(r.options.SnapshotTTL).Unix()}
	raw, e := json.Marshal(value)
	if e != nil {
		return 0, "", e
	}
	return current.Version, base64.RawURLEncoding.EncodeToString(raw), nil
}
func (r *Reader) read(ctx context.Context, q *dbgen.Queries, direction, key string, version int64) (enscontract.PrimaryResolution, error) {
	result := enscontract.PrimaryResolution{SnapshotVersion: version, Outcome: enscontract.Unavailable, Code: string(enscontract.NotIndexed)}
	row, e := q.ReadENSEffectiveName(ctx, dbgen.ReadENSEffectiveNameParams{TargetChainID: r.chain, PolicyDigest: r.options.Policy.Bytes(), Direction: direction, LookupKey: key, Snapshot: version})
	if errors.Is(e, pgx.ErrNoRows) {
		code, err := q.ReadENSPendingNameCode(ctx, dbgen.ReadENSPendingNameCodeParams{TargetChainID: r.chain, PolicyDigest: r.options.Policy.Bytes(), Direction: direction, LookupKey: key})
		if err == nil {
			result.Code = code
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
		return result, nil
	}
	if e != nil {
		return result, e
	}
	result.ObservationID = row.ID
	if !row.Current {
		result.Code = row.UnavailableCode
		return result, nil
	}
	result.ObservationID = row.ID
	result.Outcome = enscontract.State(row.State)
	result.Code = ""
	result.ValidUntil = row.ValidUntil.Time
	if row.Name != nil {
		result.Name = *row.Name
	}
	if row.Namespace != nil {
		result.Source = enscontract.Namespace(*row.Namespace)
	}
	result.Address = common.BytesToAddress(row.Address)
	return result, nil
}
func (r *Reader) ResolveAddressBatch(ctx context.Context, addresses []common.Address, token string) ([]enscontract.PrimaryResolution, string, error) {
	if len(addresses) < 1 || len(addresses) > r.options.MaxBatch {
		return nil, "", errors.New("invalid ENS address batch")
	}
	seen := map[common.Address]bool{}
	for _, address := range addresses {
		if seen[address] {
			return nil, "", errors.New("duplicate ENS address")
		}
		seen[address] = true
	}
	results := make([]enscontract.PrimaryResolution, len(addresses))
	next := ""
	e := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		version, encoded, e := r.snapshot(ctx, q, token)
		if e != nil {
			return e
		}
		next = encoded
		for i, address := range addresses {
			results[i], e = r.read(ctx, q, "primary", strings.ToLower(address.Hex()), version)
			if e != nil {
				return e
			}
			results[i].Address = address
			if token != "" && results[i].ObservationID > 0 && results[i].Outcome == enscontract.Unavailable {
				return enscontract.ErrSnapshotInvalid
			}
		}
		return nil
	})
	if e != nil {
		return nil, "", readError(e)
	}
	return results, next, nil
}
func (r *Reader) ResolveCurrentPrimary(ctx context.Context, address common.Address) (enscontract.PrimaryResolution, error) {
	result, _, e := r.ResolveAddressBatch(ctx, []common.Address{address}, "")
	if e != nil {
		return enscontract.PrimaryResolution{}, e
	}
	return result[0], nil
}
func (r *Reader) ResolveForward(ctx context.Context, input string) (enscontract.ForwardResolution, error) {
	name, e := normalize(input)
	if e != nil {
		return enscontract.ForwardResolution{}, e
	}
	var result enscontract.PrimaryResolution
	e = dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		version, _, e := r.snapshot(ctx, q, "")
		if e != nil {
			return e
		}
		result, e = r.read(ctx, q, "forward", name, version)
		return e
	})
	if e != nil {
		return enscontract.ForwardResolution{}, readError(e)
	}
	if result.Outcome == enscontract.Unavailable {
		return enscontract.ForwardResolution{}, &enscontract.UnavailableError{State: "unavailable", Code: result.Code}
	}
	return enscontract.ForwardResolution{ObservationID: result.ObservationID, Outcome: result.Outcome, Name: name, Address: result.Address, Source: result.Source, ValidUntil: result.ValidUntil}, nil
}
func normalize(input string) (string, error) {
	input = strings.TrimSpace(input)
	if len(input) == 0 || len(input) > 1024 || !utf8.ValidString(input) || strings.ContainsAny(input, "\x00\r\n\t /\\") {
		return "", enscontract.ErrInvalidName
	}
	name, e := ensip15.Shared().Normalize(input)
	if e != nil || len(name) > 255 {
		return "", enscontract.ErrInvalidName
	}
	wire := 1
	for label := range strings.SplitSeq(name, ".") {
		wire += len(label) + 1
		if len(label) == 0 || wire > 255 {
			return "", enscontract.ErrInvalidName
		}
	}
	return name, nil
}
func readError(e error) error {
	if errors.Is(e, enscontract.ErrSnapshotInvalid) {
		return enscontract.ErrSnapshotInvalid
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	return &enscontract.UnavailableError{State: "unavailable", Code: "storage_unavailable"}
}
