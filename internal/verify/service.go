package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type ServiceErrorCode string

const (
	ServiceInvalidRequest ServiceErrorCode = "invalid_request"
	ServiceStorageFailure ServiceErrorCode = "storage_unavailable"
)

// ServiceError exposes a stable, non-sensitive boundary message while keeping
// the cause available to trusted callers for structured logging.
type ServiceError struct {
	Code  ServiceErrorCode
	cause error
}

func (err ServiceError) Error() string {
	switch err.Code {
	case ServiceInvalidRequest:
		return "invalid verification request"
	default:
		return "verification storage unavailable"
	}
}

func (err ServiceError) Unwrap() error { return err.cause }

type Service struct {
	repository            Repository
	maxInputBytes         int
	requiresHardIsolation bool
	catalog               *CompilerCatalog
	runnerImage           string
}

type ServiceOptions struct {
	RequiresHardIsolation bool
	Catalog               *CompilerCatalog
	RunnerImage           string
}

func NewService(repository Repository, maxInputBytes int, optionSets ...ServiceOptions) (*Service, error) {
	if repository == nil {
		return nil, errors.New("verification service requires a repository")
	}
	if len(optionSets) > 1 {
		return nil, errors.New("verification service accepts at most one option set")
	}
	var options ServiceOptions
	if len(optionSets) == 1 {
		options = optionSets[0]
	}
	if maxInputBytes <= 0 {
		maxInputBytes = 5 << 20
	}
	return &Service{
		repository: repository, maxInputBytes: maxInputBytes,
		requiresHardIsolation: options.RequiresHardIsolation,
		catalog:               options.Catalog, runnerImage: options.RunnerImage,
	}, nil
}

type v2SubmissionRepository interface {
	SubmitV2(context.Context, SubmissionV2, bool) (VerificationJob, bool, error)
}

func (service *Service) SubmitV2(ctx context.Context, request SubmissionV2) (VerificationJob, bool, error) {
	repository, ok := service.repository.(v2SubmissionRepository)
	if !ok {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("v2 repository unavailable")}
	}
	if err := service.prepareV2(ctx, &request); err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: err}
	}
	job, created, err := repository.SubmitV2(ctx, request, service.requiresHardIsolation)
	if err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return job, created, nil
}

func (service *Service) prepareV2(ctx context.Context, request *SubmissionV2) error {
	if request == nil {
		return errors.New("verification request is required")
	}
	switch request.Kind {
	case JobSourcify, JobSourcifyFromEtherscan:
		return ValidateSourcifyV2Request(request.Kind, request.SourcifyRequest, service.maxInputBytes)
	case JobAddress, JobSolidityMultipart, JobSolidityStandardJSON,
		JobSolidityBatchMultipart, JobSolidityBatchStandardJSON,
		JobVyperMultipart, JobVyperStandardJSON:
	default:
		return errors.New("verification job kind is invalid")
	}
	if request.Language != LanguageSolidity && request.Language != LanguageYul && request.Language != LanguageVyper {
		return errors.New("verification language is invalid")
	}
	if !versionPattern.MatchString(normalizeCompilerVersion(request.CompilerVersion)) {
		return errors.New("compiler version is invalid")
	}
	request.CompilerVersion = normalizeCompilerVersion(request.CompilerVersion)
	if request.Multipart != nil {
		variants, err := BuildMultipartStandardJSON(*request.Multipart, request.CompilerVersion, service.maxInputBytes)
		if err != nil {
			return err
		}
		request.StandardJSONVariants = variants
		request.StandardJSON = variants[0]
	} else {
		prepared, err := PrepareVerifierStandardJSON(
			request.StandardJSON, request.Language, request.CompilerVersion, service.maxInputBytes,
		)
		if err != nil {
			return err
		}
		request.StandardJSON = prepared
		request.StandardJSONVariants = []json.RawMessage{prepared}
	}
	maximumPairs := 1
	if request.Kind == JobSolidityBatchMultipart || request.Kind == JobSolidityBatchStandardJSON {
		maximumPairs = 100
	}
	if len(request.Bytecodes) == 0 || len(request.Bytecodes) > maximumPairs {
		return errors.New("verification bytecode count is invalid")
	}
	for _, pair := range request.Bytecodes {
		creation, creationErr := optionalBytecode(pair.Creation)
		runtime, runtimeErr := optionalBytecode(pair.Runtime)
		if creationErr != nil || runtimeErr != nil || len(creation)+len(runtime) == 0 {
			return errors.New("verification bytecode is invalid")
		}
	}
	if request.Kind == JobAddress {
		if request.Target == nil || request.Target.ChainID == 0 ||
			!fixedHex(request.Target.Address, 20) || !fixedHex(request.Target.CodeHash, 32) ||
			!fixedHex(request.Target.AtBlockHash, 32) || request.Bytecodes[0].Runtime == "" {
			return errors.New("address verification target is invalid")
		}
	}
	if service.catalog == nil {
		return errors.New("compiler catalog is unavailable")
	}
	entry, err := service.catalog.Lookup(ctx, request.Language, request.CompilerVersion)
	if err != nil {
		return err
	}
	request.CatalogGenerationID = entry.GenerationID
	request.CompilerPlatform = entry.Platform
	request.CompilerDigest = fmt.Sprintf("%x", entry.ArtifactSHA256)
	if service.requiresHardIsolation {
		digest, err := parseContainerImage(service.runnerImage)
		if err != nil {
			return err
		}
		request.RunnerDigest = fmt.Sprintf("%x", digest)
	}
	return nil
}

func (service *Service) Submit(ctx context.Context, request Request) (VerificationJob, bool, error) {
	if service == nil || service.repository == nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("nil repository")}
	}
	standardJSON, err := PrepareStandardJSON(
		request.StandardJSON,
		request.Language,
		request.CompilerVersion,
		request.ContractIdentifier,
		service.maxInputBytes,
	)
	if err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: err}
	}
	request.StandardJSON = standardJSON
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > service.maxInputBytes {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: errors.New("encoded request exceeds limit")}
	}
	if err := request.Validate(service.maxInputBytes); err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: err}
	}
	job, created, err := service.repository.Submit(ctx, request, SubmissionOptions{
		RequiresHardIsolation: service.requiresHardIsolation,
	})
	if err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return job, created, nil
}

func (service *Service) Job(ctx context.Context, id string) (VerificationJob, bool, error) {
	if service == nil || service.repository == nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("nil repository")}
	}
	if !validUUID(id) {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: errors.New("invalid job ID")}
	}
	job, found, err := service.repository.Job(ctx, id)
	if err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return job, found, nil
}

func (service *Service) VerifiedContract(ctx context.Context, chainID uint64, address, codeHash string) (VerifiedContract, bool, error) {
	if service == nil || service.repository == nil {
		return VerifiedContract{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("nil repository")}
	}
	if chainID == 0 || !fixedHex(address, 20) || !fixedHex(codeHash, 32) {
		return VerifiedContract{}, false, ServiceError{Code: ServiceInvalidRequest, cause: errors.New("invalid contract identity")}
	}
	contract, found, err := service.repository.VerifiedContract(ctx, chainID, address, codeHash)
	if err != nil {
		return VerifiedContract{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return contract, found, nil
}
