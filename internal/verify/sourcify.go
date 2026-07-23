package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultSourcifyBaseURL = "https://sourcify.dev/server"

var sourcifyCompilerVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+_-]*$`)

var (
	ErrSourcifyUnavailable          = errors.New("sourcify is unavailable")
	ErrSourcifyNotFound             = errors.New("sourcify contract or job was not found")
	ErrSourcifyRejected             = errors.New("sourcify rejected the request")
	ErrSourcifyAlreadyVerified      = errors.New("sourcify contract is already verified")
	ErrSourcifyInvalidResponse      = errors.New("sourcify returned an invalid response")
	ErrSourcifyTargetMismatch       = errors.New("sourcify contract does not match the exact local target")
	ErrSourcifyRequestMissing       = errors.New("sourcify durable verification request is unavailable")
	ErrVerificationTargetInvalid    = errors.New("canonical verification target is invalid")
	ErrConstructorArgumentsMismatch = errors.New("constructor arguments do not match the canonical creation input")
)

type SourcifyOptions struct {
	BaseURL          string
	Timeout          time.Duration
	MaxRequestBytes  int
	MaxResponseBytes int64
}

type SourcifyClient struct {
	baseURL          *url.URL
	http             *http.Client
	maxRequestBytes  int
	maxResponseBytes int64
}

type SourcifyBytecode struct {
	OnchainBytecode string `json:"onchainBytecode"`
}

type SourcifyCompilation struct {
	Language           string          `json:"language"`
	Compiler           string          `json:"compiler"`
	CompilerVersion    string          `json:"compilerVersion"`
	CompilerSettings   json.RawMessage `json:"compilerSettings"`
	Name               string          `json:"name"`
	FullyQualifiedName string          `json:"fullyQualifiedName"`
}

type SourcifyContract struct {
	Match            string              `json:"match"`
	CreationMatch    string              `json:"creationMatch"`
	RuntimeMatch     string              `json:"runtimeMatch"`
	ChainID          string              `json:"chainId"`
	Address          string              `json:"address"`
	CreationBytecode SourcifyBytecode    `json:"creationBytecode"`
	RuntimeBytecode  SourcifyBytecode    `json:"runtimeBytecode"`
	Sources          json.RawMessage     `json:"sources"`
	Compilation      SourcifyCompilation `json:"compilation"`
	ABI              json.RawMessage     `json:"abi"`
	Metadata         json.RawMessage     `json:"metadata"`
	StandardJSON     json.RawMessage     `json:"stdJsonInput"`
	CompilerOutput   json.RawMessage     `json:"stdJsonOutput"`
}

type SourcifyTicket struct {
	VerificationID string `json:"verificationId"`
}

type SourcifyJobContract struct {
	Match         *string `json:"match"`
	CreationMatch *string `json:"creationMatch"`
	RuntimeMatch  *string `json:"runtimeMatch"`
	ChainID       string  `json:"chainId"`
	Address       string  `json:"address"`
}

type SourcifyJob struct {
	IsJobCompleted bool                 `json:"isJobCompleted"`
	VerificationID string               `json:"verificationId"`
	Contract       *SourcifyJobContract `json:"contract,omitempty"`
	ErrorCode      string               `json:"error_code,omitempty"`
}

// VerificationTarget is the exact canonical code and creation identity that a
// caller resolved from local PostgreSQL facts. External adapters may compare
// data against it, but they never get to select or replace these fields.
type VerificationTarget struct {
	ChainID          uint64
	Address          string
	CodeHash         string
	AtBlockHash      string
	CreationBytecode string
	RuntimeBytecode  string
}

// BindConstructorArguments validates a locally resolved target, removes only
// the caller-declared suffix from its canonical creation input, and returns a
// normalized durable target. The caller can influence metadata, but not the
// selected block, code hash, runtime, or creation prefix.
func BindConstructorArguments(
	target VerificationTarget,
	rawArguments string,
	maximum int,
) (VerificationTarget, string, error) {
	if maximum <= 0 {
		maximum = defaultCompilerInputBytes
	}
	if !validSourcifyImportTarget(target, maximum) {
		return VerificationTarget{}, "", ErrVerificationTargetInvalid
	}
	creation, err := decodeBytecode(target.CreationBytecode)
	if err != nil || len(creation) == 0 {
		return VerificationTarget{}, "", ErrVerificationTargetInvalid
	}
	if rawArguments != strings.TrimSpace(rawArguments) || strings.HasPrefix(rawArguments, "0X") {
		return VerificationTarget{}, "", ErrConstructorArgumentsMismatch
	}
	argumentsText := strings.TrimPrefix(rawArguments, "0x")
	if len(argumentsText)%2 != 0 || len(argumentsText)/2 > maximum {
		return VerificationTarget{}, "", ErrConstructorArgumentsMismatch
	}
	arguments, err := hex.DecodeString(argumentsText)
	if err != nil || len(arguments) > len(creation) ||
		(len(arguments) > 0 && !bytes.Equal(creation[len(creation)-len(arguments):], arguments)) {
		return VerificationTarget{}, "", ErrConstructorArgumentsMismatch
	}
	creation = creation[:len(creation)-len(arguments)]
	if len(creation) == 0 {
		return VerificationTarget{}, "", ErrConstructorArgumentsMismatch
	}
	target.Address = strings.ToLower(target.Address)
	target.CodeHash = strings.ToLower(target.CodeHash)
	target.AtBlockHash = strings.ToLower(target.AtBlockHash)
	target.CreationBytecode = "0x" + hex.EncodeToString(creation)
	runtime, _ := decodeBytecode(target.RuntimeBytecode)
	target.RuntimeBytecode = "0x" + hex.EncodeToString(runtime)
	return target, hex.EncodeToString(arguments), nil
}

type SourcifyJobReader interface {
	Job(context.Context, string) (VerificationJob, bool, error)
}

type sourcifyContractWire struct {
	Match            json.RawMessage `json:"match"`
	CreationMatch    json.RawMessage `json:"creationMatch"`
	RuntimeMatch     json.RawMessage `json:"runtimeMatch"`
	ChainID          *string         `json:"chainId"`
	Address          *string         `json:"address"`
	CreationBytecode json.RawMessage `json:"creationBytecode"`
	RuntimeBytecode  json.RawMessage `json:"runtimeBytecode"`
	Sources          json.RawMessage `json:"sources"`
	Compilation      json.RawMessage `json:"compilation"`
	ABI              json.RawMessage `json:"abi"`
	Metadata         json.RawMessage `json:"metadata"`
	StandardJSON     json.RawMessage `json:"stdJsonInput"`
	CompilerOutput   json.RawMessage `json:"stdJsonOutput"`
}

type sourcifyBytecodeWire struct {
	OnchainBytecode json.RawMessage `json:"onchainBytecode"`
}

type sourcifyCompilationWire struct {
	Language           json.RawMessage `json:"language"`
	Compiler           json.RawMessage `json:"compiler"`
	CompilerVersion    json.RawMessage `json:"compilerVersion"`
	CompilerSettings   json.RawMessage `json:"compilerSettings"`
	Name               json.RawMessage `json:"name"`
	FullyQualifiedName json.RawMessage `json:"fullyQualifiedName"`
}

type sourcifyMinimalContractWire struct {
	Match         json.RawMessage `json:"match"`
	CreationMatch json.RawMessage `json:"creationMatch"`
	RuntimeMatch  json.RawMessage `json:"runtimeMatch"`
	ChainID       *string         `json:"chainId"`
	Address       *string         `json:"address"`
}

type sourcifyJobWire struct {
	IsJobCompleted *bool           `json:"isJobCompleted"`
	VerificationID *string         `json:"verificationId"`
	Contract       json.RawMessage `json:"contract"`
	Error          json.RawMessage `json:"error"`
}

type sourcifyJobErrorWire struct {
	CustomCode *string `json:"customCode"`
	Message    *string `json:"message"`
	ErrorID    *string `json:"errorId"`
}

type sourcifyErrorWire struct {
	CustomCode string `json:"customCode"`
}

func NewSourcifyClient(options SourcifyOptions) (*SourcifyClient, error) {
	return newSourcifyClient(options, nil, nil, false, false)
}

func newSourcifyClient(
	options SourcifyOptions,
	unsafeHTTPClient *http.Client,
	resolver outboundResolver,
	unsafeAllowHTTP bool,
	unsafeAllowPrivateNetworks bool,
) (*SourcifyClient, error) {
	base := strings.TrimSpace(options.BaseURL)
	if base == "" {
		base = defaultSourcifyBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || len(parsed.String()) > 4096 ||
		(parsed.Scheme != "https" && (!unsafeAllowHTTP || parsed.Scheme != "http")) ||
		unsafeSourcifyPath(parsed.EscapedPath()) {
		return nil, errors.New("invalid Sourcify base URL")
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if timeout < 100*time.Millisecond || timeout > 2*time.Minute {
		return nil, errors.New("sourcify timeout must be between 100ms and 2m")
	}
	maxRequest := options.MaxRequestBytes
	if maxRequest == 0 {
		maxRequest = defaultCompilerInputBytes
	}
	if maxRequest < 1 || maxRequest > 64<<20 {
		return nil, errors.New("sourcify request limit must be between 1 and 67108864 bytes")
	}
	maxResponse := options.MaxResponseBytes
	if maxResponse == 0 {
		maxResponse = 32 << 20
	}
	if maxResponse < 1 || maxResponse > 64<<20 {
		return nil, errors.New("sourcify response limit must be between 1 and 67108864 bytes")
	}
	return &SourcifyClient{
		baseURL: parsed,
		http: restrictedOutboundClient(
			unsafeHTTPClient, timeout, resolver, unsafeAllowPrivateNetworks,
		),
		maxRequestBytes: maxRequest, maxResponseBytes: maxResponse,
	}, nil
}

func (c *SourcifyClient) Lookup(ctx context.Context, chainID uint64, address string) (SourcifyContract, error) {
	if c == nil || chainID == 0 || !fixedHex(address, 20) {
		return SourcifyContract{}, errors.New("invalid sourcify lookup identity")
	}
	endpoint := c.endpoint(fmt.Sprintf("/v2/contract/%d/%s", chainID, strings.ToLower(address)))
	query := endpoint.Query()
	query.Set("fields", "all")
	endpoint.RawQuery = query.Encode()
	var wire sourcifyContractWire
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &wire); err != nil {
		return SourcifyContract{}, err
	}
	contract, err := decodeSourcifyContract(wire, chainID, address)
	if err != nil {
		return SourcifyContract{}, err
	}
	return contract, nil
}

func (c *SourcifyClient) Import(ctx context.Context, target VerificationTarget) (Request, error) {
	if c == nil || !validSourcifyImportTarget(target, c.maxRequestBytes) {
		return Request{}, errors.New("invalid Sourcify import target")
	}
	contract, err := c.Lookup(ctx, target.ChainID, target.Address)
	if err != nil {
		return Request{}, err
	}
	if contract.Match == "" || contract.RuntimeMatch == "" {
		return Request{}, ErrSourcifyTargetMismatch
	}
	upstreamRuntime, upstreamErr := decodeBytecode(contract.RuntimeBytecode.OnchainBytecode)
	localRuntime, localErr := decodeBytecode(target.RuntimeBytecode)
	if upstreamErr != nil || localErr != nil || len(upstreamRuntime) == 0 ||
		!bytes.Equal(upstreamRuntime, localRuntime) {
		return Request{}, ErrSourcifyTargetMismatch
	}
	language, ok := sourcifyLanguage(contract.Compilation.Language)
	if !ok || !validSourcifyImportCompilation(contract, language) {
		return Request{}, ErrSourcifyInvalidResponse
	}
	request := Request{
		ChainID: target.ChainID, Address: strings.ToLower(target.Address),
		CodeHash: strings.ToLower(target.CodeHash), AtBlockHash: strings.ToLower(target.AtBlockHash),
		Language: language, CompilerVersion: contract.Compilation.CompilerVersion,
		ContractIdentifier: contract.Compilation.FullyQualifiedName,
		StandardJSON:       append(json.RawMessage(nil), contract.StandardJSON...),
		CreationBytecode:   target.CreationBytecode, RuntimeBytecode: target.RuntimeBytecode,
		SubmitToSourcify: false,
	}
	if err := request.Validate(c.maxRequestBytes); err != nil {
		return Request{}, ErrSourcifyInvalidResponse
	}
	return request, nil
}

func (c *SourcifyClient) Submit(
	ctx context.Context,
	jobs SourcifyJobReader,
	jobID string,
	consent bool,
) (SourcifyTicket, error) {
	if !consent {
		return SourcifyTicket{}, ErrConsentRequired
	}
	if c == nil || jobs == nil || !validUUID(jobID) {
		return SourcifyTicket{}, errors.New("invalid Sourcify submission job")
	}
	job, found, err := jobs.Job(ctx, jobID)
	if err != nil {
		if ctx.Err() != nil {
			return SourcifyTicket{}, ctx.Err()
		}
		return SourcifyTicket{}, ErrSourcifyRequestMissing
	}
	if !found || !validDurableSourcifyJob(job, jobID) {
		return SourcifyTicket{}, ErrSourcifyRequestMissing
	}
	if !job.Request.SubmitToSourcify {
		return SourcifyTicket{}, ErrConsentRequired
	}
	return c.submitRequest(ctx, job.Request)
}

func (c *SourcifyClient) submitRequest(ctx context.Context, request Request) (SourcifyTicket, error) {
	if request.Validate(c.maxRequestBytes) != nil ||
		!standardJSONLanguageMatches(request.StandardJSON, request.Language) ||
		!sourcifyCompilerVersionPattern.MatchString(request.CompilerVersion) {
		return SourcifyTicket{}, errors.New("invalid Sourcify submission")
	}
	payload := struct {
		StandardJSON       json.RawMessage `json:"stdJsonInput"`
		CompilerVersion    string          `json:"compilerVersion"`
		ContractIdentifier string          `json:"contractIdentifier"`
	}{request.StandardJSON, request.CompilerVersion, request.ContractIdentifier}
	endpoint := c.endpoint(fmt.Sprintf("/v2/verify/%d/%s", request.ChainID, strings.ToLower(request.Address)))
	var ticket SourcifyTicket
	if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, http.StatusAccepted, &ticket); err != nil {
		return SourcifyTicket{}, err
	}
	if !validSourcifyVerificationID(ticket.VerificationID) {
		return SourcifyTicket{}, ErrSourcifyInvalidResponse
	}
	return ticket, nil
}

func (c *SourcifyClient) Status(ctx context.Context, verificationID string) (SourcifyJob, error) {
	if c == nil || !validSourcifyVerificationID(verificationID) {
		return SourcifyJob{}, errors.New("invalid Sourcify verification ID")
	}
	endpoint := c.endpoint("/v2/verify/" + verificationID)
	var wire sourcifyJobWire
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &wire); err != nil {
		return SourcifyJob{}, err
	}
	job, err := decodeSourcifyJob(wire, verificationID)
	if err != nil {
		return SourcifyJob{}, ErrSourcifyInvalidResponse
	}
	return job, nil
}

func (c *SourcifyClient) endpoint(suffix string) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + suffix
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return &endpoint
}

func (c *SourcifyClient) doJSON(
	ctx context.Context,
	method string,
	endpoint *url.URL,
	payload any,
	expectedStatus int,
	target any,
) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil || len(encoded) > c.maxRequestBytes {
			return errors.New("sourcify request exceeds its configured bound")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return errors.New("create sourcify request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "etherview-sourcify/1")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSourcifyUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > c.maxResponseBytes {
		return ErrSourcifyInvalidResponse
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSourcifyUnavailable
	}
	if int64(len(data)) > c.maxResponseBytes {
		return ErrSourcifyInvalidResponse
	}
	if response.StatusCode != expectedStatus {
		return classifySourcifyStatus(response.StatusCode, data)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ErrSourcifyInvalidResponse
	}
	if len(data) == 0 || validateUniqueJSON(data) != nil || json.Unmarshal(data, target) != nil {
		return ErrSourcifyInvalidResponse
	}
	return nil
}

func classifySourcifyStatus(status int, body []byte) error {
	var response sourcifyErrorWire
	_ = json.Unmarshal(body, &response)
	switch {
	case status == http.StatusNotFound:
		return ErrSourcifyNotFound
	case status == http.StatusConflict && response.CustomCode == "already_verified":
		return ErrSourcifyAlreadyVerified
	case status == http.StatusBadRequest || status == http.StatusConflict:
		return ErrSourcifyRejected
	case status == http.StatusTooManyRequests || status >= 500:
		return ErrSourcifyUnavailable
	default:
		return ErrSourcifyInvalidResponse
	}
}

func decodeSourcifyContract(
	wire sourcifyContractWire,
	chainID uint64,
	address string,
) (SourcifyContract, error) {
	match, matchOK := decodeSourcifyNullableMatch(wire.Match)
	creationMatch, creationOK := decodeSourcifyNullableMatch(wire.CreationMatch)
	runtimeMatch, runtimeOK := decodeSourcifyNullableMatch(wire.RuntimeMatch)
	if !matchOK || !creationOK || !runtimeOK || wire.ChainID == nil || wire.Address == nil ||
		*wire.ChainID != strconv.FormatUint(chainID, 10) || !strings.EqualFold(*wire.Address, address) {
		return SourcifyContract{}, ErrSourcifyInvalidResponse
	}
	creationBytecode, ok := decodeSourcifyBytecode(wire.CreationBytecode)
	if !ok {
		return SourcifyContract{}, ErrSourcifyInvalidResponse
	}
	runtimeBytecode, ok := decodeSourcifyBytecode(wire.RuntimeBytecode)
	if !ok {
		return SourcifyContract{}, ErrSourcifyInvalidResponse
	}
	if !optionalJSONObject(wire.Sources) || !optionalJSONArray(wire.ABI) ||
		!optionalJSONObject(wire.Metadata) || !optionalJSONObject(wire.StandardJSON) ||
		!optionalJSONObject(wire.CompilerOutput) {
		return SourcifyContract{}, ErrSourcifyInvalidResponse
	}
	compilation, ok := decodeSourcifyCompilation(wire.Compilation)
	if !ok {
		return SourcifyContract{}, ErrSourcifyInvalidResponse
	}
	return SourcifyContract{
		Match: nullableMatchValue(match), CreationMatch: nullableMatchValue(creationMatch),
		RuntimeMatch: nullableMatchValue(runtimeMatch), ChainID: *wire.ChainID, Address: *wire.Address,
		CreationBytecode: creationBytecode, RuntimeBytecode: runtimeBytecode,
		Sources: append(json.RawMessage(nil), wire.Sources...), Compilation: compilation,
		ABI: append(json.RawMessage(nil), wire.ABI...), Metadata: append(json.RawMessage(nil), wire.Metadata...),
		StandardJSON:   append(json.RawMessage(nil), wire.StandardJSON...),
		CompilerOutput: append(json.RawMessage(nil), wire.CompilerOutput...),
	}, nil
}

func decodeSourcifyBytecode(raw json.RawMessage) (SourcifyBytecode, bool) {
	if len(raw) == 0 {
		return SourcifyBytecode{}, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || !jsonObject(raw) {
		return SourcifyBytecode{}, false
	}
	var wire sourcifyBytecodeWire
	if json.Unmarshal(raw, &wire) != nil {
		return SourcifyBytecode{}, false
	}
	value, present, ok := optionalSourcifyString(wire.OnchainBytecode)
	if !ok || present && !validSourcifyBytecode(value) {
		return SourcifyBytecode{}, false
	}
	return SourcifyBytecode{OnchainBytecode: value}, true
}

func decodeSourcifyCompilation(raw json.RawMessage) (SourcifyCompilation, bool) {
	if len(raw) == 0 {
		return SourcifyCompilation{}, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || !jsonObject(raw) {
		return SourcifyCompilation{}, false
	}
	var wire sourcifyCompilationWire
	if json.Unmarshal(raw, &wire) != nil {
		return SourcifyCompilation{}, false
	}
	language, languagePresent, languageOK := optionalSourcifyString(wire.Language)
	compiler, compilerPresent, compilerOK := optionalSourcifyString(wire.Compiler)
	version, versionPresent, versionOK := optionalSourcifyString(wire.CompilerVersion)
	name, namePresent, nameOK := optionalSourcifyString(wire.Name)
	identifier, identifierPresent, identifierOK := optionalSourcifyString(wire.FullyQualifiedName)
	if !languageOK || !compilerOK || !versionOK || !nameOK || !identifierOK {
		return SourcifyCompilation{}, false
	}
	if languagePresent && language != "Solidity" && language != "Vyper" && language != "Fe" {
		return SourcifyCompilation{}, false
	}
	if compilerPresent && !boundedSourcifyText(compiler, 128) {
		return SourcifyCompilation{}, false
	}
	if languagePresent && compilerPresent &&
		((language == "Solidity" && compiler != "solc") ||
			(language == "Vyper" && compiler != "vyper")) {
		return SourcifyCompilation{}, false
	}
	if versionPresent && (!sourcifyCompilerVersionPattern.MatchString(version) || len(version) > 128) {
		return SourcifyCompilation{}, false
	}
	if len(wire.CompilerSettings) > 0 && !jsonObject(wire.CompilerSettings) {
		return SourcifyCompilation{}, false
	}
	if namePresent && !boundedSourcifyText(name, 512) {
		return SourcifyCompilation{}, false
	}
	if identifierPresent && !validContractIdentifier(identifier) {
		return SourcifyCompilation{}, false
	}
	return SourcifyCompilation{
		Language: language, Compiler: compiler,
		CompilerVersion:  version,
		CompilerSettings: append(json.RawMessage(nil), wire.CompilerSettings...),
		Name:             name, FullyQualifiedName: identifier,
	}, true
}

func validSourcifyImportCompilation(contract SourcifyContract, language Language) bool {
	return jsonObject(contract.StandardJSON) &&
		standardJSONLanguageMatches(contract.StandardJSON, language) &&
		sourcifyCompilerVersionPattern.MatchString(contract.Compilation.CompilerVersion) &&
		validContractIdentifier(contract.Compilation.FullyQualifiedName) &&
		(language != LanguageSolidity || contract.Compilation.Compiler == "solc") &&
		(language != LanguageVyper || contract.Compilation.Compiler == "vyper")
}

func validDurableSourcifyJob(job VerificationJob, expectedID string) bool {
	if job.ID != expectedID || !validUUID(job.ID) || !job.Status.valid() ||
		job.Request.Validate(64<<20) != nil || validatePersistedJobState(job) != nil {
		return false
	}
	encoded, err := json.Marshal(job.Request)
	if err != nil || len(encoded) > 64<<20 {
		return false
	}
	digest := verificationRequestDigest(encoded, job.RequiresHardIsolation)
	return bytes.Equal(job.RequestDigest[:], digest[:])
}

func validSourcifyImportTarget(target VerificationTarget, maximum int) bool {
	if target.ChainID == 0 || !fixedHex(target.Address, 20) || !fixedHex(target.CodeHash, 32) ||
		!fixedHex(target.AtBlockHash, 32) || len(target.RuntimeBytecode) > maximum ||
		len(target.CreationBytecode) > maximum {
		return false
	}
	runtimeBytecode, err := decodeBytecode(target.RuntimeBytecode)
	if err != nil || len(runtimeBytecode) == 0 {
		return false
	}
	codeHash, _ := decodeBytecode(target.CodeHash)
	if !bytes.Equal(keccak256Bytes(runtimeBytecode), codeHash) {
		return false
	}
	_, err = decodeBytecode(target.CreationBytecode)
	return err == nil
}

func standardJSONLanguageMatches(raw json.RawMessage, language Language) bool {
	var header struct {
		Language string `json:"language"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return false
	}
	switch language {
	case LanguageSolidity:
		return header.Language == "Solidity"
	case LanguageVyper:
		return header.Language == "Vyper"
	default:
		return false
	}
}

