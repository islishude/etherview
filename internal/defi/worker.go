package defi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Worker struct {
	DB       dbaccess.Database
	Pool     *ethrpc.Pool
	Manifest Manifest
	Logger   *slog.Logger
}

func (*Worker) Name() string { return "defi-index" }
func (w *Worker) Run(ctx context.Context) error {
	if w.DB == nil || w.Pool == nil || w.Manifest.Validate() != nil {
		return deficontract.ErrInput
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			for _, d := range w.Manifest.Deployments {
				step, cancel := context.WithTimeout(ctx, 40*time.Second)
				e := w.Step(step, d)
				cancel()
				if e != nil && ctx.Err() == nil && w.Logger != nil {
					w.Logger.Warn("DeFi step unavailable", "deployment", d.ID, "code", failureCode(e))
				}
			}
			timer.Reset(time.Second)
		}
	}
}
func numeric(s string) pgtype.Numeric          { var n pgtype.Numeric; _ = n.Scan(s); return n }
func number(n uint64) pgtype.Numeric           { return numeric(strconv.FormatUint(n, 10)) }
func sourceID(m Manifest, d Deployment) string { return m.Digest() + ":" + d.ID }
func failureCode(e error) string {
	if errors.Is(e, deficontract.ErrUnsupported) {
		return "unsupported"
	}
	if errors.Is(e, deficontract.ErrIntegrity) {
		return "integrity"
	}
	return "unavailable"
}
func (w *Worker) Step(ctx context.Context, d Deployment) (resultErr error) {
	q := dbgen.New(w.DB)
	source := sourceID(w.Manifest, d)
	chain := numeric(w.Manifest.ChainID)
	if e := q.RegisterDeFiSource(ctx, dbgen.RegisterDeFiSourceParams{ID: source, ChainID: chain, Deployment: d.ID, Protocol: string(d.Protocol), ManifestDigest: w.Manifest.Digest()}); e != nil {
		return e
	}
	lease := uuid.NewString()
	if _, e := q.ClaimDeFiSource(ctx, &lease, source); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if resultErr != nil && !errors.Is(ctx.Err(), context.Canceled) {
			_, _ = q.FailDeFiSource(cleanup, failureCode(resultErr), source, &lease)
			return
		}
		_, _ = q.ReleaseDeFiSource(cleanup, 1, source, &lease)
	}()
	run, e := q.ActiveDeFiRun(ctx, source)
	if errors.Is(e, pgx.ErrNoRows) {
		return w.startRun(ctx, d, source, lease, chain)
	}
	if e != nil {
		return e
	}
	attachment, e := q.DeFiCanonicalBlock(ctx, chain, run.BlockNumber, run.BlockHash)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && !attachment.Time.Equal(run.Attachment.Time) {
		return dbaccess.WithTransaction(ctx, w.DB, func(tx *dbgen.Queries) error {
			if _, e := tx.FenceDeFiSource(ctx, source, &lease); e != nil {
				return e
			}
			return tx.InvalidateDeFiRun(ctx, run.ID)
		})
	}
	if e != nil {
		return e
	}
	endpoint, e := w.Pool.Acquire(ethrpc.PurposeState)
	if e != nil {
		return deficontract.ErrUnavailable
	}
	state := &State{RPC: endpoint, Hash: common.BytesToHash(run.BlockHash), cache: make(map[string][]byte)}
	if e = state.Verify(ctx, d); e != nil {
		return e
	}
	advance := dbgen.AdvanceDeFiRunParams{ID: run.ID, State: run.State, InstrumentOffset: run.InstrumentOffset, ScanNumber: run.ScanNumber, ScanIndex: run.ScanIndex, AfterCandidate: run.AfterCandidate}
	var instruments []Instrument
	var candidates []Candidate
	var positions []deficontract.Position
	switch run.State {
	case "seed_instruments":
		after := common.Address{}
		if run.AfterCandidate != "" {
			after = common.HexToAddress(run.AfterCandidate)
		}
		rows, err := q.ListDeFiSeedInstruments(ctx, *run.PreviousRun, after[:])
		e = err
		if e == nil {
			for _, row := range rows[:min(len(rows), 200)] {
				a := common.BytesToAddress(row.Address)
				instruments = append(instruments, Instrument{Address: a, Pool: common.BytesToAddress(row.Pool), Kind: row.Kind})
				advance.AfterCandidate = a.Hex()
			}
			if len(rows) <= 200 {
				advance.State = "instruments"
				advance.AfterCandidate = ""
			}
		}
	case "seed_candidates":
		rows, err := q.ListDeFiSeedCandidates(ctx, *run.PreviousRun, run.AfterCandidate, run.ID)
		e = err
		if e == nil {
			for _, row := range rows[:min(len(rows), 200)] {
				var c Candidate
				if json.Unmarshal(row.Candidate, &c) != nil {
					return deficontract.ErrIntegrity
				}
				candidates = append(candidates, c)
				advance.AfterCandidate = row.Key
			}
			if len(rows) <= 200 {
				advance.State = "candidates"
				advance.AfterCandidate = ""
			}
		}
	case "instruments":
		var next uint64
		var done bool
		instruments, next, done, e = Instruments(ctx, state, d, uint64(run.InstrumentOffset))
		advance.InstrumentOffset = int64(next)
		if done {
			advance.State = "candidates"
			if run.PreviousRun != nil {
				advance.State = "seed_candidates"
			}
		}
	case "candidates":
		logs, err := q.ListDeFiCandidateLogs(ctx, dbgen.ListDeFiCandidateLogsParams{RunID: run.ID, ChainID: chain, StartBlock: run.CoverageStart, EndBlock: run.BlockNumber, AfterNumber: run.ScanNumber, AfterIndex: run.ScanIndex, TransferTopic: transferTopic[:]})
		e = err
		if e == nil {
			for _, row := range logs[:min(len(logs), 200)] {
				found, log, err := Candidates(row.Raw, row.Kind, common.BytesToAddress(row.Pool))
				if err != nil {
					e = err
					break
				}
				candidates = append(candidates, found...)
				advance.ScanNumber = number(log.BlockNumber)
				advance.ScanIndex = int64(log.Index)
			}
			if len(logs) <= 200 {
				advance.State = "observations"
			}
		}
	case "observations":
		rows, err := q.ListDeFiCandidates(ctx, run.ID, run.AfterCandidate)
		e = err
		text, err := dbaccess.NumericText(run.BlockNumber)
		if err != nil {
			return err
		}
		if e == nil {
			for _, row := range rows[:min(len(rows), 10)] {
				if !hasStepBudget(ctx) {
					if len(positions) == 0 {
						return deficontract.ErrUnavailable
					}
					break
				}
				var candidate Candidate
				if json.Unmarshal(row.Candidate, &candidate) != nil {
					return deficontract.ErrIntegrity
				}
				sub, cancel := context.WithTimeout(ctx, 3*time.Second)
				p, err := Observe(sub, &State{RPC: endpoint, Hash: state.Hash, cache: state.cache}, d, candidate, text.String)
				cancel()
				if err != nil {
					p.Assets = []deficontract.Asset{}
					p.Debts = []deficontract.Asset{}
					p.Claimable = []deficontract.Asset{}
					p.UnsettledFees = []deficontract.Asset{}
					p.ReceiptAmount = ""
					p.Liquidity = ""
					p.Range = nil
					p.State = "unavailable"
					if errors.Is(err, deficontract.ErrUnsupported) {
						p.State = "unsupported"
					}
					p.Limitations = append(p.Limitations, failureCode(err))
					advance.FailureCode = "partial"
				}
				if p.State == "unsupported" || p.State == "unavailable" {
					advance.FailureCode = "partial"
				}
				positions = append(positions, p)
				advance.AfterCandidate = row.Key
			}
			if len(rows) <= 10 && len(positions) == len(rows) {
				advance.State = "complete"
			}
		}
	default:
		return deficontract.ErrIntegrity
	}
	if e != nil {
		return e
	}
	for _, item := range instruments {
		if item.FailureCode != "" {
			advance.FailureCode = "partial"
		}
	}
	return w.publishBatch(ctx, d, source, lease, chain, run, workBatch{advance, instruments, candidates, positions})
}

