package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/verify"
)

func (h *Handler) submitAddressVerification(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
		return
	}
	var submission verifierSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "address verification request is invalid") {
		return
	}
	if submission.Bytecodes != nil || submission.Contracts != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
		return
	}
	target, err := h.verificationTargets.ResolveVerificationTarget(r.Context(), address)
	if err != nil || target.ChainID != h.cfg.Chain.ID || !strings.EqualFold(target.Address, address) {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	request := verify.SubmissionV2{
		Kind: verify.JobAddress, Language: submission.Language,
		CompilerVersion: submission.CompilerVersion, ContractNameHint: submission.ContractNameHint,
		Target: &target, Bytecodes: []verify.BytecodePair{{
			Creation: target.CreationBytecode, Runtime: target.RuntimeBytecode,
		}},
	}
	switch submission.InputKind {
	case "standard_json":
		if submission.Language == verify.LanguageGeas || len(submission.Sources) != 0 ||
			submission.RuntimeEntrypoint != "" || submission.CreationEntrypoint != "" {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.StandardJSON = submission.Input
	case "multipart":
		if submission.Language == verify.LanguageGeas || len(submission.Input) != 0 ||
			submission.RuntimeEntrypoint != "" || submission.CreationEntrypoint != "" {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.Multipart = &verify.MultipartRequest{
			Language: submission.Language, Sources: submission.Sources,
			EVMVersion: submission.EVMVersion, OptimizationRuns: submission.OptimizationRuns,
			Libraries: submission.Libraries,
		}
	case "geas_sources":
		if submission.Language != verify.LanguageGeas || len(submission.Input) != 0 ||
			submission.EVMVersion != "" || submission.OptimizationRuns != nil ||
			len(submission.Libraries) != 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "address verification request is invalid", nil)
			return
		}
		request.Geas = &verify.GeasRequest{
			Sources: submission.Sources, RuntimeEntrypoint: submission.RuntimeEntrypoint,
			CreationEntrypoint: submission.CreationEntrypoint,
		}
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "input_kind must be multipart, standard_json, or geas_sources", nil)
		return
	}
	h.submitV2(w, r, request)
}

func (h *Handler) submitVerifier(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	if r.Pattern == "POST /api/v1/verifier/sourcify" {
		var submission struct {
			ChainID string            `json:"chain_id"`
			Address string            `json:"address"`
			Files   map[string]string `json:"files"`
		}
		if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "Sourcify request is invalid") {
			return
		}
		encoded, _ := json.Marshal(submission)
		h.submitV2(w, r, verify.SubmissionV2{
			Kind: verify.JobSourcify, SourcifyRequest: encoded,
		})
		return
	}
	if r.Pattern == "POST /api/v1/verifier/sourcify/from-etherscan" {
		var submission struct {
			ChainID string `json:"chain_id"`
			Address string `json:"address"`
		}
		if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "Sourcify Etherscan request is invalid") {
			return
		}
		encoded, _ := json.Marshal(submission)
		h.submitV2(w, r, verify.SubmissionV2{
			Kind: verify.JobSourcifyFromEtherscan, SourcifyRequest: encoded,
		})
		return
	}
	var submission verifierSubmission
	if !h.decodeBoundedJSON(w, r, &submission, "invalid_verification_request", "verifier request is invalid") {
		return
	}
	request := verify.SubmissionV2{
		CompilerVersion:  submission.CompilerVersion,
		ContractNameHint: submission.ContractNameHint,
	}
	var bytecodes verify.BytecodePair
	if submission.Bytecodes != nil {
		bytecodes = *submission.Bytecodes
	}
	switch r.Pattern {
	case "POST /api/v1/verifier/solidity/multipart":
		request.Kind, request.Language = verify.JobSolidityMultipart, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.Multipart = multipartSubmission(request.Language, submission)
		request.Bytecodes = []verify.BytecodePair{bytecodes}
	case "POST /api/v1/verifier/solidity/standard-json":
		request.Kind, request.Language = verify.JobSolidityStandardJSON, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.StandardJSON = submission.Input
		request.Bytecodes = []verify.BytecodePair{bytecodes}
	case "POST /api/v1/verifier/solidity/batch/multipart":
		request.Kind, request.Language = verify.JobSolidityBatchMultipart, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.Multipart = multipartSubmission(request.Language, submission)
		request.Bytecodes = submission.Contracts
	case "POST /api/v1/verifier/solidity/batch/standard-json":
		request.Kind, request.Language = verify.JobSolidityBatchStandardJSON, submission.Language
		if request.Language == "" {
			request.Language = verify.LanguageSolidity
		}
		request.StandardJSON = submission.Input
		request.Bytecodes = submission.Contracts
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "verifier route not found", nil)
		return
	}
	h.submitV2(w, r, request)
}

