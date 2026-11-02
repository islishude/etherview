package facilitator

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
	exact "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"
)

type chainRuntime struct {
	adapter           *chainClient
	scheme            *exact.ExactEvmScheme
	gate              chan struct{}
	verifyGate        chan struct{}
	healthy           atomic.Bool
	successes         atomic.Uint64
	failures          atomic.Uint64
	insufficientFunds atomic.Bool
}

// Service owns its RPC clients, singleton connection and bounded per-chain queues.
type Service struct {
	config    Config
	token     []byte
	pool      *pgxpool.Pool
	queries   settlementJournal
	lock      *pgx.Conn
	lockMu    sync.Mutex
	active    atomic.Bool
	chains    map[string]*chainRuntime
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	closeOnce sync.Once
	cleanups  []func()
}

func New(ctx context.Context, c Config) (*Service, error) {
	resolved, err := resolveConfig(c)
	if err != nil {
		return nil, err
	}
	defer resolved.clear()

	s := &Service{config: c, token: bytes.Clone(bytes.TrimSpace(resolved.token)), chains: map[string]*chainRuntime{}}
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()
	s.pool, err = pgxpool.New(ctx, strings.TrimSpace(string(resolved.database)))
	if err != nil {
		return nil, errors.New("database_unavailable")
	}
	s.cleanups = append(s.cleanups, s.pool.Close)
	s.queries = dbgen.New(s.pool)
	s.lock, err = pgx.Connect(ctx, strings.TrimSpace(string(resolved.database)))
	if err != nil {
		return nil, errors.New("database_unavailable")
	}
	s.cleanups = append(s.cleanups, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.lock.Close(ctx)
	})
	acquired, err := dbgen.New(s.lock).FacilitatorAcquireLock(ctx)
	if err != nil || !acquired {
		return nil, errors.New("facilitator_already_active")
	}
	signers := map[string]digestSigner{}
	for id, sc := range c.Signers {
		signer, e := loadSigner(ctx, sc, resolved.signers[id])
		if e != nil {
			return nil, e
		}
		signers[id] = signer
	}
	for i, ch := range c.Chains {
		if _, e := s.queries.FacilitatorPending(ctx, ch.Network); e != nil {
			return nil, errors.New("journal_unavailable")
		}
		client, e := dialRPC(ctx, resolved.rpc[i])
		if e != nil {
			return nil, errors.New("rpc_unavailable")
		}
		s.cleanups = append(s.cleanups, client.Close)
		adapter := &chainClient{client: client, signer: signers[ch.Signer], config: ch, queries: s.queries, guard: s.guard}
		runtime := &chainRuntime{adapter: adapter, gate: make(chan struct{}, 1), verifyGate: make(chan struct{}, 8)}
		s.chains[ch.Network] = runtime
		id, e := client.ChainID(ctx)
		expected, _ := networkID(ch.Network)
		if e != nil || id == nil || id.Cmp(expected) != 0 {
			return nil, errors.New("chain_identity_mismatch")
		}
		addresses := map[string]bool{}
		for _, t := range ch.Tokens {
			addresses[t.Address] = true
			for _, m := range t.Methods {
				if m == "permit2" {
					addresses[evm.PERMIT2Address] = true
					addresses[evm.X402ExactPermit2ProxyAddress] = true
				}
			}
		}
		for a := range addresses {
			code, e := adapter.GetCode(ctx, a)
			if e != nil || len(code) == 0 {
				return nil, errors.New("required_contract_missing")
			}
		}
		runtime.scheme = exact.NewExactEvmScheme(adapter, &exact.ExactEvmSchemeConfig{SimulateInSettle: true})
		runtime.scheme.SetPendingSettlementStore(pendingJournal{queries: s.queries})
		runtime.healthy.Store(true)
	}
	s.active.Store(true)
	workerCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, ch := range s.chains {
		s.workers.Add(1)
		go s.monitor(workerCtx, ch)
	}
	success = true
	return s, nil
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.active.Store(false)
		if s.cancel != nil {
			s.cancel()
		}
		s.workers.Wait()
		for _, cleanup := range slices.Backward(s.cleanups) {
			cleanup()
		}
		clear(s.token)
	})
}