type workBatch struct {
	advance     dbgen.AdvanceDeFiRunParams
	instruments []Instrument
	candidates  []Candidate
	positions   []deficontract.Position
}

func (w *Worker) publishBatch(ctx context.Context, d Deployment, source, lease string, chain pgtype.Numeric, run dbgen.DefiRun, batch workBatch) error {
	advance, instruments, candidates, positions := batch.advance, batch.instruments, batch.candidates, batch.positions
	return dbaccess.WithTransaction(ctx, w.DB, func(tx *dbgen.Queries) error {
		if _, e := tx.FenceDeFiSource(ctx, source, &lease); e != nil {
			return e
		}
		current, e := tx.DeFiCanonicalBlock(ctx, chain, run.BlockNumber, run.BlockHash)
		if e != nil || !current.Time.Equal(run.Attachment.Time) {
			return deficontract.ErrUnavailable
		}
		for _, i := range instruments {
			if e = tx.InsertDeFiInstrument(ctx, dbgen.InsertDeFiInstrumentParams{RunID: run.ID, Address: i.Address[:], Pool: i.Pool[:], Kind: i.Kind}); e != nil {
				return e
			}
		}
		for _, c := range candidates {
			raw, err := json.Marshal(c)
			if err != nil {
				return err
			}
			if e = tx.InsertDeFiCandidate(ctx, dbgen.InsertDeFiCandidateParams{RunID: run.ID, Key: c.Key(), Owner: c.Owner[:], Candidate: raw}); e != nil {
				return e
			}
		}
		for _, p := range positions {
			raw, err := json.Marshal(p)
			if err != nil {
				return err
			}
			owner := common.HexToAddress(p.Owner)
			if e = tx.InsertDeFiObservation(ctx, dbgen.InsertDeFiObservationParams{RunID: run.ID, Key: p.ID, Owner: owner[:], State: p.State, Position: raw}); e != nil {
				return e
			}
		}
		changed, e := tx.AdvanceDeFiRun(ctx, advance)
		if e != nil {
			return e
		}
		if changed != 1 {
			return deficontract.ErrUnavailable
		}
		if e = tx.ClearDeFiSourceFailure(ctx, source, &lease); e != nil {
			return e
		}
		if advance.State == "complete" {
			text, err := dbaccess.NumericText(run.BlockNumber)
			if err != nil {
				return err
			}
			if err = tx.EmitDeFiPublication(ctx, chain, d.ID, text.String); err != nil {
				return err
			}
			_, e = tx.ReleaseDeFiSource(ctx, 60, source, &lease)
		}
		return e
	})
}

