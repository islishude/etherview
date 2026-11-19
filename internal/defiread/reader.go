// Package defiread provides read-only access to canonical DeFi publications.
package defiread

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/jackc/pgx/v5"
)

type Source struct {
	ID, Deployment string
	Protocol       deficontract.Protocol
}
type Reader struct {
	DB      dbaccess.Database
	Sources []Source
	Digest  string
}
type cursor struct {
	Version                                  int                   `json:"v"`
	Owner                                    string                `json:"o"`
	Protocol                                 deficontract.Protocol `json:"p"`
	Digest                                   string                `json:"d"`
	Runs                                     []string              `json:"r"`
	AfterProtocol, AfterDeployment, AfterKey string
}

func (r *Reader) Positions(ctx context.Context, req deficontract.Request) (deficontract.Page, error) {
	page := deficontract.Page{Positions: []deficontract.Position{}, Deployments: []deficontract.DeploymentStatus{}}
	if !common.IsHexAddress(req.Owner) || req.Protocol != "" && !req.Protocol.Valid() || req.Limit < 0 || req.Limit > 200 {
		return page, deficontract.ErrInput
	}
	if req.Limit == 0 {
		req.Limit = 25
	}
	owner := common.HexToAddress(req.Owner)
	cur := cursor{Version: 1, Owner: strings.ToLower(owner.Hex()), Protocol: req.Protocol, Digest: r.Digest, Runs: []string{}}
	if req.Cursor != "" {
		var e error
		cur, e = decodeCursor(req.Cursor)
		if e != nil || cur.Owner != strings.ToLower(owner.Hex()) || cur.Protocol != req.Protocol || cur.Digest != r.Digest {
			return page, deficontract.ErrCursor
		}
	}
	sources := []Source{}
	ids := []string{}
	supported := map[deficontract.Protocol]bool{}
	for _, source := range r.Sources {
		if req.Protocol == "" || req.Protocol == source.Protocol {
			sources = append(sources, source)
			ids = append(ids, source.ID)
			supported[source.Protocol] = true
		}
	}
	for _, p := range deficontract.Protocols() {
		if !supported[p] && (req.Protocol == "" || req.Protocol == p) {
			page.Deployments = append(page.Deployments, deficontract.DeploymentStatus{Protocol: p, Deployment: string(p), State: "not_configured"})
		}
	}
	if len(sources) == 0 {
		if req.Cursor != "" {
			return page, deficontract.ErrCursor
		}
		return page, nil
	}
	if r.DB == nil {
		return page, deficontract.ErrUnavailable
	}
	tx, e := r.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return page, e
	}
	defer dbaccess.Rollback(ctx, tx)
	q := dbgen.New(tx)
	pubs, e := q.ListDeFiPublications(ctx, cur.Runs, ids)
	if e != nil {
		return page, e
	}
	bySource := map[string]dbgen.ListDeFiPublicationsRow{}
	for _, pub := range pubs {
		bySource[pub.SourceID] = pub
	}
	runs := []string{}
	for _, source := range sources {
		status := deficontract.DeploymentStatus{Protocol: source.Protocol, Deployment: source.Deployment, State: "scanning"}
		if pub, ok := bySource[source.ID]; ok && pub.RunID != "" {
			runs = append(runs, pub.RunID)
			status.State = "complete"
			if pub.FailureCode != "" {
				status.State = "partial"
				status.FailureCode = pub.FailureCode
			}
			status.BlockNumber = pub.BlockNumber
			status.BlockHash = common.BytesToHash(pub.BlockHash).Hex()
			status.CoverageStart = pub.CoverageStart
			status.CoverageEnd = pub.BlockNumber
			status.UpdatedAt = pub.PublishedAt.Time.UTC().Format(time.RFC3339)
		}
		if pub, ok := bySource[source.ID]; ok && pub.SourceFailure != "" {
			status.FailureCode = pub.SourceFailure
			if status.BlockNumber == "" {
				status.State = "unavailable"
			} else {
				status.State = "partial"
			}
		}
		page.Deployments = append(page.Deployments, status)
	}
	if req.Cursor != "" {
		a := append([]string{}, runs...)
		b := append([]string{}, cur.Runs...)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			return page, deficontract.ErrCursor
		}
	}
	rows, e := q.ListDeFiPositions(ctx, dbgen.ListDeFiPositionsParams{RunIds: runs, Owner: owner[:], AfterProtocol: cur.AfterProtocol, AfterDeployment: cur.AfterDeployment, AfterKey: cur.AfterKey, RowLimit: int32(req.Limit + 1)})
	if e != nil {
		return page, e
	}
	for _, row := range rows[:min(len(rows), req.Limit)] {
		var position deficontract.Position
		if json.Unmarshal(row.Position, &position) != nil || !strings.EqualFold(position.Owner, owner.Hex()) || position.Protocol != deficontract.Protocol(row.Protocol) || position.Deployment != row.Deployment || position.ID != row.Key {
			return page, deficontract.ErrIntegrity
		}
		page.Positions = append(page.Positions, position)
	}
	if len(rows) > req.Limit {
		last := rows[req.Limit-1]
		cur.Runs = runs
		cur.AfterProtocol = last.Protocol
		cur.AfterDeployment = last.Deployment
		cur.AfterKey = last.Key
		page.NextCursor, e = encodeCursor(cur)
		if e != nil {
			return page, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return page, e
	}
	return page, nil
}
