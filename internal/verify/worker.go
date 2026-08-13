package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/geascompiler"
)

type WorkerOptions struct {
	ServiceName    string
	WorkerID       string
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	MaxOutputBytes int
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

type verificationAvailabilityObserver interface {
	RecordVerificationCompiler(family string, available bool)
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
		found, err := worker.processOneRunnable(ctx, worker.compilerAvailability(ctx))
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
	return worker.processOneRunnable(ctx, CompilerAvailability{SolcJS: true, Geas: true})
}

type runnableVerificationClaimer interface {
	ClaimRunnable(context.Context, string, time.Duration, CompilerAvailability) (VerificationLease, bool, error)
}

func (worker *Worker) compilerAvailability(ctx context.Context) CompilerAvailability {
	if runtime, ok := worker.compiler.(interface {
		Availability(context.Context) CompilerAvailability
	}); ok {
		return runtime.Availability(ctx)
	}
	if runtime, ok := worker.compiler.(interface {
		CompilerAvailable(context.Context) bool
	}); ok {
		available := runtime.CompilerAvailable(ctx)
		return CompilerAvailability{SolcJS: available, Geas: available}
	}
	runtime, ok := worker.compiler.(interface{ Ready() bool })
	if !ok {
		return CompilerAvailability{SolcJS: true, Geas: true}
	}
	available := runtime.Ready()
	return CompilerAvailability{SolcJS: available, Geas: available}
}

func (worker *Worker) processOneRunnable(
	ctx context.Context,
	availability CompilerAvailability,
) (bool, error) {
	if observer, ok := worker.options.Observer.(verificationAvailabilityObserver); ok {
		observer.RecordVerificationCompiler(string(CompilerFamilySolcJS), availability.SolcJS)
		observer.RecordVerificationCompiler(string(CompilerFamilyGeas), availability.Geas)
	}
	var lease VerificationLease
	var found bool
	var err error
	if repository, ok := worker.repository.(runnableVerificationClaimer); ok {
		lease, found, err = repository.ClaimRunnable(
			ctx, worker.options.WorkerID, worker.options.LeaseDuration, availability,
		)
	} else {
		if !availability.SolcJS && !availability.Geas {
			return false, nil
		}
		lease, found, err = worker.repository.Claim(
			ctx, worker.options.WorkerID, worker.options.LeaseDuration,
		)
	}
	if err != nil || !found {
		return found, err
	}
	err = worker.processLease(ctx, lease)
	if err != nil && ctx.Err() == nil {
		worker.observe("error")
	}
	return true, err
}

func (worker *Worker) processLease(ctx context.Context, lease VerificationLease) error {
	if lease.Job.RequestV2 == nil {
		return errors.New("verification job payload is invalid")
	}
	return worker.processLeaseV2(ctx, lease)
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
	if request.Kind == JobProxy {
		proxyRepository, ok := worker.repository.(interface {
			CompleteProxyV2(context.Context, VerificationLease) error
		})
		if !ok {
			return errors.New("proxy verification completion repository is unavailable")
		}
		if err := proxyRepository.CompleteProxyV2(ctx, lease); err != nil {
			if errors.Is(err, ErrTargetNotCanonical) {
				return worker.failLease(ctx, lease, ErrorTargetNotCanonical)
			}
			return err
		}
		worker.observe("succeeded")
		return nil
	}
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
	provenance, err := worker.resolveCompilerV2(ctx, lease)
	if err != nil {
		if errors.Is(err, ErrCompilerVersionUnavailable) {
			return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
		}
		if transientCompilerError(err) || errors.Is(err, context.Canceled) {
			return err
		}
		return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
	}
	if provenance.CatalogGeneration > 0 &&
		request.CompilerPlatform != "" && provenance.Platform != request.CompilerPlatform {
		return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
	}
	if err := worker.repository.BindCompiler(ctx, lease, provenance); err != nil {
		return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
	}
	if request.Language == LanguageGeas {
		return worker.processGeasV2(ctx, repository, lease, provenance)
	}
	var compiledVariants [][]CandidateArtifact
	var sawCompilationFailure bool
	for _, input := range request.StandardJSONVariants {
		modified, err := PerturbVerifierSources(input, worker.options.MaxOutputBytes)
		if err != nil {
			return worker.failLease(ctx, lease, ErrorCompilerOutput)
		}
		first, second, err := worker.compilePairV2WithRetry(
			ctx, lease, request, provenance, input, modified,
		)
		if err != nil {
			if errors.Is(err, ErrCompilerCleanup) || errors.Is(err, ErrCompilerRuntime) {
				return err
			}
			if transientCompilerError(err) || errors.Is(err, context.Canceled) {
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

type geasEntrypointCompiler interface {
	CompileGeasEntrypointPinned(
		context.Context,
		string,
		CompilerProvenance,
		map[string]string,
		string,
	) (geascompiler.Response, error)
}

func (worker *Worker) processGeasV2(
	ctx context.Context,
	repository v2CompletionRepository,
	lease VerificationLease,
	provenance CompilerProvenance,
) error {
	request := lease.Job.RequestV2
	compiler, ok := worker.compiler.(geasEntrypointCompiler)
	if !ok || request == nil || request.Geas == nil || len(request.Bytecodes) != 1 {
		return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
	}
	compile := func(entrypoint string) (geascompiler.Response, error) {
		return runWithLeaseHeartbeat(
			ctx, worker, lease,
			func(operationContext context.Context) (geascompiler.Response, error) {
				return compiler.CompileGeasEntrypointPinned(
					operationContext, request.CompilerVersion, provenance,
					request.Geas.Sources, entrypoint,
				)
			},
		)
	}
	runtime, err := compile(request.Geas.RuntimeEntrypoint)
	if err != nil {
		return worker.handleGeasCompilerError(ctx, lease, err)
	}
	if !runtime.Successful {
		return worker.completeOutcomeV2(ctx, repository, lease, "compilation_failure", map[string]any{
			"kind": "compilation_failure",
		})
	}
	compiledRuntime, runtimeErr := optionalBytecode(runtime.Bytecode)
	targetRuntime, targetRuntimeErr := optionalBytecode(request.Bytecodes[0].Runtime)
	if runtimeErr != nil || targetRuntimeErr != nil || !bytes.Equal(compiledRuntime, targetRuntime) {
		return worker.completeOutcomeV2(ctx, repository, lease, "verification_failure", map[string]any{
			"kind": "verification_failure",
		})
	}
	usedSources := make(map[string]struct{}, len(runtime.Sources))
	for _, name := range runtime.Sources {
		usedSources[name] = struct{}{}
	}
	fullMatch := &VerificationMatchDetails{
		MatchType: VerificationMatchFull, Transformations: make([]Transformation, 0),
	}
	var creationMatch *VerificationMatchDetails
	if request.Geas.CreationEntrypoint != "" {
		creation, compileErr := compile(request.Geas.CreationEntrypoint)
		if compileErr != nil {
			return worker.handleGeasCompilerError(ctx, lease, compileErr)
		}
		if !creation.Successful {
			return worker.completeOutcomeV2(ctx, repository, lease, "compilation_failure", map[string]any{
				"kind": "compilation_failure",
			})
		}
		compiledCreation, compiledErr := optionalBytecode(creation.Bytecode)
		targetCreation, targetErr := optionalBytecode(request.Bytecodes[0].Creation)
		if compiledErr != nil || targetErr != nil || len(targetCreation) == 0 ||
			!bytes.Equal(compiledCreation, targetCreation) {
			return worker.completeOutcomeV2(ctx, repository, lease, "verification_failure", map[string]any{
				"kind": "verification_failure",
			})
		}
		for _, name := range creation.Sources {
			usedSources[name] = struct{}{}
		}
		creationMatch = fullMatch
	}
	sources := make(map[string]any, len(usedSources))
	for name := range usedSources {
		content, exists := request.Geas.Sources[name]
		if !exists {
			return worker.failLease(ctx, lease, ErrorCompilerOutput)
		}
		sources[name] = map[string]string{"content": content}
	}
	settings := map[string]any{
		"runtime_entrypoint": request.Geas.RuntimeEntrypoint,
		"stack_check":        true,
	}
	if request.Geas.CreationEntrypoint != "" {
		settings["creation_entrypoint"] = request.Geas.CreationEntrypoint
	}
	return worker.completeOutcomeV2(ctx, repository, lease, "verification_success", map[string]any{
		"kind":                    "verification_success",
		"file_name":               request.Geas.RuntimeEntrypoint,
		"contract_name":           request.ContractNameHint,
		"language":                LanguageGeas,
		"compiler_version":        GeasCompilerVersion,
		"settings":                settings,
		"sources":                 sources,
		"abi":                     []any{},
		"compilation_artifacts":   map[string]any{},
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
		"creation_match":          creationMatch,
		"runtime_match":           fullMatch,
		"libraries":               map[string]string{},
		"is_blueprint":            false,
	})
}

func (worker *Worker) handleGeasCompilerError(
	ctx context.Context,
	lease VerificationLease,
	err error,
) error {
	if errors.Is(err, ErrCompilerCleanup) || errors.Is(err, ErrCompilerRuntime) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return err
	}
	if errors.Is(err, ErrCompilerNondeterministic) {
		return worker.failLease(ctx, lease, ErrorCompilerOutput)
	}
	if errors.Is(err, ErrCompilerProvenanceConflict) {
		return worker.failLease(ctx, lease, ErrorCompilerProvenanceMismatch)
	}
	return worker.failLease(ctx, lease, ErrorCompilerUnavailable)
}

func (worker *Worker) compilePairV2(
	ctx context.Context,
	request *SubmissionV2,
	provenance CompilerProvenance,
	firstInput, secondInput []byte,
) ([]byte, []byte, error) {
	if paired, ok := worker.compiler.(PinnedPairCompiler); ok {
		return paired.CompilePairPinned(
			ctx, request.Language, request.CompilerVersion, provenance, firstInput, secondInput,
		)
	}
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

func (worker *Worker) resolveCompilerV2(
	ctx context.Context,
	lease VerificationLease,
) (CompilerProvenance, error) {
	if lease.Job.Compiler != nil {
		return *lease.Job.Compiler, nil
	}
	request := lease.Job.RequestV2
	delay := time.Second
	for {
		provenance, err := runWithLeaseHeartbeat(
			ctx, worker, lease,
			func(operationContext context.Context) (CompilerProvenance, error) {
				if pinned, ok := worker.compiler.(PinnedCompiler); ok {
					return pinned.Resolve(
						operationContext, request.Language, request.CompilerVersion,
					)
				}
				return worker.compiler.Provenance(request.Language, request.CompilerVersion)
			},
		)
		if !transientCompilerError(err) {
			return provenance, err
		}
		if err := worker.waitForCompilerRetry(ctx, lease, delay); err != nil {
			return CompilerProvenance{}, err
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func (worker *Worker) compilePairV2WithRetry(
	ctx context.Context,
	lease VerificationLease,
	request *SubmissionV2,
	provenance CompilerProvenance,
	firstInput, secondInput []byte,
) ([]byte, []byte, error) {
	delay := time.Second
	for {
		type pair struct {
			first  []byte
			second []byte
		}
		outputs, err := runWithLeaseHeartbeat(
			ctx, worker, lease,
			func(operationContext context.Context) (pair, error) {
				first, second, compileErr := worker.compilePairV2(
					operationContext, request, provenance, firstInput, secondInput,
				)
				return pair{first: first, second: second}, compileErr
			},
		)
		if !transientCompilerError(err) {
			return outputs.first, outputs.second, err
		}
		if err := worker.waitForCompilerRetry(ctx, lease, delay); err != nil {
			return nil, nil, err
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func transientCompilerError(err error) bool {
	return errors.Is(err, ErrCompilerCatalogStale) ||
		errors.Is(err, ErrCompilerCatalogUnavailable)
}

func (worker *Worker) waitForCompilerRetry(
	ctx context.Context,
	lease VerificationLease,
	delay time.Duration,
) error {
	_, err := runWithLeaseHeartbeat(
		ctx, worker, lease,
		func(operationContext context.Context) (struct{}, error) {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-operationContext.Done():
				return struct{}{}, operationContext.Err()
			case <-timer.C:
				return struct{}{}, nil
			}
		},
	)
	return err
}

func runWithLeaseHeartbeat[T any](
	ctx context.Context,
	worker *Worker,
	lease VerificationLease,
	operation func(context.Context) (T, error),
) (T, error) {
	var zero T
	operationContext, cancel := context.WithCancel(ctx)
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(worker.options.LeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-operationContext.Done():
				renewed <- nil
				return
			case <-ticker.C:
				if err := worker.repository.Renew(
					ctx, lease, worker.options.LeaseDuration,
				); err != nil {
					cancel()
					renewed <- err
					return
				}
			}
		}
	}()
	value, err := operation(operationContext)
	cancel()
	if renewalErr := <-renewed; renewalErr != nil {
		return zero, renewalErr
	}
	return value, err
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