func sourcifyLanguage(value string) (Language, bool) {
	switch value {
	case "Solidity":
		return LanguageSolidity, true
	case "Vyper":
		return LanguageVyper, true
	default:
		return "", false
	}
}

func decodeSourcifyJob(wire sourcifyJobWire, expectedID string) (SourcifyJob, error) {
	if wire.IsJobCompleted == nil || wire.VerificationID == nil ||
		*wire.VerificationID != expectedID || !validSourcifyVerificationID(*wire.VerificationID) {
		return SourcifyJob{}, ErrSourcifyInvalidResponse
	}
	contract, err := decodeSourcifyJobContract(wire.Contract)
	if err != nil {
		return SourcifyJob{}, ErrSourcifyInvalidResponse
	}
	errorCode, err := decodeSourcifyJobError(wire.Error)
	if err != nil {
		return SourcifyJob{}, ErrSourcifyInvalidResponse
	}
	job := SourcifyJob{
		IsJobCompleted: *wire.IsJobCompleted,
		VerificationID: *wire.VerificationID,
		Contract:       contract,
		ErrorCode:      errorCode,
	}
	if !job.IsJobCompleted {
		if job.ErrorCode != "" || sourcifyJobHasMatch(job.Contract) {
			return SourcifyJob{}, ErrSourcifyInvalidResponse
		}
		return job, nil
	}
	if job.ErrorCode != "" {
		if sourcifyJobHasMatch(job.Contract) {
			return SourcifyJob{}, ErrSourcifyInvalidResponse
		}
		return job, nil
	}
	if job.Contract == nil || job.Contract.Match == nil ||
		(job.Contract.CreationMatch == nil && job.Contract.RuntimeMatch == nil) {
		return SourcifyJob{}, ErrSourcifyInvalidResponse
	}
	return job, nil
}

