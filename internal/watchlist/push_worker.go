package watchlist

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PushWorker struct {
	db      *pgxpool.Pool
	service *Service
	sender  PushSender
	observe func(string)
}

func NewPushWorker(db *pgxpool.Pool, chain uint64, sender PushSender, observe func(string)) (*PushWorker, error) {
	if db == nil || sender == nil {
		return nil, ErrInvalid
	}
	s, e := New(db, chain)
	if e != nil {
		return nil, e
	}
	if observe == nil {
		observe = func(string) {}
	}
	return &PushWorker{db: db, service: s, sender: sender, observe: observe}, nil
}
func (*PushWorker) Name() string { return "browser-push" }
func (w *PushWorker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if e := w.lead(ctx); e != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "browser push temporarily unavailable", "error_code", "browser_push_unavailable")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
func (w *PushWorker) lead(parent context.Context) error {
	conn, e := w.db.Acquire(parent)
	if e != nil {
		return e
	}
	locked, e := dbgen.New(conn).PushLeaderLock(parent, w.service.chain)
	if e != nil {
		dbaccess.Discard(context.WithoutCancel(parent), conn)
		return e
	}
	if !locked {
		conn.Release()
		return nil
	}
	// Always close a lock-owning physical session; uncertain unlocks cannot leak.
	defer dbaccess.Discard(context.WithoutCancel(parent), conn)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	q := dbgen.New(w.db)
	boundary, e := q.PushBoundary(ctx, w.service.numeric)
	if e != nil {
		return e
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(ctx, time.Second)
				e := conn.Conn().Ping(check)
				stop()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	core := boundary.CoreID
	token := dbgen.PushTokenSourcesParams{ChainID: w.service.numeric, AfterTime: boundary.TokenTime}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		sources, e := q.PushCoreSources(ctx, w.service.numeric, core)
		if e != nil {
			return e
		}
		for _, source := range sources {
			core = source.ID // Advance before attempting delivery. There is no replay.
			var block struct {
				Number string `json:"block_number"`
				Hash   string `json:"block_hash"`
			}
			if json.Unmarshal(source.Payload, &block) != nil || len(block.Hash) != 66 {
				w.observe("dropped")
				continue
			}
			if e = w.deliver(ctx, block.Number, common.HexToHash(block.Hash).Bytes(), false); e != nil {
				return e
			}
		}
		tokens, e := q.PushTokenSources(ctx, token)
		if e != nil {
			return e
		}
		for _, source := range tokens {
			token.AfterTime, token.AfterJob, token.AfterGeneration = source.CommittedAt, source.JobID, source.JobGeneration
			if e = w.deliver(ctx, source.BlockNumber, source.BlockHash, true); e != nil {
				return e
			}
		}
		if e = q.PushCleanup(ctx); e != nil {
			return e
		}
		if e = q.ExportCleanup(ctx); e != nil {
			return e
		}
	}
}
func (w *PushWorker) deliver(parent context.Context, blockNumber string, hash []byte, token bool) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	q := dbgen.New(w.db)
	after := ""
	slots := make(chan struct{}, 8)
	var pending sync.WaitGroup
	defer pending.Wait()
	for range 64 {
		sources, e := q.PushActivity(ctx, dbgen.PushActivityParams{ChainID: w.service.numeric, BlockHash: hash, AfterSource: after, Token: token})
		if e != nil {
			return w.deliveryError(ctx, e)
		}
		for _, source := range sources {
			after = source.SourceKey
			from, to := source.FromAddress, source.ToAddress
			cursor := pgtype.UUID{Valid: true}
			for page := range 64 {
				followers, e := q.PushFollowers(ctx, dbgen.PushFollowersParams{ChainID: w.service.numeric, FromAddress: from, ToAddress: to, AfterWatch: cursor})
				if e != nil {
					return w.deliveryError(ctx, e)
				}
				for _, watch := range followers {
					cursor = watch.ID
					if !slices.Contains(watch.Kinds, source.SourceKind) || watch.StartNumber.Int.Cmp(number(blockNumber).Int) >= 0 {
						continue
					}
					incoming, outgoing := bytes.Equal(watch.Address, to), bytes.Equal(watch.Address, from)
					matches := incoming && (watch.Direction == "in" || watch.Direction == "both") || outgoing && (watch.Direction == "out" || watch.Direction == "both")
					if !matches {
						continue
					}
					recipients, e := q.PushRecipients(ctx, watch.UserID, number(blockNumber))
					if e != nil {
						return w.deliveryError(ctx, e)
					}
					for _, recipient := range recipients {
						select {
						case slots <- struct{}{}:
						case <-ctx.Done():
							w.observe("dropped")
							return nil
						}
						pending.Go(func() {
							defer func() { <-slots }()
							w.send(ctx, q, watch, recipient, source, blockNumber, hash, from, to)
						})
					}
				}
				if len(followers) < 200 {
					break
				}
				if page == 63 {
					w.observe("dropped")
				}
			}
		}
		if len(sources) < 100 {
			return nil
		}
	}
	w.observe("dropped")
	return nil
}
func (w *PushWorker) deliveryError(ctx context.Context, e error) error {
	if ctx.Err() != nil {
		w.observe("dropped")
		return nil
	}
	return e
}
func (w *PushWorker) send(ctx context.Context, q *dbgen.Queries, watch dbgen.PushFollowersRow, recipient dbgen.PushSubscription, source dbgen.PushActivityRow, blockNumber string, hash, from, to []byte) {
	valid, e := q.PushValidateDelivery(ctx, dbgen.PushValidateDeliveryParams{WatchID: watch.ID, BlockNumber: number(blockNumber), BlockHash: hash, SubscriptionID: recipient.ID, SessionID: recipient.SessionID, P256dh: recipient.P256dh, Auth: recipient.Auth, Kind: source.SourceKind, FromAddress: from, ToAddress: to, SourceGeneration: source.SourceGeneration})
	if e != nil || !valid || ctx.Err() != nil {
		w.observe("dropped")
		return
	}
	activity, e := decodeActivity(source.Activity, common.BytesToAddress(watch.Address).Hex())
	if e != nil {
		w.observe("dropped")
		return
	}
	label := watch.Label
	if label == "" {
		a := common.BytesToAddress(watch.Address).Hex()
		label = a[:8] + "…" + a[len(a)-4:]
	}
	title, direction := "Watchlist activity", activity.Direction
	if recipient.Language == "zh" {
		title = "关注地址有新活动"
		switch direction {
		case "in":
			direction = "转入"
		case "out":
			direction = "转出"
		case "self":
			direction = "自转账"
		}
	}
	payload, e := json.Marshal(struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Path    string `json:"path"`
		Tag     string `json:"tag"`
		Expires int64  `json:"expires"`
	}{title, label + " · " + source.SourceKind + " · " + direction, "/tx/" + activity.TransactionHash, "etherview:" + uuidText(recipient.ID) + ":" + common.BytesToHash(hash).Hex() + ":" + source.SourceKey, time.Now().Add(10 * time.Second).UnixMilli()})
	if e != nil {
		w.observe("dropped")
		return
	}
	status, e := w.sender.Send(ctx, recipient.Endpoint, recipient.P256dh, recipient.Auth, payload)
	if status == 404 || status == 410 {
		_ = q.PushDeleteExpiredEndpoint(ctx, dbgen.PushDeleteExpiredEndpointParams{ID: recipient.ID, SessionID: recipient.SessionID, P256dh: recipient.P256dh, Auth: recipient.Auth})
		w.observe("expired")
		return
	}
	if e != nil || status < 200 || status >= 300 {
		w.observe("failed")
		return
	}
	w.observe("sent")
}
