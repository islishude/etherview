package enssync

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ethrpc"
)

type RuntimeSource struct {
	Manifest Manifest
	Pool     *ethrpc.Pool
	Gateways []string
}
type RuntimeOptions struct {
	Sources      []RuntimeSource
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
	sources []*runtimeSource
}

func NewRuntime(repo *Repository, options RuntimeOptions) (*Runtime, error) {
	if repo == nil || len(options.Sources) < 1 || len(options.Sources) > 2 || options.PollInterval < 100*time.Millisecond || options.PollInterval > time.Minute {
		return nil, ErrInvalidInput
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	runtime := &Runtime{repo: repo, options: options}
	namespaces := map[enscontract.Namespace]bool{}
	for _, source := range options.Sources {
		m := source.Manifest
		if namespaces[m.Namespace] || m.Policy != options.Policy.Digest || m.TargetChainID != options.Policy.TargetChainID {
			return nil, ErrSourceIdentity
		}
		namespaces[m.Namespace] = true
		pool := source.Pool
		if m.Namespace == enscontract.Custom {
			pool = nil
		}
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
		runtime.sources = append(runtime.sources, &runtimeSource{source: source, collector: collector})
	}
	if options.Policy.Mode == "custom_only" && namespaces[enscontract.Official] {
		return nil, ErrInvalidInput
	}
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
		if !registered {
			policy := r.options.Policy
			for _, source := range r.sources {
				id, e := source.collector.EnsureSource(ctx)
				if e != nil {
					r.report(ctx, "identity", source.source.Manifest.Namespace, e)
					return
				}
				source.id.Store(id)
				if source.source.Manifest.Namespace == enscontract.Official {
					policy.OfficialSourceID = id
				} else {
					policy.CustomSourceID = id
				}
			}
			if e := r.repo.RegisterPolicy(ctx, policy); e != nil {
				r.report(ctx, "policy", "", e)
				return
			}
			registered = true
		}
		for _, source := range r.sources {
			if _, e := source.collector.Step(ctx, true); e != nil {
				r.report(ctx, "collection", source.source.Manifest.Namespace, e)
				continue
			}
			worker, e := r.worker(source)
			if e == nil {
				e = worker.Schedule(ctx, 100)
			}
			if e != nil {
				r.report(ctx, "schedule", source.source.Manifest.Namespace, e)
			}
		}
	})
}
func (r *Runtime) worker(source *runtimeSource) (*Worker, error) {
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
		for offset := range len(r.sources) {
			source := r.sources[(offset+s.index)%len(r.sources)]
			if source.id.Load() == 0 {
				continue
			}
			worker, e := r.worker(source)
			if e != nil {
				r.report(ctx, "worker", source.source.Manifest.Namespace, e)
				continue
			}
			// Each lane has bounded work and yields to collection and cancellation.
			for range 16 {
				worked, e := worker.Step(ctx)
				if e != nil {
					r.report(ctx, "resolution", source.source.Manifest.Namespace, e)
				}
				if !worked || ctx.Err() != nil {
					break
				}
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
func (r *Runtime) report(ctx context.Context, phase string, source enscontract.Namespace, e error) {
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
		r.options.Observer.RecordENS(ensresolver.MetricObservation{Source: source, Direction: phase, Outcome: "error"})
	}
	r.options.Logger.WarnContext(ctx, "ENS background work deferred", "phase", phase, "source", string(source), "code", code)
}