func multipartSubmission(language verify.Language, submission verifierSubmission) *verify.MultipartRequest {
	return &verify.MultipartRequest{
		Language: language, Sources: submission.Sources, EVMVersion: submission.EVMVersion,
		OptimizationRuns: submission.OptimizationRuns, Libraries: submission.Libraries,
	}
}

func (h *Handler) submitV2(w http.ResponseWriter, r *http.Request, request verify.SubmissionV2) {
	job, _, err := h.verificationSubmitter.SubmitV2(r.Context(), request)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) verifierCompilers(w http.ResponseWriter, r *http.Request) {
	language := verify.Language(r.URL.Query().Get("language"))
	if language != verify.LanguageSolidity && language != verify.LanguageYul &&
		language != verify.LanguageGeas {
		writeError(w, r, http.StatusBadRequest, "invalid_language", "language must be solidity, yul, or geas", nil)
		return
	}
	if language == verify.LanguageGeas {
		writeJSON(w, http.StatusOK, gen.CompilerCatalogResponse{
			Data: gen.CompilerCatalog{
				Language: gen.VerifierLanguage(language),
				Versions: []string{verify.GeasCompilerVersion},
			},
			Meta: h.meta(r),
		})
		return
	}
	if h.compilerCatalog == nil {
		writeError(w, r, http.StatusServiceUnavailable, "compiler_catalog_unavailable", "compiler catalog is unavailable", nil)
		return
	}
	versions, err := h.compilerCatalog.Versions(r.Context(), language)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "compiler_catalog_unavailable", "compiler catalog is unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.CompilerCatalogResponse{
		Data: gen.CompilerCatalog{Language: gen.VerifierLanguage(language), Versions: versions},
		Meta: h.meta(r),
	})
}

func (h *Handler) lookupVerifierMethods(w http.ResponseWriter, r *http.Request) {
	var request verify.MethodLookupRequest
	if !h.decodeBoundedJSON(w, r, &request, "invalid_method_lookup", "method lookup request is invalid") {
		return
	}
	methods, err := verify.LookupMethods(request)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_method_lookup", "method lookup request is invalid", nil)
		return
	}
	models := make([]gen.MethodSource, 0, len(methods))
	for _, method := range methods {
		models = append(models, gen.MethodSource{
			Selector: method.Selector, Signature: method.Signature, FileName: method.FileName,
			Offset: method.Offset, Length: method.Length,
		})
	}
	writeJSON(w, http.StatusOK, gen.LookupMethodsResponse{
		Data: gen.LookupMethods{Methods: models}, Meta: h.meta(r),
	})
}

