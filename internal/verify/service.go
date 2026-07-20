package verify

import (
	"context"
	"encoding/json"
	"errors"
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
}

type ServiceOptions struct {
	RequiresHardIsolation bool
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
	}, nil
}

func (service *Service) Submit(ctx context.Context, request Request) (VerificationJob, bool, error) {
	if service == nil || service.repository == nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("nil repository")}
	}
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