func (w *Worker) startRun(ctx context.Context, d Deployment, source, lease string, chain pgtype.Numeric) error {
	q := dbgen.New(w.DB)
	block, e := q.LatestDeFiBlock(ctx, w.Manifest.Genesis[:], chain, number(d.Start))
	if errors.Is(e, pgx.ErrNoRows) {
		// Core history may still be synchronizing. This is not an adapter failure.
		_, err := q.ReleaseDeFiSource(ctx, 30, source, &lease)
		return err
	}
	if e != nil {
		return e
	}
	if _, e = q.DeFiCanonicalBlock(ctx, chain, number(d.Start), d.StartHash[:]); e != nil {
		return deficontract.ErrUnsupported
	}
	input := dbgen.InsertDeFiRunParams{ID: uuid.NewString(), SourceID: source, BlockNumber: numeric(block.Number), BlockHash: block.BlockHash, Attachment: block.UpdatedAt, CoverageStart: number(d.Start), ScanNumber: number(d.Start), ScanIndex: -1, State: "instruments"}
	pubs, err := q.ListDeFiPublications(ctx, []string{}, []string{source})
	if err != nil {
		return err
	}
	if len(pubs) > 0 && pubs[0].RunID != "" {
		previous, err := q.GetDeFiRun(ctx, pubs[0].RunID)
		if err != nil {
			return err
		}
		if bytes.Equal(previous.BlockHash, block.BlockHash) {
			attempts, err := q.CountDeFiBlockPublications(ctx, source, block.BlockHash, block.UpdatedAt)
			if err != nil {
				return err
			}
			// A temporary per-position RPC failure must recover even when Core's
			// head is stationary. Each repair is a new immutable publication.
			if previous.FailureCode == "" || attempts >= 5 {
				_, err = q.ReleaseDeFiSource(ctx, 60, source, &lease)
				return err
			}
		}
		input.PreviousRun = &previous.ID
		input.ScanNumber = previous.BlockNumber
		input.ScanIndex = math.MaxInt64
		if d.Protocol == deficontract.Curve {
			input.ScanNumber = number(d.Start)
			input.ScanIndex = -1
		}
		if d.Protocol == deficontract.UniswapV2 {
			input.State = "seed_instruments"
			input.InstrumentOffset = previous.InstrumentOffset
		}
	}
	return dbaccess.WithTransaction(ctx, w.DB, func(tx *dbgen.Queries) error {
		if _, e := tx.FenceDeFiSource(ctx, source, &lease); e != nil {
			return e
		}
		return tx.InsertDeFiRun(ctx, input)
	})
}

// Reserve time for the lease-fenced transaction after external RPC work.
func hasStepBudget(ctx context.Context) bool {
	deadline, bounded := ctx.Deadline()
	return !bounded || time.Until(deadline) >= 5*time.Second
}