func decodeSourcifyJobContract(raw json.RawMessage) (*SourcifyJobContract, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || !jsonObject(raw) {
		return nil, ErrSourcifyInvalidResponse
	}
	var wire sourcifyMinimalContractWire
	if json.Unmarshal(raw, &wire) != nil || wire.ChainID == nil || wire.Address == nil ||
		!canonicalUint64(*wire.ChainID) || !fixedHex(*wire.Address, 20) {
		return nil, ErrSourcifyInvalidResponse
	}
	match, matchOK := decodeSourcifyNullableMatch(wire.Match)
	creationMatch, creationOK := decodeSourcifyNullableMatch(wire.CreationMatch)
	runtimeMatch, runtimeOK := decodeSourcifyNullableMatch(wire.RuntimeMatch)
	if !matchOK || !creationOK || !runtimeOK {
		return nil, ErrSourcifyInvalidResponse
	}
	return &SourcifyJobContract{
		Match: match, CreationMatch: creationMatch, RuntimeMatch: runtimeMatch,
		ChainID: *wire.ChainID, Address: *wire.Address,
	}, nil
}

func decodeSourcifyJobError(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || !jsonObject(raw) {
		return "", ErrSourcifyInvalidResponse
	}
	var wire sourcifyJobErrorWire
	if json.Unmarshal(raw, &wire) != nil || wire.CustomCode == nil || wire.Message == nil ||
		wire.ErrorID == nil || !validSourcifyErrorCode(*wire.CustomCode) ||
		!boundedSourcifyText(*wire.Message, 64<<10) || !validUUID(*wire.ErrorID) {
		return "", ErrSourcifyInvalidResponse
	}
	return *wire.CustomCode, nil
}

