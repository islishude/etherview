package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WorkerOptions struct {
	ServiceName    string
	WorkerID       string
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	MaxOutputBytes int
	Public         bool
}

func (options *WorkerOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "contract-verification-worker"
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 2 * time.Minute
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = 64 << 20
	}
}

type Worker struct {
	repository Repository
	compiler   Compiler
	options    WorkerOptions
}

func NewWorker(repository Repository, compiler Compiler, options WorkerOptions) (*Worker, error) {
	if repository == nil {
		return nil, errors.New("verification worker requires a repository")
	}
	if compiler == nil {
		return nil, errors.New("verification worker requires a compiler")
	}
	options.defaults()
	options.ServiceName = strings.TrimSpace(options.ServiceName)
	options.WorkerID = strings.TrimSpace(options.WorkerID)
	if options.ServiceName == "" || options.WorkerID == "" {
		return nil, errors.New("verification worker service and worker IDs are required")
	}
	if len(options.ServiceName) > 128 || len(options.WorkerID) > 128 {
		return nil, errors.New("verification worker service or worker ID exceeds 128 bytes")
	}
	if options.LeaseDuration < 3*time.Millisecond || options.PollInterval <= 0 || options.MaxOutputBytes <= 0 {
		return nil, errors.New("verification worker limits are invalid")
	}
	if options.Public && !compiler.HardIsolated() {
		return nil, ErrSandboxRequired
	}
	return &Worker{repository: repository, compiler: compiler, options: options}, nil
}

func (worker *Worker) Name() string {
	if worker == nil {
		return "contract-verification-worker"
	}
	return worker.options.ServiceName
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || worker.repository == nil || worker.compiler == nil {
		return errors.New("run nil verification worker")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		found, err := worker.ProcessOne(ctx)
		if err != nil {
			return err
		}
		if !found {
			if err := waitForContext(ctx, worker.options.PollInterval); err != nil {
				return err
			}
		}
	}
}

func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	lease, found, err := worker.repository.Claim(ctx, worker.options.WorkerID, worker.options.LeaseDuration)
	if err != nil || !found {
		return found, err
	}
	return true, worker.processLease(ctx, lease)
}

type compileOutcome struct {
	completion *Completion
	errorCode  ErrorCode
	cancelled  bool
}

func (worker *Worker) processLease(ctx context.Context, lease VerificationLease) error {
	provenance, err := worker.compiler.Provenance(
		lease.Job.Request.Language, lease.Job.Request.CompilerVersion,
	)
	if err != nil {
		return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
	}
	if lease.Job.RequiresHardIsolation && !provenance.HardIsolated {
		return worker.failLease(ctx, lease, ErrorSandboxRequired)
	}
	if err := worker.repository.BindCompiler(ctx, lease, provenance); err != nil {
		switch {
		case errors.Is(err, ErrSandboxRequired):
			return worker.failLease(ctx, lease, ErrorSandboxRequired)
		case errors.Is(err, ErrCompilerProvenanceConflict):
			return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
		default:
			return fmt.Errorf("bind verification compiler: %w", err)
		}
	}
	lease.Job.Compiler = &provenance
	compileContext, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan compileOutcome, 1)
	go func() {
		outcome := compileOutcome{}
		defer func() {
			if recover() != nil {
				outcome = compileOutcome{errorCode: ErrorCompileFailed}
			}
			finished <- outcome
		}()
		output, err := worker.compiler.Compile(
			compileContext,
			lease.Job.Request.Language,
			lease.Job.Request.CompilerVersion,
			lease.Job.Request.StandardJSON,
		)
		if err != nil {
			if compileContext.Err() != nil {
				outcome.cancelled = true
				return
			}
			outcome.errorCode = ErrorCompileFailed
			return
		}
		completion, code := buildCompletion(lease.Job.Request, output, worker.options.MaxOutputBytes)
		if code != "" {
			outcome.errorCode = code
			return
		}
		outcome.completion = &completion
	}()

	heartbeat := time.NewTicker(worker.options.LeaseDuration / 3)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-heartbeat.C:
			if err := worker.repository.Renew(ctx, lease, worker.options.LeaseDuration); err != nil {
				cancel()
				return fmt.Errorf("renew verification lease: %w", err)
			}
		case outcome := <-finished:
			if outcome.cancelled {
				if err := ctx.Err(); err != nil {
					return err
				}
				return errors.New("verification compiler cancelled")
			}
			if outcome.errorCode != "" {
				return worker.failLease(ctx, lease, outcome.errorCode)
			}
			if outcome.completion == nil {
				return errors.New("verification compiler returned no outcome")
			}
			if err := worker.repository.Complete(ctx, lease, *outcome.completion); errors.Is(err, ErrTargetNotCanonical) {
				return nil
			} else if err != nil {
				return fmt.Errorf("complete verification job: %w", err)
			}
			return nil
		}
	}
}

func (worker *Worker) failLease(ctx context.Context, lease VerificationLease, code ErrorCode) error {
	if err := worker.repository.Fail(ctx, lease, code); err != nil {
		return fmt.Errorf("fail verification job: %w", err)
	}
	return nil
}

func buildCompletion(request Request, compilerOutput []byte, maximum int) (Completion, ErrorCode) {
	if len(compilerOutput) == 0 {
		return Completion{}, ErrorCompilerOutput
	}
	if maximum <= 0 || len(compilerOutput) > maximum {
		return Completion{}, ErrorCompilerTooLarge
	}
	artifact, err := ExtractArtifact(compilerOutput, request.ContractIdentifier)
	if err != nil {
		return Completion{}, ErrorCompilerOutput
	}
	match, err := MatchArtifact(request, artifact)
	if err != nil {
		return Completion{}, ErrorMatchFailed
	}
	kind := summarizeMatch(match)
	completion := Completion{Kind: kind, Match: match}
	if kind == MatchMismatch {
		return completion, ""
	}
	sources, settings, err := extractSourcesAndSettings(request)
	if err != nil || !jsonArray(artifact.ABI) {
		return Completion{}, ErrorCompilerOutput
	}
	completion.Artifact = artifact
	completion.Sources = sources
	completion.Settings = settings
	return completion, ""
}

func extractSourcesAndSettings(request Request) (json.RawMessage, json.RawMessage, error) {
	var input struct {
		Sources  json.RawMessage `json:"sources"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(request.StandardJSON, &input); err != nil {
		return nil, nil, errors.New("standard JSON is invalid")
	}
	if !jsonObject(input.Sources) {
		return nil, nil, errors.New("standard JSON sources must be an object")
	}
	if len(input.Settings) == 0 {
		input.Settings = json.RawMessage(`{}`)
	}
	if !jsonObject(input.Settings) {
		return nil, nil, errors.New("standard JSON settings must be an object")
	}
	if request.ConstructorArgs != "" || request.LicenseType != "" {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(input.Settings, &settings); err != nil {
			return nil, nil, errors.New("standard JSON settings must be an object")
		}
		for key, value := range map[string]string{
			"constructorArguments": request.ConstructorArgs,
			"licenseType":          request.LicenseType,
		} {
			if value == "" {
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, nil, errors.New("verification metadata is invalid")
			}
			settings[key] = encoded
		}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return nil, nil, errors.New("verification settings are invalid")
		}
		input.Settings = encoded
	}
	return append(json.RawMessage(nil), input.Sources...), append(json.RawMessage(nil), input.Settings...), nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