func (h *Handler) verificationJob(w http.ResponseWriter, r *http.Request) {
	if !h.verificationReadAvailable(w, r) {
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	job, found, err := h.verificationReader.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "verification job not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.VerificationJobResponse{Data: verificationJobModel(job), Meta: h.meta(r)})
}

func (h *Handler) verifiedContract(w http.ResponseWriter, r *http.Request) {
	if _, ok := parseExactQuery(w, r); !ok {
		return
	}
	if !h.verificationReadAvailable(w, r) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_contract_identity", "address must be a fixed-size hexadecimal value", nil)
		return
	}
	contract, found, err := h.verificationReader.VerifiedContract(
		r.Context(), h.cfg.Chain.ID, address,
	)
	if err != nil {
		h.handleVerificationError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "verified contract not found", nil)
		return
	}
	model, err := verifiedContractModel(contract)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "decode verified contract", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "verified contract is invalid", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.VerifiedContractResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) contractProxy(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	if _, ok := parseExactQuery(w, r); !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "proxy details are unavailable", nil)
		return
	}
	detail, err := h.proxyReader.Proxy(r.Context(), address)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ProxyDetailsResponse{Data: detail, Meta: h.meta(r)})
}

func (h *Handler) contractProxyUpgrades(w http.ResponseWriter, r *http.Request) {
	h.contractProxyHistory(w, r, true)
}

func (h *Handler) contractProxyInitializations(w http.ResponseWriter, r *http.Request) {
	h.contractProxyHistory(w, r, false)
}

func (h *Handler) contractDiamondCuts(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	values, ok := parseExactQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	cursor := ""
	if items, present := values["cursor"]; present {
		cursor = items[0]
	}
	if len(cursor) > maximumOpaqueCursorLength || (values.Has("cursor") && cursor == "") {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or too long", nil)
		return
	}
	limit, ok := parseExactLimit(w, r, values, 20)
	if !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "Diamond history is unavailable", nil)
		return
	}
	page, next, err := h.proxyReader.DiamondCuts(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.DiamondCutHistoryResponse{
		Data: page, Meta: pageMeta(h.meta(r), next),
	})
}

func (h *Handler) contractProxyHistory(w http.ResponseWriter, r *http.Request, upgrades bool) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	values, ok := parseExactQuery(w, r, "cursor", "limit")
	if !ok {
		return
	}
	cursor := ""
	if items, present := values["cursor"]; present {
		cursor = items[0]
	}
	if len(cursor) > maximumOpaqueCursorLength || (values.Has("cursor") && cursor == "") {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or too long", nil)
		return
	}
	limit, ok := parseExactLimit(w, r, values, 20)
	if !ok {
		return
	}
	if h.proxyReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "proxy_unavailable", "proxy history is unavailable", nil)
		return
	}
	if upgrades {
		page, next, err := h.proxyReader.ProxyUpgrades(r.Context(), address, cursor, limit)
		if err != nil {
			h.handleReaderError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, gen.ProxyUpgradeHistoryResponse{
			Data: page, Meta: pageMeta(h.meta(r), next),
		})
		return
	}
	page, next, err := h.proxyReader.ProxyInitializations(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ProxyInitializationHistoryResponse{
		Data: page, Meta: pageMeta(h.meta(r), next),
	})
}

func pageMeta(meta gen.Meta, next string) gen.Meta {
	if next != "" {
		meta.NextCursor = &next
	}
	return meta
}

func parseExactQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	if len(r.URL.RawQuery) > maximumNativeQueryBytes {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
		return nil, false
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
		return nil, false
	}
	allowlist := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowlist[name] = struct{}{}
	}
	for name, items := range values {
		if _, exists := allowlist[name]; !exists || name == "" || len(items) != 1 {
			writeError(w, r, http.StatusBadRequest, "invalid_query", "query parameters are invalid", nil)
			return nil, false
		}
	}
	return values, true
}

func parseExactLimit(w http.ResponseWriter, r *http.Request, values url.Values, defaultValue int) (int, bool) {
	items, present := values["limit"]
	if !present {
		return defaultValue, true
	}
	raw := items[0]
	value, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(value) != raw || value < 1 || value > maximumPageSize {
		writeError(w, r, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maximumPageSize), nil)
		return 0, false
	}
	return value, true
}

