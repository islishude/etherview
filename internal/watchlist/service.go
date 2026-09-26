// Package watchlist owns writer-authoritative private watches and notifications.
package watchlist

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrInvalid     = errors.New("invalid watchlist request")
	ErrNotFound    = errors.New("watchlist resource not found")
	ErrLimit       = errors.New("watchlist resource limit")
	ErrDuplicate   = errors.New("address already watched")
	ErrUnavailable = errors.New("activity data unavailable")
	ErrRateLimit   = errors.New("export admission limit")
	ErrExportLimit = errors.New("export exceeds resource limit")
)

type Service struct {
	db      dbaccess.Database
	chain   string
	numeric pgtype.Numeric
}

func New(db dbaccess.Database, chainID uint64) (*Service, error) {
	if db == nil || chainID == 0 {
		return nil, ErrInvalid
	}
	return &Service{db: db, chain: strconv.FormatUint(chainID, 10), numeric: pgtype.Numeric{Int: new(big.Int).SetUint64(chainID), Valid: true}}, nil
}
func uid(s string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, ErrInvalid
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
func number(s string) pgtype.Numeric { var n pgtype.Numeric; _ = n.Scan(s); return n }
func uuidText(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
func classify(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return ErrDuplicate
	}
	return err
}
func validateInput(input gen.WatchInput) (common.Address, []string, error) {
	address, err := ethrpc.ParseAddress(input.Address)
	if err != nil || !utf8.ValidString(input.Label) || utf8.RuneCountInString(input.Label) > 64 || strings.ContainsFunc(input.Label, unicode.IsControl) || !validDirection(string(input.Direction)) || len(input.Kinds) < 1 || len(input.Kinds) > 4 {
		return common.Address{}, nil, ErrInvalid
	}
	kinds := make([]string, 0, len(input.Kinds))
	seen := map[string]bool{}
	for _, kind := range input.Kinds {
		k := string(kind)
		if (k != "transaction" && k != "erc20" && k != "erc721" && k != "erc1155") || seen[k] {
			return common.Address{}, nil, ErrInvalid
		}
		seen[k] = true
		kinds = append(kinds, k)
	}
	return address, kinds, nil
}
func validDirection(s string) bool { return s == "in" || s == "out" || s == "both" }
func watchModel(row dbgen.AddressWatch) (gen.AddressWatch, error) {
	n, err := dbaccess.NumericText(row.StartNumber)
	if err != nil || !n.Valid || len(row.Address) != 20 || len(row.StartHash) != 32 {
		return gen.AddressWatch{}, ErrUnavailable
	}
	kinds := make([]gen.AddressWatchKinds, len(row.Kinds))
	for i, k := range row.Kinds {
		kinds[i] = gen.AddressWatchKinds(k)
	}
	return gen.AddressWatch{Id: uuidText(row.ID), Address: common.BytesToAddress(row.Address).Hex(), Label: row.Label, Kinds: kinds, Direction: gen.AddressWatchDirection(row.Direction), Enabled: row.Enabled, StartNumber: n.String, StartHash: "0x" + hex.EncodeToString(row.StartHash)}, nil
}
func (s *Service) List(ctx context.Context, user string) ([]gen.AddressWatch, error) {
	id, err := uid(user)
	if err != nil {
		return nil, err
	}
	rows, err := dbgen.New(s.db).WatchList(ctx, id, s.numeric)
	if err != nil {
		return nil, err
	}
	out := make([]gen.AddressWatch, 0, len(rows))
	for _, row := range rows {
		m, e := watchModel(row)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, nil
}
func (s *Service) Save(ctx context.Context, user, id string, input gen.WatchInput) (gen.AddressWatch, error) {
	owner, err := uid(user)
	if err != nil {
		return gen.AddressWatch{}, err
	}
	address, kinds, err := validateInput(input)
	if err != nil {
		return gen.AddressWatch{}, err
	}
	identifier := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if id != "" {
		identifier, err = uid(id)
		if err != nil {
			return gen.AddressWatch{}, err
		}
	}
	var row dbgen.AddressWatch
	err = dbaccess.WithTransaction(ctx, s.db, func(q *dbgen.Queries) error {
		if e := q.StoreLegacyLockChainStatement1(ctx, new(s.chain)); e != nil {
			return e
		}
		if _, e := q.WatchLockOwner(ctx, owner, s.numeric); e != nil {
			return e
		}
		tip, e := q.WatchTip(ctx, s.numeric)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if e != nil {
			return e
		}
		if id == "" {
			count, e := q.WatchCount(ctx, owner)
			if e != nil {
				return e
			}
			if count >= 100 {
				return ErrLimit
			}
			row, e = q.WatchCreate(ctx, dbgen.WatchCreateParams{ID: identifier, UserID: owner, ChainID: s.numeric, Address: address.Bytes(), Label: input.Label, Kinds: kinds, Direction: string(input.Direction), Enabled: input.Enabled, StartNumber: number(tip.BlockNumber), StartHash: tip.BlockHash})
			return e
		}
		// Address identity is immutable; editing preferences cannot retarget history.
		existing, e := q.WatchList(ctx, owner, s.numeric)
		if e != nil {
			return e
		}
		found := false
		for _, w := range existing {
			if w.ID == identifier {
				found = common.BytesToAddress(w.Address) == address
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		row, e = q.WatchUpdate(ctx, dbgen.WatchUpdateParams{ID: identifier, UserID: owner, Label: input.Label, Kinds: kinds, Direction: string(input.Direction), Enabled: input.Enabled, StartNumber: number(tip.BlockNumber), StartHash: tip.BlockHash})
		return e
	})
	if err != nil {
		return gen.AddressWatch{}, classify(err)
	}
	return watchModel(row)
}
func (s *Service) Delete(ctx context.Context, user, id string) error {
	owner, err := uid(user)
	if err != nil {
		return err
	}
	identifier, err := uid(id)
	if err != nil {
		return err
	}
	return classify(dbaccess.WithTransaction(ctx, s.db, func(q *dbgen.Queries) error {
		if e := q.StoreLegacyLockChainStatement1(ctx, new(s.chain)); e != nil {
			return e
		}
		count, e := q.WatchDelete(ctx, identifier, owner)
		if e == nil && count == 0 {
			return ErrNotFound
		}
		return e
	}))
}

type notificationCursor struct {
	User   string `json:"u"`
	Before int64  `json:"b"`
	Unread bool   `json:"r"`
}

func (s *Service) Notifications(ctx context.Context, user, cursor string, unread bool) (gen.WatchNotificationPage, error) {
	owner, err := uid(user)
	if err != nil {
		return gen.WatchNotificationPage{}, err
	}
	after := notificationCursor{User: user, Unread: unread}
	if cursor != "" {
		if publicquery.DecodeCursor(cursor, &after) != nil || after.User != user || after.Before <= 0 || after.Unread != unread {
			return gen.WatchNotificationPage{}, ErrInvalid
		}
	}
	page := gen.WatchNotificationPage{Items: []gen.WatchNotification{}}
	err = dbaccess.WithTransactionOptions(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		summary, e := q.WatchNotificationSummary(ctx, owner)
		if e != nil {
			return e
		}
		page.UnreadCount = summary.UnreadCount
		page.Watermark = summary.Watermark
		rows, e := q.WatchNotificationList(ctx, dbgen.WatchNotificationListParams{UserID: owner, BeforeID: after.Before, UnreadOnly: unread, PageLimit: 51})
		if e != nil {
			return e
		}
		if len(rows) > 50 {
			rows = rows[:50]
			page.NextCursor, e = publicquery.EncodeCursor(notificationCursor{User: user, Before: rows[49].ID, Unread: unread})
			if e != nil {
				return e
			}
		}
		for _, row := range rows {
			address := common.BytesToAddress(row.Address).Hex()
			activity, e := decodeActivity(row.Activity, address)
			if e != nil {
				return e
			}
			page.Items = append(page.Items, gen.WatchNotification{Id: strconv.FormatInt(row.ID, 10), WatchId: uuidText(row.WatchID), Address: address, Label: row.Label, Canonical: row.Canonical, Published: row.Published != nil && *row.Published, Read: row.ReadAt.Valid, Activity: activity})
		}
		return nil
	})
	return page, err
}
func (s *Service) Read(ctx context.Context, user, id string, through bool) error {
	owner, err := uid(user)
	if err != nil {
		return err
	}
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value < 0 || strconv.FormatInt(value, 10) != id {
		return ErrInvalid
	}
	q := dbgen.New(s.db)
	if through {
		return q.WatchReadThrough(ctx, owner, value)
	}
	count, err := q.WatchReadNotification(ctx, owner, value)
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}
