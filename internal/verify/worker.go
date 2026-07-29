package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	Observer       VerificationObserver
	Sourcify       SourcifyWorkflow
}

type SourcifyWorkflow interface {
	RunV2(context.Context, JobKind, json.RawMessage) (SourcifyWorkflowResult, error)
}

// VerificationObserver receives only controlled terminal/result labels.
type VerificationObserver interface {
	RecordVerificationJob(result string)
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
	err = worker.processLease(ctx, lease)
	if err != nil && ctx.Err() == nil {
		worker.observe("error")
	}
	return true, err
}

type compileOutcome struct {
	completion *Completion
	errorCode  ErrorCode
	cancelled  bool
	fatal      error
}

const compilerCancellationCleanupTimeout = 8 * time.Second

func (worker *Worker) processLease(ctx context.Context, lease VerificationLease) error {
	if lease.Job.RequestV2 != nil {
		return worker.processLeaseV2(ctx, lease)
	}
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
				outcome = compileOutcome{fatal: ErrCompilerRuntime}
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
			if errors.Is(err, ErrCompilerCleanup) || errors.Is(err, ErrCompilerRuntime) {
				if errors.Is(err, ErrCompilerCleanup) {
					outcome.fatal = ErrCompilerCleanup
				} else {
					outcome.fatal = ErrCompilerRuntime
				}
				return
			}
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
			outcome, ok := waitForCompilerCleanup(finished)
			if !ok {
				return ErrCompilerCleanup
			}
			if outcome.fatal != nil {
				return outcome.fatal
			}
			return ctx.Err()
		case <-heartbeat.C:
			if err := worker.repository.Renew(ctx, lease, worker.options.LeaseDuration); err != nil {
				cancel()
				outcome, ok := waitForCompilerCleanup(finished)
				if !ok {
					return ErrCompilerCleanup
				}
				if outcome.fatal != nil {
					return outcome.fatal
				}
				return fmt.Errorf("renew verification lease: %w", err)
			}
		case outcome := <-finished:
			if outcome.fatal != nil {
				return outcome.fatal
			}
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
				worker.observe("stale_target")
				return nil
			} else if err != nil {
				return fmt.Errorf("complete verification job: %w", err)
			}
			worker.observe("succeeded")
			return nil
		}
	}
}

type v2CompletionRepository interface {
	CompleteV2(context.Context, VerificationLease, string, json.RawMessage) error
}