func decodeSourcifyNullableMatch(raw json.RawMessage) (*string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !validSourcifyMatch(value) {
		return nil, false
	}
	return &value, true
}

func sourcifyJobHasMatch(contract *SourcifyJobContract) bool {
	return contract != nil &&
		(contract.Match != nil || contract.CreationMatch != nil || contract.RuntimeMatch != nil)
}

func validSourcifyVerificationID(value string) bool {
	return value == strings.ToLower(value) && validUUID(value)
}

func validSourcifyErrorCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validSourcifyMatch(value string) bool {
	return value == "match" || value == "exact_match"
}

func validSourcifyBytecode(value string) bool {
	if value != strings.ToLower(value) || value != strings.TrimSpace(value) {
		return false
	}
	decoded, err := decodeBytecode(value)
	return err == nil && decoded != nil && strings.HasPrefix(value, "0x")
}

func optionalJSONObject(value json.RawMessage) bool {
	return len(value) == 0 || jsonObject(value)
}

func optionalJSONArray(value json.RawMessage) bool {
	return len(value) == 0 || jsonArray(value)
}

func nullableMatchValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalSourcifyString(raw json.RawMessage) (string, bool, bool) {
	if len(raw) == 0 {
		return "", false, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true, false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", true, false
	}
	return value, true, true
}

func boundedSourcifyText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value)
}

func validContractIdentifier(value string) bool {
	separator := strings.LastIndex(value, ":")
	return len(value) <= 512 && separator > 0 && separator < len(value)-1
}

func canonicalUint64(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(parsed, 10) == value
}

func unsafeSourcifyPath(value string) bool {
	for segment := range strings.SplitSeq(value, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == ".." {
			return true
		}
	}
	return false
}