func (h *Handler) verificationReadAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.verificationReader != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification is unavailable", nil)
	return false
}

func (h *Handler) decodeBoundedJSON(w http.ResponseWriter, r *http.Request, destination any, code, message string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxVerificationBody)
	body, err := io.ReadAll(r.Body)
	if err != nil || verify.ValidateUniqueJSON(body) != nil {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, code, message, nil)
		return false
	}
	return true
}

func (h *Handler) handleVerificationTargetError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "resolve verification target", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
	writeError(w, r, http.StatusServiceUnavailable, "verification_target_unavailable", "canonical contract code or creation facts are unavailable", nil)
}

func (h *Handler) requireAPIKey(w http.ResponseWriter, r *http.Request, scope auth.Scope) bool {
	identity := auth.IdentityFrom(r.Context())
	if identity.Authenticated {
		if identity.HasScope(scope) {
			return true
		}
		writeError(w, r, http.StatusForbidden, "api_key_scope_required", "API key scope does not authorize this operation", nil)
		return false
	}
	if operation, ok := billing.PaidOperationFrom(r.Context()); ok {
		spec, exists := apiops.Lookup(operation)
		if exists && spec.BillingEligible && spec.MuxPattern == r.Pattern {
			return true
		}
	}
	writeError(w, r, http.StatusUnauthorized, "api_key_required", "an API key is required", nil)
	return false
}

func requiredAPIScope(operation string) auth.Scope {
	switch operation {
	case "getVerifierJob", "getVerifiedContract", "submitAddressVerification",
		"verifySolidityMultipart", "verifySolidityStandardJson",
		"batchVerifySolidityMultipart", "batchVerifySolidityStandardJson",
		"listVerifierCompilers", "lookupVerifierMethods",
		"submitSourcifyVerification", "submitSourcifyFromEtherscan":
		return auth.ScopeVerification
	default:
		return auth.ScopeRead
	}
}

func operationUsesAPIKeyScope(operation string) bool {
	switch operation {
	case "createAuthChallenge", "verifyAuthChallenge", "getAuthSession",
		"logoutAuthSession", "updateCurrentUser", "listCurrentUserAPIKeys",
		"createCurrentUserAPIKey", "rotateCurrentUserAPIKey",
		"revokeCurrentUserAPIKey", "listAdminUsers", "updateAdminUser",
		"revokeAdminUserSessions", "getBillingConfig",
		"listCurrentUserBillingPayments", "listAdminBillingPayments",
		"getAdminBillingSummary":
		return false
	default:
		return true
	}
}

