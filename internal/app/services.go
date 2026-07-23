package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/observability"
)

type databasePinger interface {
	PingContext(context.Context) error
}

type operationalService struct {
	address         string
	shutdownTimeout time.Duration
	db              databasePinger
	registry        *observability.Registry
	lifecycle       *components.Lifecycle
	logger          *slog.Logger
	telemetry       *observability.Telemetry
}

func (s *operationalService) Name() string { return "operations-http" }

func (s *operationalService) Run(ctx context.Context) error {
	if s.db == nil || s.lifecycle == nil {
		return errors.New("operational HTTP dependencies are not configured")
	}
	server := s.httpServer()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve operational HTTP: %w", err)
	case <-ctx.Done():
		timeout := s.shutdownTimeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown operational HTTP: %w", err)
		}
		err := <-done
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return ctx.Err()
	}
}

func (s *operationalService) httpServer() *http.Server {
	return &http.Server{
		Addr:              s.address,
		Handler:           s.handler(),
		ErrorLog:          observability.HTTPServerErrorLog(s.logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func (s *operationalService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte("{\"status\":\"live\"}\n"))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if !s.lifecycle.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(pingCtx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte("{\"status\":\"ready\"}\n"))
	})
	observability.MountMetrics(mux, s.registry)
	return observability.HTTPMiddleware(mux, observability.HTTPOptions{
		Registry: s.registry, Logger: s.logger, Telemetry: s.telemetry,
		Route: func(request *http.Request) string {
			return observability.MuxRoutePattern(mux, request)
		},
	})
}

// databaseRoleService keeps a role process live while its durable worker is
// disabled by feature configuration. It still verifies PostgreSQL health; it
// never marks queued work successful or substitutes in-memory correctness.
type databaseRoleService struct {
	name     string
	db       *sql.DB
	interval time.Duration
}

func (s *databaseRoleService) Name() string { return s.name }

func (s *databaseRoleService) Run(ctx context.Context) error {
	interval := s.interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := s.db.PingContext(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("%s database health: %w", s.name, err)
			}
		}
	}
}

// namedWorkerService gives worker implementations with a fixed Name method a
// unique supervisor identity without changing their durable lease behavior.
type namedWorkerService struct {
	name   string
	worker components.Service
}

func (s *namedWorkerService) Name() string { return s.name }

func (s *namedWorkerService) Run(ctx context.Context) error {
	return s.worker.Run(ctx)
}

func indexedWorkerName(base string, index int) string {
	return fmt.Sprintf("%s-%02d", base, index+1)
}

func workerComponentKey(base string, index int) string {
	return indexedWorkerName(base, index)
}

func registerWorkerPool(
	registry *components.Registry,
	role components.Role,
	componentBase string,
	serviceBase string,
	count int,
	build func(index int, serviceName string) (components.Service, error),
) error {
	if registry == nil || role == "" || componentBase == "" || serviceBase == "" || build == nil {
		return errors.New("worker pool registration is incomplete")
	}
	if count <= 0 {
		return errors.New("worker pool count must be positive")
	}
	for index := 0; index < count; index++ {
		serviceName := indexedWorkerName(serviceBase, index)
		worker, err := build(index, serviceName)
		if err != nil {
			return fmt.Errorf("build %s: %w", serviceName, err)
		}
		if worker == nil {
			return fmt.Errorf("build %s: nil worker", serviceName)
		}
		service := worker
		if worker.Name() != serviceName {
			service = &namedWorkerService{name: serviceName, worker: worker}
		}
		componentKey := workerComponentKey(componentBase, index)
		if err := registry.Register(role, componentKey, func() (components.Service, error) {
			return service, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func addWorkerComponentKeys(add func(string), componentBase string, count int) {
	for index := 0; index < count; index++ {
		add(workerComponentKey(componentBase, index))
	}
}
