package enssync

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/ethrpc"
)

type RuntimeSource struct {
	Manifest Manifest
	Pool     *ethrpc.Pool
	Gateways []string
}
type RuntimeOptions struct {
	Source       RuntimeSource
	Policy       EffectivePolicy
	Collector    CollectorOptions
	Worker       WorkerOptions
	PollInterval time.Duration
	Logger       *slog.Logger
	Observer     ensresolver.Observer
}
type runtimeSource struct {
	source    RuntimeSource
	collector *Collector
	id        atomic.Int64
}
type Runtime struct {
	repo    *Repository
	options RuntimeOptions
	source  *runtimeSource
}

func NewRuntime(repo *Repository, options RuntimeOptions) (*Runtime, error) {
	if repo == nil || options.PollInterval < 100*time.Millisecond || options.PollInterval > time.Minute {
		return nil, ErrInvalidInput
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	runtime := &Runtime{repo: repo, options: options}
	source := options.Source
	m := source.Manifest
	if m.Policy != options.Policy.Digest || m.TargetChainID != options.Policy.TargetChainID {
		return nil, ErrSourceIdentity
	}
	pool := source.Pool
	collector, e := NewCollector(repo, m, pool, options.Collector)
	if e != nil {
		return nil, e
	}
	workerOptions := options.Worker
	workerOptions.SourceID = 1
	workerOptions.Manifest = m
	workerOptions.Pool = source.Pool
	workerOptions.Registry = m.Registry
	workerOptions.UniversalResolver = m.UniversalResolver
	workerOptions.Gateways = source.Gateways
	if _, e = NewWorker(repo, workerOptions); e != nil {
		return nil, e
	}
	runtime.source = &runtimeSource{source: source, collector: collector}
	return runtime, nil
}

type CollectionService struct{ runtime *Runtime }
type ResolutionService struct {
	runtime *Runtime
	index   int
}

func (r *Runtime) CollectionService() *CollectionService { return &CollectionService{r} }
func (r *Runtime) ResolutionService(index int) *ResolutionService {
	return &ResolutionService{r, index}
}
func (*CollectionService) Name() string   { return "ens-collection" }
func (s *ResolutionService) Name() string { return "ens-resolution-" + strconv.Itoa(s.index) }
func (s *CollectionService) Run(ctx context.Context) error {
	r := s.runtime
	registered := false
	return poll(ctx, r.options.PollInterval, func() {
		source := r.source
		if !registered {
			id, err := source.collector.EnsureSource(ctx)
			if err != nil {
				r.report(ctx, "identity", err)
				return
			}
			source.id.Store(id)
			policy := r.options.Policy
			policy.SourceID = id
			if err := r.repo.RegisterPolicy(ctx, policy); err != nil {
				r.report(ctx, "policy", err)
				return
			}
			registered = true
		}
		if _, err := source.collector.Step(ctx, true); err != nil {
			r.report(ctx, "collection", err)
		}
		worker, err := r.worker()
		if err == nil {
			err = worker.Schedule(ctx, 100)
		}
		if err != nil {
			r.report(ctx, "schedule", err)
		}
	})
}
func (r *Runtime) worker() (*Worker, error) {
	source := r.source
	options := r.options.Worker
	options.Observer = r.options.Observer
	options.SourceID = source.id.Load()
	options.Manifest = source.source.Manifest
	options.Pool = source.source.Pool
	options.Registry = options.Manifest.Registry
	options.UniversalResolver = options.Manifest.UniversalResolver
	options.Gateways = source.source.Gateways
	return NewWorker(r.repo, options)
}
func (s *ResolutionService) Run(ctx context.Context) error {
	r := s.runtime
	return poll(ctx, r.options.PollInterval, func() {
		if r.source.id.Load() == 0 {
			return
		}
		worker, err := r.worker()
		if err != nil {
			r.report(ctx, "worker", err)
			return
		}
		// Each lane has bounded work and yields to collection and cancellation.
		for range 16 {
			worked, err := worker.Step(ctx)
			if err != nil {
				r.report(ctx, "resolution", err)
			}
			if !worked || ctx.Err() != nil {
				break
			}
		}
	})
}
func poll(ctx context.Context, interval time.Duration, step func()) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if ctx.Err() != nil {
				return nil
			}
			step()
			timer.Reset(interval)
		}
	}
}
func (r *Runtime) report(ctx context.Context, phase string, e error) {
	if ctx.Err() != nil {
		return
	}
	code := "storage_unavailable"
	if typed, ok := errors.AsType[*ensresolver.ResolutionError](e); ok {
		code = typed.Code
	} else {
		for _, known := range []error{ErrInvalidInput, ErrFenceLost, ErrSourceIdentity, ErrSourceIntegrity, ErrCoverageGap, ErrUnsupportedDeployment, ErrCollectionLimit, ErrInvalidEvent, ErrRPCUnavailable} {
			if errors.Is(e, known) {
				code = known.Error()
				break
			}
		}
	}
	if r.options.Observer != nil {
		r.options.Observer.RecordENS(ensresolver.MetricObservation{Direction: phase, Outcome: "error"})
	}
	r.options.Logger.WarnContext(ctx, "ENS background work deferred", "phase", phase, "code", code)
}
