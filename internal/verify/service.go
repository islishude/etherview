package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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
	repository    Repository
	maxInputBytes int
}

func NewService(repository Repository, maxInputBytes int) (*Service, error) {
	if repository == nil {
		return nil, errors.New("verification service requires a repository")
	}
	if maxInputBytes <= 0 {
		maxInputBytes = 5 << 20
	}
	return &Service{repository: repository, maxInputBytes: maxInputBytes}, nil
}

type v2SubmissionRepository interface {
	SubmitV2(context.Context, SubmissionV2) (VerificationJob, bool, error)
}

func (service *Service) SubmitV2(ctx context.Context, request SubmissionV2) (VerificationJob, bool, error) {
	repository, ok := service.repository.(v2SubmissionRepository)
	if !ok {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("v2 repository unavailable")}
	}
	if err := service.prepareV2(ctx, &request); err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceInvalidRequest, cause: err}
	}
	job, created, err := repository.SubmitV2(ctx, request)
	if err != nil {
		return VerificationJob{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return job, created, nil
}

func (service *Service) prepareV2(_ context.Context, request *SubmissionV2) error {
	if request == nil {
		return errors.New("verification request is required")
	}
	switch request.Kind {
	case JobSourcify, JobSourcifyFromEtherscan:
		return ValidateSourcifyV2Request(request.Kind, request.SourcifyRequest, service.maxInputBytes)
	case JobProxy:
		return validateProxyVerificationSubmission(request)
	case JobAddress, JobSolidityMultipart, JobSolidityStandardJSON,
		JobSolidityBatchMultipart, JobSolidityBatchStandardJSON:
	default:
		return errors.New("verification job kind is invalid")
	}
	if request.Language != LanguageSolidity && request.Language != LanguageYul &&
		request.Language != LanguageGeas {
		return errors.New("verification language is invalid")
	}
	if !versionPattern.MatchString(normalizeCompilerVersion(request.CompilerVersion)) {
		return errors.New("compiler version is invalid")
	}
	request.CompilerVersion = normalizeCompilerVersion(request.CompilerVersion)
	if request.Language == LanguageGeas {
		if request.Kind != JobAddress || request.Multipart != nil || len(request.StandardJSON) != 0 ||
			len(request.StandardJSONVariants) != 0 || request.CompilerVersion != GeasCompilerVersion {
			return errors.New("geas verification request is invalid")
		}
		if err := prepareGeasRequest(request.Geas, &request.ContractNameHint, service.maxInputBytes); err != nil {
			return err
		}
	} else {
		if request.Geas != nil {
			return errors.New("solidity verification request contains geas input")
		}
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
	}
	normalizeSolidityAnalysisVersion(request)
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
		if !validAddressVerificationSubmission(request) {
			return errors.New("address verification target is invalid")
		}
	}
	request.CatalogGenerationID = 0
	request.CompilerPlatform = ""
	request.CompilerDigest = ""
	request.ExecutorKind = ""
	request.ExecutionPolicy = ""
	request.ExecutorDigest = ""
	return nil
}

func validAddressVerificationSubmission(request *SubmissionV2) bool {
	if request == nil || request.Target == nil || request.Target.ChainID == 0 ||
		!fixedHex(request.Target.Address, 20) || !fixedHex(request.Target.CodeHash, 32) ||
		!fixedHex(request.Target.AtBlockHash, 32) || len(request.Bytecodes) != 1 {
		return false
	}
	pairCreation, pairCreationErr := optionalBytecode(request.Bytecodes[0].Creation)
	pairRuntime, pairRuntimeErr := optionalBytecode(request.Bytecodes[0].Runtime)
	targetCreation, targetCreationErr := optionalBytecode(request.Target.CreationBytecode)
	targetRuntime, targetRuntimeErr := optionalBytecode(request.Target.RuntimeBytecode)
	codeHash, codeHashErr := decodeBytecode(request.Target.CodeHash)
	if pairCreationErr != nil || pairRuntimeErr != nil || targetCreationErr != nil ||
		targetRuntimeErr != nil || codeHashErr != nil || len(pairRuntime) == 0 ||
		!bytes.Equal(pairRuntime, targetRuntime) ||
		!bytes.Equal(keccak256Bytes(targetRuntime), codeHash) {
		return false
	}
	if request.Target.GenesisPredeploy {
		return len(pairCreation) == 0 && len(targetCreation) == 0
	}
	return len(pairCreation) > 0 && bytes.Equal(pairCreation, targetCreation)
}

