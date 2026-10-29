package ensread

import (
	"context"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"time"
)

func (r *Reader) NameStatus(ctx context.Context) ([]enscontract.SourceStatus, error) {
	result := make([]enscontract.SourceStatus, 0, 2)
	e := dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		rows, e := q.ReadENSNameSourceStatus(ctx, r.chain, r.options.Policy.Bytes())
		if e != nil {
			return e
		}
		for _, row := range rows {
			item := enscontract.SourceStatus{Source: enscontract.Namespace(row.Namespace), Protocols: row.Protocols, Target: numberText(row.TargetNumber), Scanned: numberText(row.ScannedNumber), Published: numberText(row.Published), Pending: strconv.FormatInt(row.Pending, 10), LastSuccess: row.LastSuccess.Time, ValidUntil: row.HealthyUntil.Time, State: "current"}
			if row.FailureCode != nil {
				item.Code = *row.FailureCode
			}
			switch {
			case row.State == "halted" || row.State == "unsupported":
				item.State = "unsupported_source"
			case row.State != "active" || item.Target == nil || item.Scanned == nil || *item.Target != *item.Scanned:
				item.State = "sync_pending"
			case !row.HealthyUntil.Valid || !time.Now().Before(row.HealthyUntil.Time):
				item.State = "stale"
			case row.Pending > 0:
				item.State = "resolution_pending"
			}
			result = append(result, item)
		}
		return nil
	})
	if e != nil {
		return nil, readError(e)
	}
	for _, source := range r.options.Sources {
		found := false
		for _, row := range result {
			found = found || row.Source == source
		}
		if !found {
			result = append(result, enscontract.SourceStatus{Source: source, State: "not_indexed", Protocols: []string{}, Pending: "0"})
		}
	}
	return result, nil
}
func numberText(n pgtype.Numeric) *string {
	value, e := dbaccess.NumericText(n)
	if e != nil || !value.Valid {
		return nil
	}
	return &value.String
}
