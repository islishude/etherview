package watchlist

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrPushLimit = errors.New("browser subscription limit")

type PushOptions struct {
	Enabled   bool
	PublicKey string
}

func validEndpoint(endpoint string) bool {
	if len(endpoint) == 0 || len(endpoint) > 2048 {
		return false
	}
	u, e := url.Parse(endpoint)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && (u.Port() == "" || u.Port() == "443")
}
func (s *Service) PushSubscriptions(ctx context.Context, user, session string) (gen.PushSubscriptions, error) {
	result := gen.PushSubscriptions{Enabled: s.push.Enabled, PublicKey: s.push.PublicKey, Items: []gen.BrowserPushSubscription{}}
	owner, e := uid(user)
	if e != nil {
		return result, e
	}
	sid, e := uid(session)
	if e != nil {
		return result, e
	}
	q := dbgen.New(s.db)
	if _, e = q.PushSession(ctx, sid, owner, s.numeric); e != nil {
		return result, classify(e)
	}
	rows, e := q.PushList(ctx, owner, sid)
	if e != nil {
		return result, e
	}
	for _, r := range rows {
		result.Items = append(result.Items, gen.BrowserPushSubscription{Id: uuidText(r.ID), EndpointHash: r.EndpointHash, Language: r.Language, ExpiresAt: r.ExpiresAt.Time})
	}
	return result, nil
}
func (s *Service) SavePushSubscription(ctx context.Context, user, session string, input gen.PushSubscriptionInput) (gen.BrowserPushSubscription, error) {
	var result gen.BrowserPushSubscription
	if !s.push.Enabled {
		return result, ErrUnavailable
	}
	public, e := base64.RawURLEncoding.DecodeString(input.P256dh)
	if e != nil {
		return result, ErrInvalid
	}
	if _, e = ecdh.P256().NewPublicKey(public); e != nil {
		return result, ErrInvalid
	}
	auth, e := base64.RawURLEncoding.DecodeString(input.Auth)
	if e != nil || len(auth) != 16 || !validEndpoint(input.Endpoint) || (input.Language != "en" && input.Language != "zh") {
		return result, ErrInvalid
	}
	owner, e := uid(user)
	if e != nil {
		return result, e
	}
	sid, e := uid(session)
	if e != nil {
		return result, e
	}
	digest := sha256.Sum256([]byte(input.Endpoint))
	e = dbaccess.WithTransaction(ctx, s.db, func(q *dbgen.Queries) error {
		if e := q.StoreLegacyLockChainStatement1(ctx, new(s.chain)); e != nil {
			return e
		}
		if e := q.PushLockSubscriptions(ctx); e != nil {
			return e
		}
		expiry, e := q.PushSession(ctx, sid, owner, s.numeric)
		if e != nil {
			return e
		}
		count, e := q.PushCount(ctx, owner, input.Endpoint)
		if e != nil {
			return e
		}
		if count >= 10 {
			return ErrPushLimit
		}
		tip, e := q.WatchTip(ctx, s.numeric)
		if e != nil {
			return e
		}
		row, e := q.PushSave(ctx, dbgen.PushSaveParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, UserID: owner, SessionID: sid, Endpoint: input.Endpoint, EndpointHash: hex.EncodeToString(digest[:]), P256dh: input.P256dh, Auth: input.Auth, Language: string(input.Language), StartNumber: number(tip.BlockNumber), StartHash: tip.BlockHash})
		if e != nil {
			return e
		}
		result = gen.BrowserPushSubscription{Id: uuidText(row.ID), EndpointHash: row.EndpointHash, Language: row.Language, ExpiresAt: expiry.Time}
		return nil
	})
	return result, classify(e)
}
func (s *Service) DeletePushSubscription(ctx context.Context, user, session, id string) error {
	owner, e := uid(user)
	if e != nil {
		return e
	}
	sid, e := uid(session)
	if e != nil {
		return e
	}
	identifier, e := uid(id)
	if e != nil {
		return e
	}
	count, e := dbgen.New(s.db).PushDelete(ctx, identifier, owner, sid)
	if e == nil && count == 0 {
		return ErrNotFound
	}
	return e
}