func (h *Handler) handleVerificationError(w http.ResponseWriter, r *http.Request, err error) {
	var serviceError verify.ServiceError
	if errors.As(err, &serviceError) && serviceError.Code == verify.ServiceInvalidRequest {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", serviceError.Error(), nil)
		return
	}
	h.logger.ErrorContext(r.Context(), "verification request failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
	writeError(w, r, http.StatusInternalServerError, "verification_failed", "verification service failed", nil)
}

func verificationJobModel(job verify.VerificationJob) gen.VerificationJob {
	id, _ := uuid.Parse(job.ID)
	kind := gen.VerificationJobKind(job.Kind)
	if job.Kind == "" {
		kind = gen.VerificationJobKindAddress
	}
	model := gen.VerificationJob{
		Id: id, Kind: kind, Status: gen.VerificationJobStatus(job.Status),
		CreatedAt: job.CreatedAt.UTC(), UpdatedAt: job.UpdatedAt.UTC(),
	}
	if len(job.Outcome) > 0 {
		var outcome gen.VerificationOutcome
		if json.Unmarshal(job.Outcome, &outcome) == nil {
			model.Outcome = &outcome
		}
	}
	if job.ErrorCode != "" {
		value := string(job.ErrorCode)
		model.ErrorCode = &value
	}
	return model
}

func verifiedContractModel(contract verify.VerifiedContract) (gen.VerifiedContract, error) {
	var abi []map[string]any
	var sources, settings, compilation, creationArtifacts, runtimeArtifacts map[string]any
	targetAddress, err := checksumAddress(contract.Target.Address)
	if err != nil {
		return gen.VerifiedContract{}, fmt.Errorf("checksum artifact target address: %w", err)
	}
	sourceAddress, err := checksumAddress(contract.Source.Address)
	if err != nil {
		return gen.VerifiedContract{}, fmt.Errorf("checksum artifact source address: %w", err)
	}
	if err := json.Unmarshal(contract.ABI, &abi); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.Sources, &sources); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.Settings, &settings); err != nil {
		return gen.VerifiedContract{}, err
	}
	if len(contract.CompilationArtifacts) == 0 {
		contract.CompilationArtifacts = json.RawMessage(`{}`)
	}
	if len(contract.CreationCodeArtifacts) == 0 {
		contract.CreationCodeArtifacts = json.RawMessage(`{}`)
	}
	if len(contract.RuntimeCodeArtifacts) == 0 {
		contract.RuntimeCodeArtifacts = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(contract.CompilationArtifacts, &compilation); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.CreationCodeArtifacts, &creationArtifacts); err != nil {
		return gen.VerifiedContract{}, err
	}
	if err := json.Unmarshal(contract.RuntimeCodeArtifacts, &runtimeArtifacts); err != nil {
		return gen.VerifiedContract{}, err
	}
	fileName := contract.FileName
	if fileName == "" {
		fileName = "unknown"
	}
	model := gen.VerifiedContract{
		Resolution: gen.VerifiedContractResolution(contract.Resolution),
		Target: gen.ContractArtifactTarget{
			ChainId: strconv.FormatUint(contract.Target.ChainID, 10),
			Address: targetAddress, CodeHash: contract.Target.CodeHash,
			BlockNumber: strconv.FormatUint(contract.Target.BlockNumber, 10),
			BlockHash:   contract.Target.BlockHash,
		},
		Source: gen.ContractArtifactSource{
			Address: sourceAddress, CodeHash: contract.Source.CodeHash,
			ValidFromBlock: strconv.FormatUint(contract.Source.ValidFromBlock, 10),
			CreatedAt:      contract.Source.CreatedAt.UTC(),
		},
		Kind: gen.VerifiedContractKindVerificationSuccess, Language: gen.VerifierLanguage(contract.Language),
		CompilerVersion: contract.CompilerVersion, FileName: fileName,
		ContractName: contract.ContractName, Abi: &abi, Sources: sources, Settings: settings,
		CompilationArtifacts: compilation, CreationCodeArtifacts: creationArtifacts,
		RuntimeCodeArtifacts: runtimeArtifacts, CreationMatch: matchDetailsModel(contract.CreationMatch),
		RuntimeMatch: matchDetailsModel(contract.RuntimeMatch), Libraries: contract.Libraries,
		IsBlueprint: contract.IsBlueprint,
	}
	if model.Libraries == nil {
		model.Libraries = map[string]string{}
	}
	if contract.ConstructorArguments != "" {
		value := contract.ConstructorArguments
		model.ConstructorArguments = &value
	}
	if contract.Source.ValidToBlock != nil {
		value := strconv.FormatUint(*contract.Source.ValidToBlock, 10)
		model.Source.ValidToBlock = &value
	}
	return model, nil
}

func matchDetailsModel(details *verify.VerificationMatchDetails) *gen.VerificationMatchDetails {
	if details == nil {
		return nil
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil
	}
	var model gen.VerificationMatchDetails
	if json.Unmarshal(encoded, &model) != nil {
		return nil
	}
	if model.Transformations == nil {
		model.Transformations = make([]gen.VerificationTransformation, 0)
	}
	return &model
}

func checksumAddress(value string) (string, error) {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return "", err
	}
	return common.Address(address).Hex(), nil
}