func (worker *Worker) processLeaseV2(ctx context.Context, lease VerificationLease) error {
	repository, ok := worker.repository.(v2CompletionRepository)
	if !ok {
		return errors.New("verification v2 completion repository is unavailable")
	}
	request := lease.Job.RequestV2
	if request.Kind == JobSourcify || request.Kind == JobSourcifyFromEtherscan {
		if worker.options.Sourcify == nil {
			return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
		}
		result, err := worker.options.Sourcify.RunV2(ctx, request.Kind, request.SourcifyRequest)
		if err != nil {
			return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
		}
		if !result.Successful {
			return worker.completeOutcomeV2(ctx, repository, lease, "verification_failure", map[string]any{
				"kind": "verification_failure",
			})
		}
		return worker.completeOutcomeV2(ctx, repository, lease, "sourcify_success", map[string]any{
			"kind": "sourcify_success", "verification_id": result.VerificationID,
		})
	}
	var provenance CompilerProvenance
	var err error
	if pinned, ok := worker.compiler.(PinnedCompiler); ok {
		provenance, err = pinned.Resolve(ctx, request.Language, request.CompilerVersion)
	} else {
		provenance, err = worker.compiler.Provenance(request.Language, request.CompilerVersion)
	}
	if err != nil {
		return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
	}
	if provenance.CatalogGeneration > 0 &&
		(request.CompilerPlatform == "" || provenance.Platform != request.CompilerPlatform) {
		return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
	}
	if err := worker.repository.BindCompiler(ctx, lease, provenance); err != nil {
		return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
	}
	var compiledVariants [][]CandidateArtifact
	var sawCompilationFailure bool
	for _, input := range request.StandardJSONVariants {
		modified, err := PerturbVerifierSources(input, worker.options.MaxOutputBytes)
		if err != nil {
			return worker.failLease(ctx, lease, ErrorCompilerOutput)
		}
		first, second, err := worker.compilePairV2(ctx, request, provenance, input, modified)
		if err != nil {
			if errors.Is(err, ErrCompilerCleanup) || errors.Is(err, ErrCompilerRuntime) {
				return err
			}
			// Compiler-process, cache, sandbox, timeout, cancellation, and
			// transport failures are infrastructure failures. Source-level
			// compilation diagnostics arrive as a valid compiler JSON document
			// and are classified by ExtractCandidatesV2 below.
			return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
		}
		candidates, err := ExtractCandidatesV2(
			first, second, request.Language, request.CompilerVersion,
		)
		if err != nil {
			if _, ok := errors.AsType[CompilationFailure](err); ok {
				sawCompilationFailure = true
				continue
			}
			return worker.failLease(ctx, lease, ErrorCompilerOutput)
		}
		compiledVariants = append(compiledVariants, candidates)
	}
	if len(compiledVariants) == 0 {
		if sawCompilationFailure {
			return worker.completeOutcomeV2(ctx, repository, lease, "compilation_failure", map[string]any{
				"kind": "compilation_failure",
			})
		}
		return worker.failLease(ctx, lease, ErrorCompilerOutput)
	}
	results := make([]any, 0, len(request.Bytecodes))
	for _, bytecodes := range request.Bytecodes {
		var selected *CandidateVerification
		for _, candidates := range compiledVariants {
			matches, err := VerifyCandidateArtifacts(
				candidates, bytecodes, request.ContractNameHint, request.Kind == JobAddress,
			)
			if err != nil {
				return worker.failLease(ctx, lease, ErrorMatchFailed)
			}
			if len(matches) > 0 {
				candidate := matches[0]
				selected = &candidate
				break
			}
		}
		if selected == nil {
			results = append(results, map[string]any{"kind": "verification_failure"})
			continue
		}
		results = append(results, verificationSuccessOutcome(*selected, request))
	}
	if request.Kind == JobSolidityBatchMultipart || request.Kind == JobSolidityBatchStandardJSON {
		return worker.completeOutcomeV2(ctx, repository, lease, "batch_results", map[string]any{
			"kind": "batch_results", "results": results,
		})
	}
	if len(results) == 0 {
		return worker.completeOutcomeV2(ctx, repository, lease, "verification_failure", map[string]any{
			"kind": "verification_failure",
		})
	}
	if failure, ok := results[0].(map[string]any); ok && failure["kind"] == "verification_failure" {
		return worker.completeOutcomeV2(ctx, repository, lease, "verification_failure", failure)
	}
	return worker.completeOutcomeV2(ctx, repository, lease, "verification_success", results[0])
}

func (worker *Worker) compilePairV2(
	ctx context.Context,
	request *SubmissionV2,
	provenance CompilerProvenance,
	firstInput, secondInput []byte,
) ([]byte, []byte, error) {
	compile := func(input []byte) ([]byte, error) {
		if pinned, ok := worker.compiler.(PinnedCompiler); ok {
			return pinned.CompilePinned(ctx, request.Language, request.CompilerVersion, provenance, input)
		}
		return worker.compiler.Compile(ctx, request.Language, request.CompilerVersion, input)
	}
	first, err := compile(firstInput)
	if err != nil {
		return nil, nil, err
	}
	second, err := compile(secondInput)
	if err != nil {
		return nil, nil, err
	}
	return first, second, nil
}