func validateProxyVerificationSubmission(request *SubmissionV2) error {
	if request.Target == nil || request.Target.ChainID == 0 ||
		request.Target.GenesisPredeploy ||
		!fixedHex(request.Target.Address, 20) ||
		!fixedHex(request.Target.CodeHash, 32) ||
		!fixedHex(request.Target.AtBlockHash, 32) ||
		request.ProxyTarget == nil ||
		!validCanonicalProxyBlockNumber(request.ProxyTarget.SubmissionContextBlockNumber) ||
		!fixedHex(request.ProxyTarget.SubmissionContextBlockHash, 32) ||
		!nonZeroFixedHex(request.ProxyTarget.ImplementationAddress, 20) ||
		!nonZeroFixedHex(request.ProxyTarget.ImplementationCodeHash, 32) ||
		!validOptionalProxyIdentity(
			request.ProxyTarget.AdminAddress,
			request.ProxyTarget.AdminCodeHash,
		) || !validOptionalProxyIdentity(
		request.ProxyTarget.BeaconAddress,
		request.ProxyTarget.BeaconCodeHash,
	) || !validOptionalProxyIdentity(
		request.ProxyTarget.ManagementAddress,
		request.ProxyTarget.ManagementCodeHash,
	) {
		return errors.New("proxy verification target is invalid")
	}
	target := request.ProxyTarget
	if !validPositiveProxyGeneration(target.ObservationGenerationID) ||
		(target.Pattern == "clone") != (target.ArtifactResolutionID == "") ||
		(target.ArtifactResolutionID != "" && !validPositiveProxyGeneration(target.ArtifactResolutionID)) ||
		(target.Pattern == "beacon") != (target.BeaconGenerationID != "") ||
		(target.BeaconGenerationID != "" && !validPositiveProxyGeneration(target.BeaconGenerationID)) ||
		(target.Pattern == "uups") != (target.UUPSGenerationID != "") ||
		(target.UUPSGenerationID != "" && !validPositiveProxyGeneration(target.UUPSGenerationID)) {
		return errors.New("proxy verification generation identity is invalid")
	}
	if target.StandardVersion != "" && target.StandardVersion != openZeppelin561Version {
		return errors.New("proxy verification standard version is invalid")
	}
	switch target.Kind {
	case "eip1167", "cwia":
		if target.Pattern != "clone" {
			return errors.New("proxy verification kind and pattern disagree")
		}
	case "eip1967":
		if target.Pattern != "erc1967" && target.Pattern != "transparent" &&
			target.Pattern != "uups" {
			return errors.New("proxy verification kind and pattern disagree")
		}
	case "beacon":
		if target.Pattern != "beacon" {
			return errors.New("proxy verification kind and pattern disagree")
		}
	default:
		return errors.New("proxy verification kind is invalid")
	}
	if err := validateProxyManagementIdentity(target); err != nil {
		return err
	}
	request.Target.Address = strings.ToLower(request.Target.Address)
	request.Target.CodeHash = strings.ToLower(request.Target.CodeHash)
	request.Target.AtBlockHash = strings.ToLower(request.Target.AtBlockHash)
	target.ImplementationAddress = strings.ToLower(target.ImplementationAddress)
	target.ImplementationCodeHash = strings.ToLower(target.ImplementationCodeHash)
	target.AdminAddress = strings.ToLower(target.AdminAddress)
	target.AdminCodeHash = strings.ToLower(target.AdminCodeHash)
	target.BeaconAddress = strings.ToLower(target.BeaconAddress)
	target.BeaconCodeHash = strings.ToLower(target.BeaconCodeHash)
	target.ManagementAddress = strings.ToLower(target.ManagementAddress)
	target.ManagementCodeHash = strings.ToLower(target.ManagementCodeHash)
	target.SubmissionContextBlockHash = strings.ToLower(target.SubmissionContextBlockHash)
	target.ExpectedImplementation = target.ImplementationAddress
	request.Language = ""
	request.CompilerVersion = ""
	request.StandardJSON = nil
	request.StandardJSONVariants = nil
	request.Multipart = nil
	request.Bytecodes = nil
	request.ContractNameHint = ""
	request.SourcifyRequest = nil
	request.CatalogGenerationID = 0
	request.CompilerPlatform = ""
	request.CompilerDigest = ""
	request.ExecutorKind = ""
	request.ExecutionPolicy = ""
	request.ExecutorDigest = ""
	return nil
}

