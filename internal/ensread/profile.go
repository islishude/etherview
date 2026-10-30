package ensread

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReadDetail composes immutable profile and resolution publications inside the
// caller's read-only snapshot. Explicit IDs never silently select a newer row.
func ReadDetail(ctx context.Context, q *dbgen.Queries, chain string, policy common.Hash, name string, profileID, forwardID *int64) (enscontract.NameDetail, error) {
	result := enscontract.NameDetail{Name: name, State: "not_indexed", RegistrationProfile: enscontract.PendingProfile(), ResolvedAddress: enscontract.Field{State: "pending"}}
	var numeric pgtype.Numeric
	if e := numeric.Scan(chain); e != nil {
		return result, e
	}
	known, e := q.ReadENSKnownName(ctx, name, numeric, policy.Bytes())
	if errors.Is(e, pgx.ErrNoRows) {
		return result, nil
	}
	if e != nil {
		return result, e
	}
	if !known {
		return result, nil
	}
	result.State = "indexed"
	var p, f int64
	if profileID != nil {
		p = *profileID
	}
	if forwardID != nil {
		f = *forwardID
	}
	rows, e := q.ReadENSProfilePublications(ctx, dbgen.ReadENSProfilePublicationsParams{ChainID: numeric, PolicyDigest: policy.Bytes(), Name: name, ProfileID: p, ForwardID: f})
	if e != nil {
		return result, e
	}
	for _, row := range rows {
		number := numberText(row.Number)
		if number == nil {
			return result, errors.New("invalid name observation")
		}
		observation := &enscontract.Observation{Publication: strconv.FormatInt(row.ID, 10), BlockNumber: *number, BlockHash: common.BytesToHash(row.BlockHash).Hex(), ValidUntil: row.ValidUntil.Time.UTC().Format(time.RFC3339Nano)}
		if row.Direction == "profile" {
			if e = json.Unmarshal(row.ProfileData, &result.RegistrationProfile); e != nil {
				return result, e
			}
			result.ProfileObservation = observation
			// Crossing registration expiry cannot leave current ownership on screen
			// while waiting for the next half-window refresh.
			if expiry, e := strconv.ParseUint(result.ExpirationTime.Value, 10, 64); e == nil && expiry > 0 && expiry <= uint64(time.Now().Unix()) && result.RegistrationState == "registered" {
				result.RegistrationState = "expired"
				result.Registrant = enscontract.Field{State: "pending"}
				result.Controller = enscontract.Field{State: "pending"}
			}
		} else {
			result.ResolutionObservation = observation
			if row.State == "not_found" {
				result.ResolvedAddress = enscontract.Field{State: "not_set"}
			} else if len(row.Address) == 20 {
				result.ResolvedAddress = enscontract.Field{State: "value", Value: common.BytesToAddress(row.Address).Hex()}
			}
		}
	}
	return result, nil
}
func (r *Reader) NameDetail(ctx context.Context, name string) (enscontract.NameDetail, error) {
	normalized, e := normalize(name)
	if e != nil {
		return enscontract.NameDetail{}, e
	}
	var result enscontract.NameDetail
	e = dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		ids, err := q.ReadENSLatestProfileIDs(ctx, r.chain, r.options.Policy.Bytes(), normalized)
		if err != nil {
			return err
		}
		result, err = ReadDetail(ctx, q, strconv.FormatUint(r.options.ChainID, 10), r.options.Policy, normalized, &ids.ProfileID, &ids.ForwardID)
		return err
	})
	return result, e
}