func verificationSuccessOutcome(
	result CandidateVerification,
	request *SubmissionV2,
) map[string]any {
	var sources map[string]any
	var settings map[string]any
	var document struct {
		Sources  map[string]any `json:"sources"`
		Settings map[string]any `json:"settings"`
	}
	_ = json.Unmarshal(request.StandardJSON, &document)
	sources, settings = document.Sources, document.Settings
	libraries := make(map[string]string)
	constructor := ""
	if result.Creation != nil {
		maps.Copy(libraries, result.Creation.Values.Libraries)
		constructor = result.Creation.Values.ConstructorArguments
	}
	if result.Runtime != nil {
		maps.Copy(libraries, result.Runtime.Values.Libraries)
	}
	outcome := map[string]any{
		"kind":             "verification_success",
		"file_name":        result.Candidate.FileName,
		"contract_name":    result.Candidate.ContractName,
		"language":         result.Candidate.Language,
		"compiler_version": result.Candidate.CompilerVersion,
		"settings":         settings, "sources": sources,
		"abi":                     json.RawMessage(result.Candidate.ABI),
		"compilation_artifacts":   json.RawMessage(result.Candidate.CompilationArtifacts),
		"creation_code_artifacts": json.RawMessage(result.Candidate.CreationCodeArtifacts),
		"runtime_code_artifacts":  json.RawMessage(result.Candidate.RuntimeCodeArtifacts),
		"creation_match":          result.Creation, "runtime_match": result.Runtime,
		"libraries": libraries, "is_blueprint": result.Blueprint,
	}
	if constructor != "" {
		outcome["constructor_arguments"] = constructor
	}
	return outcome
}

func (worker *Worker) completeOutcomeV2(
	ctx context.Context,
	repository v2CompletionRepository,
	lease VerificationLease,
	kind string,
	value any,
) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := repository.CompleteV2(ctx, lease, kind, encoded); err != nil {
		if errors.Is(err, ErrTargetNotCanonical) {
			worker.observe("stale_target")
			return nil
		}
		return err
	}
	worker.observe("succeeded")
	return nil
}

func waitForCompilerCleanup(finished <-chan compileOutcome) (compileOutcome, bool) {
	timer := time.NewTimer(compilerCancellationCleanupTimeout)
	defer timer.Stop()
	select {
	case outcome := <-finished:
		return outcome, true
	case <-timer.C:
		return compileOutcome{}, false
	}
}

func (worker *Worker) failLease(ctx context.Context, lease VerificationLease, code ErrorCode) error {
	if err := worker.repository.Fail(ctx, lease, code); err != nil {
		return fmt.Errorf("fail verification job: %w", err)
	}
	result := "failed"
	switch code {
	case ErrorCompilerTooLarge:
		result = "resource_exhausted"
	case ErrorCompilerUnavailable:
		result = "unavailable"
	case ErrorTargetNotCanonical:
		result = "stale_target"
	}
	worker.observe(result)
	return nil
}

func (worker *Worker) observe(result string) {
	if worker.options.Observer != nil {
		worker.options.Observer.RecordVerificationJob(result)
	}
}

func buildCompletion(request Request, compilerOutput []byte, maximum int) (Completion, ErrorCode) {
	if len(compilerOutput) == 0 {
		return Completion{}, ErrorCompilerOutput
	}
	if maximum <= 0 || len(compilerOutput) > maximum {
		return Completion{}, ErrorCompilerTooLarge
	}
	artifact, err := ExtractArtifact(
		compilerOutput,
		request.Language,
		request.CompilerVersion,
		request.ContractIdentifier,
	)
	if err != nil {
		return Completion{}, ErrorCompilerOutput
	}
	sources, settings, err := extractSourcesAndSettings(request)
	if err != nil {
		return Completion{}, ErrorCompilerOutput
	}
	match, err := MatchArtifact(request, artifact)
	if err != nil {
		if errors.Is(err, errCompilerOutputMalformed) ||
			errors.Is(err, errCompiledCodeMalformed) ||
			errors.Is(err, errCompilerVersionMalformed) {
			return Completion{}, ErrorCompilerOutput
		}
		return Completion{}, ErrorMatchFailed
	}
	kind := summarizeMatch(match)
	completion := Completion{Kind: kind, Match: match}
	if kind == MatchMismatch {
		return completion, ""
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