func validCanonicalProxyBlockNumber(value string) bool {
	if value == "" || len(value) > 78 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validPositiveProxyGeneration(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func validateProxyManagementIdentity(target *ProxyVerificationTarget) error {
	if target == nil {
		return errors.New("proxy verification target is invalid")
	}
	adminPresent := target.AdminAddress != ""
	beaconPresent := target.BeaconAddress != ""
	managementPresent := target.ManagementAddress != ""
	switch target.Pattern {
	case "transparent":
		if target.StandardVersion != openZeppelin561Version || !adminPresent || beaconPresent ||
			target.ManagementKind != "proxy_admin" || !managementPresent ||
			!strings.EqualFold(target.AdminAddress, target.ManagementAddress) ||
			!strings.EqualFold(target.AdminCodeHash, target.ManagementCodeHash) {
			return errors.New("transparent proxy management identity is invalid")
		}
	case "uups":
		if target.StandardVersion != openZeppelin561Version || adminPresent || beaconPresent ||
			target.ManagementKind != "none" || managementPresent {
			return errors.New("UUPS proxy management identity is invalid")
		}
	case "beacon":
		if target.StandardVersion != openZeppelin561Version || adminPresent || !beaconPresent ||
			target.ManagementKind != "upgradeable_beacon" || !managementPresent ||
			!strings.EqualFold(target.BeaconAddress, target.ManagementAddress) ||
			!strings.EqualFold(target.BeaconCodeHash, target.ManagementCodeHash) {
			return errors.New("beacon proxy management identity is invalid")
		}
	case "clone":
		if target.StandardVersion != "" || adminPresent || beaconPresent || target.ManagementKind != "none" || managementPresent {
			return errors.New("clone management identity is invalid")
		}
	case "erc1967":
		if target.StandardVersion != openZeppelin561Version || adminPresent || beaconPresent ||
			target.ManagementKind != "none" || managementPresent {
			return errors.New("ERC-1967 proxy management identity is invalid")
		}
	default:
		return errors.New("proxy verification pattern is invalid")
	}
	return nil
}

func validOptionalProxyIdentity(address, codeHash string) bool {
	if address == "" || codeHash == "" {
		return address == "" && codeHash == ""
	}
	return nonZeroFixedHex(address, 20) && nonZeroFixedHex(codeHash, 32)
}

func nonZeroFixedHex(value string, size int) bool {
	if !fixedHex(value, size) {
		return false
	}
	for _, character := range value[2:] {
		if character != '0' {
			return true
		}
	}
	return false
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

func (service *Service) VerifiedContract(ctx context.Context, chainID uint64, address string) (VerifiedContract, bool, error) {
	if service == nil || service.repository == nil {
		return VerifiedContract{}, false, ServiceError{Code: ServiceStorageFailure, cause: errors.New("nil repository")}
	}
	if chainID == 0 || !fixedHex(address, 20) {
		return VerifiedContract{}, false, ServiceError{Code: ServiceInvalidRequest, cause: errors.New("invalid contract identity")}
	}
	contract, found, err := service.repository.VerifiedContract(ctx, chainID, address)
	if err != nil {
		return VerifiedContract{}, false, ServiceError{Code: ServiceStorageFailure, cause: err}
	}
	return contract, found, nil
}
