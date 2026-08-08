package etherscan

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/verify"
)

const defaultVerificationInputBytes = 5 << 20

var compilerIdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)

// VerificationService is the public subset of verify.Service used by the
// Etherscan compatibility boundary. Production wiring supplies the same
// durable service used by the native API only when public verification is
// enabled.
type VerificationService interface {
	SubmitV2(context.Context, verify.SubmissionV2) (verify.VerificationJob, bool, error)
	Job(context.Context, string) (verify.VerificationJob, bool, error)
}

type etherscanVerificationForm struct {
	language             verify.Language
	compilerVersion      string
	contractIdentifier   string
	standardJSON         json.RawMessage
	constructorArguments string
	licenseType          string
}

type verificationTarget struct {
	codeHash         []byte
	blockHash        []byte
	runtimeBytecode  []byte
	creationBytecode string
}

type proxyVerificationTarget struct {
	proxyCodeHash          []byte
	blockHash              []byte
	contextBlockNumber     string
	contextBlockHash       []byte
	kind                   string
	pattern                string
	standardVersion        sql.NullString
	implementationAddress  []byte
	implementationCodeHash []byte
	adminAddress           []byte
	adminCodeHash          []byte
	beaconAddress          []byte
	beaconCodeHash         []byte
	managementKind         string
	managementAddress      []byte
	managementCodeHash     []byte
	observationGeneration  int64
	artifactResolution     sql.NullInt64
	beaconGeneration       sql.NullInt64
	uupsGeneration         sql.NullInt64
	proxyVerified          bool
	implementationVerified bool
	managementVerified     bool
	existingBindingID      sql.NullString
}

func (b *PostgresBackend) submitProxyVerification(ctx context.Context, values url.Values) (string, error) {
	if b.verification == nil {
		return "", ErrVerificationUnavailable
	}
	address, addressBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return "", err
	}
	var target proxyVerificationTarget
	err = b.db.QueryRowContext(ctx, proxyVerificationTargetSQL, b.chain, addressBytes).Scan(
		&target.proxyCodeHash,
		&target.blockHash,
		&target.contextBlockNumber,
		&target.contextBlockHash,
		&target.kind,
		&target.pattern,
		&target.standardVersion,
		&target.implementationAddress,
		&target.implementationCodeHash,
		&target.adminAddress,
		&target.adminCodeHash,
		&target.beaconAddress,
		&target.beaconCodeHash,
		&target.managementKind,
		&target.managementAddress,
		&target.managementCodeHash,
		&target.observationGeneration,
		&target.artifactResolution,
		&target.beaconGeneration,
		&target.uupsGeneration,
		&target.proxyVerified,
		&target.implementationVerified,
		&target.managementVerified,
		&target.existingBindingID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrProxyVerificationTargetUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("query proxy verification target: %w", err)
	}
	if !validExactProxyVerificationTarget(target) {
		return "", ErrProxyVerificationTargetUnavailable
	}
	if !target.proxyVerified {
		return "", ErrProxySourceUnverified
	}
	if !target.implementationVerified {
		return "", ErrProxyImplementationUnverified
	}
	if !target.managementVerified {
		return "", ErrProxyVerificationTargetUnavailable
	}
	implementation := common.BytesToAddress(target.implementationAddress)
	if expected := values.Get("expectedimplementation"); expected != "" {
		parsedExpected, _, parseErr := parseAddressParameter(expected, "expectedimplementation")
		if parseErr != nil {
			return "", parseErr
		}
		if parsedExpected != implementation {
			return "", ErrProxyExpectedImplementationMismatch
		}
	}
	if target.existingBindingID.Valid {
		// Ordinary canonical-tip growth must not churn the opaque binding ID.
		// The query returns an existing ID only when its exact proxy generation
		// and every participating code-identity epoch are still current.
		if !validVerificationGUID(target.existingBindingID.String) {
			return "", errors.New("stored proxy binding has an invalid verification job ID")
		}
		return target.existingBindingID.String, nil
	}
	request := verify.SubmissionV2{
		Kind: verify.JobProxy,
		Target: &verify.VerificationTarget{
			ChainID:     b.chainID,
			Address:     strings.ToLower(address.Hex()),
			CodeHash:    "0x" + hex.EncodeToString(target.proxyCodeHash),
			AtBlockHash: "0x" + hex.EncodeToString(target.blockHash),
		},
		ProxyTarget: &verify.ProxyVerificationTarget{
			Kind:                         target.kind,
			Pattern:                      target.pattern,
			StandardVersion:              target.standardVersion.String,
			SubmissionContextBlockNumber: target.contextBlockNumber,
			SubmissionContextBlockHash:   "0x" + hex.EncodeToString(target.contextBlockHash),
			ImplementationAddress:        strings.ToLower(implementation.Hex()),
			ImplementationCodeHash:       "0x" + hex.EncodeToString(target.implementationCodeHash),
			AdminAddress:                 optionalProxyHex(target.adminAddress),
			AdminCodeHash:                optionalProxyHex(target.adminCodeHash),
			BeaconAddress:                optionalProxyHex(target.beaconAddress),
			BeaconCodeHash:               optionalProxyHex(target.beaconCodeHash),
			ManagementKind:               target.managementKind,
			ManagementAddress:            optionalProxyHex(target.managementAddress),
			ManagementCodeHash:           optionalProxyHex(target.managementCodeHash),
			ObservationGenerationID:      strconv.FormatInt(target.observationGeneration, 10),
			ArtifactResolutionID:         optionalProxyGeneration(target.artifactResolution),
			BeaconGenerationID:           optionalProxyGeneration(target.beaconGeneration),
			UUPSGenerationID:             optionalProxyGeneration(target.uupsGeneration),
			ExpectedImplementation:       strings.ToLower(implementation.Hex()),
		},
	}
	job, _, err := b.verification.SubmitV2(ctx, request)
	if err != nil {
		return "", translateVerificationServiceError(err)
	}
	if !validVerificationGUID(job.ID) {
		return "", errors.New("verification service returned an invalid proxy job ID")
	}
	return job.ID, nil
}

func validExactProxyVerificationTarget(target proxyVerificationTarget) bool {
	if !validProxyCodeIdentity(target.implementationAddress, target.implementationCodeHash) ||
		len(target.proxyCodeHash) != 32 || !nonZeroProxyBytes(target.proxyCodeHash) ||
		len(target.blockHash) != 32 || !nonZeroProxyBytes(target.blockHash) ||
		len(target.contextBlockHash) != 32 || !nonZeroProxyBytes(target.contextBlockHash) ||
		!validOptionalProxyCodeIdentity(target.adminAddress, target.adminCodeHash) ||
		!validOptionalProxyCodeIdentity(target.beaconAddress, target.beaconCodeHash) ||
		!validOptionalProxyCodeIdentity(target.managementAddress, target.managementCodeHash) ||
		target.observationGeneration <= 0 || !validProxyContextBlockNumber(target.contextBlockNumber) ||
		(target.standardVersion.Valid && target.standardVersion.String != "5.6.1") {
		return false
	}
	switch target.kind {
	case "eip1167":
		if target.pattern != "clone" {
			return false
		}
	case "eip1967":
		if target.pattern != "erc1967" && target.pattern != "transparent" &&
			target.pattern != "uups" {
			return false
		}
	case "beacon":
		if target.pattern != "beacon" {
			return false
		}
	default:
		return false
	}
	switch target.pattern {
	case "transparent":
		return target.artifactResolution.Valid && target.artifactResolution.Int64 > 0 &&
			!target.beaconGeneration.Valid && !target.uupsGeneration.Valid && target.standardVersion.Valid &&
			target.standardVersion.String == "5.6.1" &&
			target.managementKind == "proxy_admin" &&
			validProxyCodeIdentity(target.adminAddress, target.adminCodeHash) &&
			bytes.Equal(target.adminAddress, target.managementAddress) &&
			bytes.Equal(target.adminCodeHash, target.managementCodeHash) &&
			len(target.beaconAddress) == 0
	case "uups":
		return target.artifactResolution.Valid && target.artifactResolution.Int64 > 0 &&
			!target.beaconGeneration.Valid && target.uupsGeneration.Valid &&
			target.uupsGeneration.Int64 > 0 && target.standardVersion.Valid &&
			target.standardVersion.String == "5.6.1" &&
			target.managementKind == "none" && len(target.adminAddress) == 0 &&
			len(target.beaconAddress) == 0 && len(target.managementAddress) == 0
	case "beacon":
		return target.artifactResolution.Valid && target.artifactResolution.Int64 > 0 &&
			target.beaconGeneration.Valid && target.beaconGeneration.Int64 > 0 &&
			!target.uupsGeneration.Valid && target.standardVersion.Valid &&
			target.standardVersion.String == "5.6.1" &&
			target.managementKind == "upgradeable_beacon" &&
			validProxyCodeIdentity(target.beaconAddress, target.beaconCodeHash) &&
			bytes.Equal(target.beaconAddress, target.managementAddress) &&
			bytes.Equal(target.beaconCodeHash, target.managementCodeHash) &&
			len(target.adminAddress) == 0
	case "clone":
		return !target.artifactResolution.Valid && !target.beaconGeneration.Valid && !target.uupsGeneration.Valid &&
			!target.standardVersion.Valid && target.managementKind == "none" && len(target.adminAddress) == 0 &&
			len(target.beaconAddress) == 0 && len(target.managementAddress) == 0
	case "erc1967":
		return target.artifactResolution.Valid && target.artifactResolution.Int64 > 0 &&
			!target.beaconGeneration.Valid && !target.uupsGeneration.Valid && target.standardVersion.Valid &&
			target.standardVersion.String == "5.6.1" &&
			target.managementKind == "none" && len(target.adminAddress) == 0 &&
			len(target.beaconAddress) == 0 && len(target.managementAddress) == 0
	default:
		return false
	}
}

func validProxyContextBlockNumber(value string) bool {
	_, err := parseCanonicalDecimal(value)
	return err == nil
}

func validProxyCodeIdentity(address, codeHash []byte) bool {
	return len(address) == 20 && len(codeHash) == 32 &&
		nonZeroProxyBytes(address) && nonZeroProxyBytes(codeHash)
}

func nonZeroProxyBytes(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return true
		}
	}
	return false
}

func validOptionalProxyCodeIdentity(address, codeHash []byte) bool {
	if len(address) == 0 || len(codeHash) == 0 {
		return len(address) == 0 && len(codeHash) == 0
	}
	return validProxyCodeIdentity(address, codeHash)
}

func optionalProxyHex(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(value)
}

func optionalProxyGeneration(value sql.NullInt64) string {
	if !value.Valid {
		return ""
	}
	return strconv.FormatInt(value.Int64, 10)
}

func (b *PostgresBackend) proxyVerificationStatus(ctx context.Context, values url.Values) (string, error) {
	if b.verification == nil {
		return "", ErrVerificationUnavailable
	}
	guid, err := oneVerificationValue(values, "guid", true)
	if err != nil || !validVerificationGUID(guid) {
		return "", invalidParameter("guid must be a UUID")
	}
	job, found, err := b.verification.Job(ctx, guid)
	if err != nil {
		return "", translateVerificationServiceError(err)
	}
	if !found || job.Kind != verify.JobProxy {
		return "", ErrVerificationJobNotFound
	}
	switch job.Status {
	case verify.JobQueued, verify.JobRunning:
		return "", ErrPending
	case verify.JobSucceeded:
		var outcome struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(job.Outcome, &outcome) != nil ||
			outcome.Kind != "proxy_verification_success" {
			return "", errors.New("succeeded proxy verification job has an invalid outcome")
		}
		return "Pass - Verified", nil
	case verify.JobFailed, verify.JobCancelled:
		return "", ErrProxyVerificationFailed
	default:
		return "", errors.New("proxy verification job has an invalid status")
	}
}

func (b *PostgresBackend) submitSourceVerification(ctx context.Context, values url.Values) (string, error) {
	if b.verification == nil {
		return "", ErrVerificationUnavailable
	}
	maximum := b.maxVerificationInputBytes
	form, addressBytes, address, err := parseEtherscanVerificationForm(values, maximum)
	if err != nil {
		return "", err
	}
	target, err := b.currentVerificationTarget(ctx, addressBytes, address)
	if err != nil {
		return "", err
	}
	// Etherscan's constructorArguments and licenseType fields remain accepted at
	// the compatibility boundary, but verifier v2 derives constructor arguments
	// from the canonical creation input and does not persist either hint.
	request := verify.SubmissionV2{
		Kind:             verify.JobAddress,
		Language:         form.language,
		CompilerVersion:  form.compilerVersion,
		StandardJSON:     form.standardJSON,
		ContractNameHint: form.contractIdentifier,
		Bytecodes: []verify.BytecodePair{{
			Creation: target.creationBytecode,
			Runtime:  "0x" + hex.EncodeToString(target.runtimeBytecode),
		}},
		Target: &verify.VerificationTarget{
			ChainID:          b.chainID,
			Address:          strings.ToLower(address),
			CodeHash:         "0x" + hex.EncodeToString(target.codeHash),
			AtBlockHash:      "0x" + hex.EncodeToString(target.blockHash),
			CreationBytecode: target.creationBytecode,
			RuntimeBytecode:  "0x" + hex.EncodeToString(target.runtimeBytecode),
		},
	}
	job, _, err := b.verification.SubmitV2(ctx, request)
	if err != nil {
		return "", translateVerificationServiceError(err)
	}
	if !validVerificationGUID(job.ID) {
		return "", errors.New("verification service returned an invalid job ID")
	}
	return job.ID, nil
}

func (b *PostgresBackend) sourceVerificationStatus(ctx context.Context, values url.Values) (string, error) {
	if b.verification == nil {
		return "", ErrVerificationUnavailable
	}
	guid, err := oneVerificationValue(values, "guid", true)
	if err != nil || !validVerificationGUID(guid) {
		return "", invalidParameter("guid must be a UUID")
	}
	job, found, err := b.verification.Job(ctx, guid)
	if err != nil {
		return "", translateVerificationServiceError(err)
	}
	if !found || job.Kind == verify.JobProxy {
		return "", ErrVerificationJobNotFound
	}
	switch job.Status {
	case verify.JobQueued, verify.JobRunning:
		return "", ErrPending
	case verify.JobSucceeded:
		var outcome struct {
			Kind string `json:"kind"`
		}
		if len(job.Outcome) == 0 || json.Unmarshal(job.Outcome, &outcome) != nil {
			return "", errors.New("succeeded verification job has no valid outcome")
		}
		switch outcome.Kind {
		case "verification_success":
			return "Pass - Verified", nil
		case "compilation_failure", "verification_failure":
			return "", ErrVerificationFailed
		default:
			return "", errors.New("succeeded verification job has an invalid outcome")
		}
	case verify.JobFailed, verify.JobCancelled:
		return "", ErrVerificationFailed
	default:
		return "", errors.New("verification job has an invalid status")
	}
}

func translateVerificationServiceError(err error) error {
	var serviceError verify.ServiceError
	if errors.As(err, &serviceError) && serviceError.Code == verify.ServiceInvalidRequest {
		return invalidParameter("verification request is invalid")
	}
	return fmt.Errorf("verification service: %w", err)
}

func (b *PostgresBackend) currentVerificationTarget(ctx context.Context, addressBytes []byte, address string) (verificationTarget, error) {
	var target verificationTarget
	var creation sql.NullString
	err := b.db.QueryRowContext(ctx, verificationTargetSQL, b.chain, addressBytes, strings.ToLower(address)).Scan(
		&target.codeHash, &target.blockHash, &target.runtimeBytecode, &creation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return verificationTarget{}, ErrVerificationTargetUnavailable
	}
	if err != nil {
		return verificationTarget{}, fmt.Errorf("query verification target: %w", err)
	}
	if len(target.codeHash) != 32 || len(target.blockHash) != 32 || len(target.runtimeBytecode) == 0 || len(target.runtimeBytecode) > b.maxVerificationInputBytes {
		return verificationTarget{}, ErrVerificationTargetUnavailable
	}
	if !bytes.Equal(crypto.Keccak256(target.runtimeBytecode), target.codeHash) {
		return verificationTarget{}, ErrVerificationTargetUnavailable
	}
	if !creation.Valid || strings.TrimSpace(creation.String) == "" {
		return verificationTarget{}, ErrVerificationTargetUnavailable
	}
	target.creationBytecode = creation.String
	return target, nil
}

// ResolveVerificationTarget returns only locally authoritative canonical facts
// for the configured chain. Native and interoperability submission handlers
// use this same resolver so neither boundary can accept a client-selected code
// hash, block hash, runtime bytecode, or creation input.
func (b *PostgresBackend) ResolveVerificationTarget(ctx context.Context, rawAddress string) (verify.VerificationTarget, error) {
	if b == nil || b.db == nil || b.chainID == 0 {
		return verify.VerificationTarget{}, ErrVerificationTargetUnavailable
	}
	address, addressBytes, err := parseAddressParameter(rawAddress, "address")
	if err != nil {
		return verify.VerificationTarget{}, ErrVerificationTargetUnavailable
	}
	target, err := b.currentVerificationTarget(ctx, addressBytes, address.String())
	if err != nil {
		return verify.VerificationTarget{}, err
	}
	creation, err := stripConstructorArguments(target.creationBytecode, "", b.maxVerificationInputBytes)
	if err != nil {
		return verify.VerificationTarget{}, ErrVerificationTargetUnavailable
	}
	return verify.VerificationTarget{
		ChainID: b.chainID, Address: strings.ToLower(address.String()),
		CodeHash:         "0x" + hex.EncodeToString(target.codeHash),
		AtBlockHash:      "0x" + hex.EncodeToString(target.blockHash),
		CreationBytecode: creation,
		RuntimeBytecode:  "0x" + hex.EncodeToString(target.runtimeBytecode),
	}, nil
}

func parseEtherscanVerificationForm(values url.Values, maximum int) (etherscanVerificationForm, []byte, string, error) {
	if maximum <= 0 {
		maximum = defaultVerificationInputBytes
	}
	if err := validateVerificationFormKeys(values); err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	address, addressBytes, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	sourceEntries := values["sourceCode"]
	if len(sourceEntries) != 1 || strings.TrimSpace(sourceEntries[0]) == "" || len(sourceEntries[0]) > maximum {
		return etherscanVerificationForm{}, nil, "", invalidParameter("sourceCode must contain at most %d bytes", maximum)
	}
	sourceCode := sourceEntries[0]
	codeFormat, err := oneVerificationValue(values, "codeformat", true)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	contractName, err := oneVerificationValue(values, "contractname", true)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	compilerVersion, err := oneVerificationValue(values, "compilerversion", true)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}

	constructorArguments, err := aliasedVerificationValue(values, "constructorArguments", "constructorArguements")
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	constructorArguments, err = normalizeVerificationHex(constructorArguments, "constructorArguments", maximum)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	licenseType, err := oneVerificationValue(values, "licenseType", false)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	if licenseType == "" {
		licenseType = "1"
	}
	license, parseErr := strconv.ParseUint(licenseType, 10, 8)
	if parseErr != nil || license < 1 || license > 14 || strconv.FormatUint(license, 10) != licenseType {
		return etherscanVerificationForm{}, nil, "", invalidParameter("licenseType must be between 1 and 14")
	}

	language := verify.LanguageSolidity
	switch codeFormat {
	case "solidity-single-file", "solidity-standard-json-input":
	default:
		return etherscanVerificationForm{}, nil, "", invalidParameter("unsupported codeformat")
	}
	if compilerVersion == "" {
		return etherscanVerificationForm{}, nil, "", invalidParameter("compilerversion is required")
	}

	settings, err := parseVerificationCompilerSettings(values, language)
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	var standardJSON json.RawMessage
	var identifier string
	if codeFormat == "solidity-single-file" {
		identifier, standardJSON, err = singleFileCompilerInput(
			sourceCode,
			contractName,
			compilerVersion,
			settings,
			maximum,
		)
	} else {
		identifier, standardJSON, err = standardCompilerInput(
			sourceCode,
			contractName,
			language,
			compilerVersion,
			settings,
			maximum,
		)
	}
	if err != nil {
		return etherscanVerificationForm{}, nil, "", err
	}
	if len(standardJSON) > maximum {
		return etherscanVerificationForm{}, nil, "", invalidParameter("compiler input exceeds %d bytes", maximum)
	}
	return etherscanVerificationForm{
		language: language, compilerVersion: compilerVersion,
		contractIdentifier: identifier, standardJSON: standardJSON,
		constructorArguments: constructorArguments, licenseType: licenseType,
	}, addressBytes, address.String(), nil
}

type verificationCompilerSettings struct {
	optimizationSet bool
	optimized       bool
	runsSet         bool
	runs            uint64
	evmVersion      string
	libraries       []verificationLibrary
}

type verificationLibrary struct {
	name    string
	address string
}

func parseVerificationCompilerSettings(values url.Values, language verify.Language) (verificationCompilerSettings, error) {
	var settings verificationCompilerSettings
	optimization, err := oneVerificationValue(values, "optimizationUsed", false)
	if err != nil {
		return settings, err
	}
	if optimization != "" {
		if optimization != "0" && optimization != "1" {
			return settings, invalidParameter("optimizationUsed must be 0 or 1")
		}
		settings.optimizationSet = true
		settings.optimized = optimization == "1"
	}
	runs, err := oneVerificationValue(values, "runs", false)
	if err != nil {
		return settings, err
	}
	if runs != "" {
		parsed, parseErr := strconv.ParseUint(runs, 10, 64)
		if parseErr != nil || parsed > 1_000_000 || strconv.FormatUint(parsed, 10) != runs {
			return settings, invalidParameter("runs must be between 0 and 1000000")
		}
		settings.runsSet, settings.runs = true, parsed
	}
	settings.evmVersion, err = aliasedVerificationValue(values, "evmVersion", "evmversion")
	if err != nil {
		return settings, err
	}
	if settings.evmVersion == "default" {
		settings.evmVersion = ""
	}
	if settings.evmVersion != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`).MatchString(settings.evmVersion) {
		return settings, invalidParameter("evmVersion is invalid")
	}
	for index := 1; index <= 10; index++ {
		name, nameErr := oneVerificationValue(values, fmt.Sprintf("libraryname%d", index), false)
		address, addressErr := oneVerificationValue(values, fmt.Sprintf("libraryaddress%d", index), false)
		if nameErr != nil || addressErr != nil {
			return settings, errors.Join(nameErr, addressErr)
		}
		if (name == "") != (address == "") {
			return settings, invalidParameter("libraryname%d and libraryaddress%d must be provided together", index, index)
		}
		if name == "" {
			continue
		}
		if language != verify.LanguageSolidity {
			return settings, invalidParameter("libraries are only valid for Solidity")
		}
		parsedAddress, _, parseErr := parseAddressParameter(address, fmt.Sprintf("libraryaddress%d", index))
		if parseErr != nil {
			return settings, parseErr
		}
		settings.libraries = append(settings.libraries, verificationLibrary{name: name, address: strings.ToLower(parsedAddress.String())})
	}
	return settings, nil
}

func singleFileCompilerInput(
	sourceCode string,
	contractName string,
	compilerVersion string,
	form verificationCompilerSettings,
	maximum int,
) (string, json.RawMessage, error) {
	source, name, err := contractIdentifier(contractName, "Contract.sol", false)
	if err != nil {
		return "", nil, err
	}
	if !form.optimizationSet {
		form.optimizationSet = true
		form.optimized = false
	}
	if !form.runsSet {
		form.runsSet, form.runs = true, 200
	}
	document := map[string]any{
		"language": "Solidity",
		"sources":  map[string]any{source: map[string]string{"content": sourceCode}},
		"settings": map[string]any{},
	}
	if err := mergeCompilerSettings(document["settings"].(map[string]any), verify.LanguageSolidity, form, []string{source}); err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", nil, invalidParameter("sourceCode cannot be encoded")
	}
	identifier := source + ":" + name
	prepared, err := verify.PrepareStandardJSON(
		encoded,
		verify.LanguageSolidity,
		compilerVersion,
		identifier,
		maximum,
	)
	if err != nil {
		return "", nil, invalidParameter("compiler input is invalid")
	}
	return identifier, prepared, nil
}

func standardCompilerInput(
	raw string,
	rawIdentifier string,
	language verify.Language,
	compilerVersion string,
	form verificationCompilerSettings,
	maximum int,
) (string, json.RawMessage, error) {
	source, name, err := contractIdentifier(rawIdentifier, "", true)
	if err != nil {
		return "", nil, err
	}
	identifier := source + ":" + name
	prepared, err := verify.PrepareStandardJSON(
		json.RawMessage(raw),
		language,
		compilerVersion,
		identifier,
		maximum,
	)
	if err != nil {
		return "", nil, invalidParameter("sourceCode is not a valid bounded Standard JSON input")
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(prepared, &document); err != nil || document == nil {
		return "", nil, invalidParameter("sourceCode must be one Standard JSON object")
	}
	wantLanguage := "Solidity"
	var actualLanguage string
	if err := json.Unmarshal(document["language"], &actualLanguage); err != nil || actualLanguage != wantLanguage {
		return "", nil, invalidParameter("Standard JSON language must be %s", wantLanguage)
	}
	var sourceDocuments map[string]json.RawMessage
	if err := json.Unmarshal(document["sources"], &sourceDocuments); err != nil || len(sourceDocuments) == 0 {
		return "", nil, invalidParameter("Standard JSON sources must be a non-empty object")
	}
	sourceNames := make([]string, 0, len(sourceDocuments))
	for source, rawSource := range sourceDocuments {
		var sourceDocument struct {
			Content *string         `json:"content"`
			URLs    json.RawMessage `json:"urls"`
		}
		if source == "" || json.Unmarshal(rawSource, &sourceDocument) != nil || sourceDocument.Content == nil || len(sourceDocument.URLs) != 0 {
			return "", nil, invalidParameter("every Standard JSON source must contain inline content and no URLs")
		}
		sourceNames = append(sourceNames, source)
	}
	if _, exists := sourceDocuments[source]; !exists {
		return "", nil, invalidParameter("contractname source %q is not present in Standard JSON", source)
	}
	settings := make(map[string]any)
	if rawSettings := document["settings"]; len(rawSettings) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(rawSettings))
		decoder.UseNumber()
		if err := decoder.Decode(&settings); err != nil || settings == nil {
			return "", nil, invalidParameter("Standard JSON settings must be an object")
		}
	}
	if err := mergeCompilerSettings(settings, language, form, sourceNames); err != nil {
		return "", nil, err
	}
	document["settings"], err = json.Marshal(settings)
	if err != nil {
		return "", nil, invalidParameter("Standard JSON settings cannot be encoded")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", nil, invalidParameter("Standard JSON cannot be encoded")
	}
	prepared, err = verify.PrepareStandardJSON(encoded, language, compilerVersion, identifier, maximum)
	if err != nil {
		return "", nil, invalidParameter("compiler input is invalid")
	}
	return identifier, prepared, nil
}

func mergeCompilerSettings(settings map[string]any, language verify.Language, form verificationCompilerSettings, sources []string) error {
	if language != verify.LanguageSolidity {
		return invalidParameter("unsupported compiler language")
	}
	if form.evmVersion != "" {
		if existing, exists := settings["evmVersion"]; exists && existing != form.evmVersion {
			return invalidParameter("evmVersion conflicts with Standard JSON settings")
		}
		settings["evmVersion"] = form.evmVersion
	}
	if form.optimizationSet || form.runsSet {
		optimizer, err := objectSetting(settings, "optimizer")
		if err != nil {
			return err
		}
		if form.optimizationSet {
			if existing, exists := optimizer["enabled"]; exists && existing != form.optimized {
				return invalidParameter("optimizationUsed conflicts with Standard JSON settings")
			}
			optimizer["enabled"] = form.optimized
		}
		if form.runsSet {
			if existing, exists := optimizer["runs"]; exists && !sameJSONNumber(existing, form.runs) {
				return invalidParameter("runs conflicts with Standard JSON settings")
			}
			optimizer["runs"] = form.runs
		}
		settings["optimizer"] = optimizer
	}
	if err := mergeLibraries(settings, form.libraries, sources); err != nil {
		return err
	}
	return nil
}

func objectSetting(settings map[string]any, name string) (map[string]any, error) {
	value, exists := settings[name]
	if !exists {
		return make(map[string]any), nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalidParameter("Standard JSON %s setting must be an object", name)
	}
	return object, nil
}

func sameJSONNumber(value any, want uint64) bool {
	switch value := value.(type) {
	case float64:
		return value >= 0 && value == float64(want)
	case json.Number:
		return value.String() == strconv.FormatUint(want, 10)
	default:
		return false
	}
}

func mergeLibraries(settings map[string]any, additions []verificationLibrary, sources []string) error {
	if len(additions) == 0 {
		return nil
	}
	libraries := make(map[string]map[string]string)
	if existing, exists := settings["libraries"]; exists {
		encoded, err := json.Marshal(existing)
		if err != nil || json.Unmarshal(encoded, &libraries) != nil {
			return invalidParameter("Standard JSON libraries must map source and library names to addresses")
		}
	}
	for _, library := range additions {
		source, name, err := contractIdentifier(library.name, "", len(sources) != 1)
		if err != nil {
			if len(sources) != 1 {
				return invalidParameter("library %q must be source-qualified for multi-file input", library.name)
			}
			source, name, err = contractIdentifier(library.name, sources[0], false)
			if err != nil {
				return err
			}
		}
		if !containsString(sources, source) {
			return invalidParameter("library source %q is not present in compiler input", source)
		}
		if libraries[source] == nil {
			libraries[source] = make(map[string]string)
		}
		if existing := libraries[source][name]; existing != "" && !strings.EqualFold(existing, library.address) {
			return invalidParameter("library %q conflicts with Standard JSON settings", library.name)
		}
		libraries[source][name] = library.address
	}
	settings["libraries"] = libraries
	return nil
}

func contractIdentifier(raw, fallbackSource string, sourceRequired bool) (string, string, error) {
	raw = strings.TrimSpace(raw)
	separator := strings.LastIndex(raw, ":")
	if separator < 0 {
		if sourceRequired || fallbackSource == "" {
			return "", "", invalidParameter("contractname must be source:name")
		}
		if !compilerIdentifierPattern.MatchString(raw) {
			return "", "", invalidParameter("contractname contains an invalid contract name")
		}
		return fallbackSource, raw, nil
	}
	source, name := strings.TrimSpace(raw[:separator]), strings.TrimSpace(raw[separator+1:])
	if source == "" || len(source) > 384 || !compilerIdentifierPattern.MatchString(name) {
		return "", "", invalidParameter("contractname must be a valid source:name identifier")
	}
	return source, name, nil
}

func stripConstructorArguments(creation, arguments string, maximum int) (string, error) {
	normalized, err := normalizeVerificationHex(creation, "canonical creation bytecode", maximum)
	if err != nil || normalized == "" {
		return "", ErrVerificationTargetUnavailable
	}
	if arguments != "" {
		if len(arguments) > len(normalized) || !strings.HasSuffix(normalized, arguments) {
			return "", invalidParameter("constructorArguments do not match the canonical creation input")
		}
		normalized = strings.TrimSuffix(normalized, arguments)
		if normalized == "" {
			return "", invalidParameter("constructorArguments consume the entire canonical creation input")
		}
	}
	return "0x" + normalized, nil
}

func normalizeVerificationHex(raw, name string, maximum int) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0x")
	if len(raw)%2 != 0 || len(raw)/2 > maximum {
		return "", invalidParameter("%s must be even-length hexadecimal within the input limit", name)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", invalidParameter("%s must be hexadecimal", name)
	}
	return strings.ToLower(raw), nil
}

func validateVerificationFormKeys(values url.Values) error {
	allowed := map[string]struct{}{
		"chainid": {}, "module": {}, "action": {}, "apikey": {},
		"contractaddress": {}, "sourceCode": {}, "codeformat": {},
		"contractname": {}, "compilerversion": {}, "optimizationUsed": {},
		"runs": {}, "constructorArguments": {}, "constructorArguements": {},
		"evmVersion": {}, "evmversion": {}, "licenseType": {},
	}
	for index := 1; index <= 10; index++ {
		allowed[fmt.Sprintf("libraryname%d", index)] = struct{}{}
		allowed[fmt.Sprintf("libraryaddress%d", index)] = struct{}{}
	}
	for key, entries := range values {
		if _, exists := allowed[key]; !exists {
			return invalidParameter("unsupported verification parameter %q", key)
		}
		if len(entries) != 1 {
			return invalidParameter("verification parameter %q must appear exactly once", key)
		}
	}
	return nil
}

func oneVerificationValue(values url.Values, name string, required bool) (string, error) {
	entries, exists := values[name]
	if !exists {
		if required {
			return "", invalidParameter("%s is required", name)
		}
		return "", nil
	}
	if len(entries) != 1 {
		return "", invalidParameter("%s must appear exactly once", name)
	}
	value := strings.TrimSpace(entries[0])
	if required && value == "" {
		return "", invalidParameter("%s is required", name)
	}
	return value, nil
}

func aliasedVerificationValue(values url.Values, primary, alias string) (string, error) {
	left, leftErr := oneVerificationValue(values, primary, false)
	right, rightErr := oneVerificationValue(values, alias, false)
	if leftErr != nil || rightErr != nil {
		return "", errors.Join(leftErr, rightErr)
	}
	if left != "" && right != "" && left != right {
		return "", invalidParameter("%s and %s conflict", primary, alias)
	}
	if left != "" {
		return left, nil
	}
	return right, nil
}

func validVerificationGUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func containsString(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}

const verificationTargetSQL = `
WITH current_code AS (
    SELECT observation.code_hash, observation.block_hash,
           observation.block_number, observation.code
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND observation.canonical = TRUE
    ORDER BY observation.block_number DESC,
             observation.observed_at DESC,
             observation.code_hash DESC
    LIMIT 1
)
SELECT current_code.code_hash, current_code.block_hash, current_code.code,
       creation.creation_bytecode
FROM current_code
LEFT JOIN LATERAL (
    SELECT candidate.creation_bytecode
    FROM (
        SELECT inclusion.raw->>'input' AS creation_bytecode,
               receipt.block_number, receipt.tx_index,
               ''::text AS trace_path, 0 AS source_rank
        FROM receipts AS receipt
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = receipt.chain_id
         AND canonical.number = receipt.block_number
         AND canonical.block_hash = receipt.block_hash
        JOIN transaction_inclusions AS inclusion
          ON inclusion.chain_id = receipt.chain_id
         AND inclusion.block_number = receipt.block_number
         AND inclusion.block_hash = receipt.block_hash
         AND inclusion.tx_index = receipt.tx_index
        WHERE receipt.chain_id = $1::numeric
          AND lower(receipt.raw->>'contractAddress') = $3
          AND receipt.block_number <= current_code.block_number
          AND inclusion.raw->>'input' IS NOT NULL

        UNION ALL

        SELECT '0x' || encode(trace.input, 'hex') AS creation_bytecode,
               trace.block_number, trace.transaction_index,
               trace.trace_path, 1 AS source_rank
        FROM normalized_traces AS trace
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = trace.chain_id
         AND canonical.number = trace.block_number
         AND canonical.block_hash = trace.block_hash
        WHERE trace.chain_id = $1::numeric
          AND trace.created_address = $2
          AND trace.canonical = TRUE
          AND trace.reverted = FALSE
          AND trace.input IS NOT NULL
          AND trace.block_number <= current_code.block_number
    ) AS candidate
    ORDER BY candidate.block_number DESC, candidate.tx_index DESC,
             candidate.source_rank DESC, candidate.trace_path DESC
       LIMIT 1
    ) AS creation ON TRUE`

const proxyCurrentStateSQL = `
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), latest_raw AS (
    SELECT observation.*, tip.number AS context_number,
           tip.block_hash AS context_hash
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.*
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = $2
          AND observation.canonical = TRUE
          AND observation.stage_version = 2
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
), published_raw AS (
    SELECT raw.*, generation.id AS observation_generation_id,
           generation.durable_job_id AS observation_durable_job_id,
           generation.job_generation AS observation_job_generation
    FROM latest_raw AS raw
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = raw.chain_id
          AND witness.proxy_address = raw.proxy_address
          AND witness.observation_block_hash = raw.block_hash
          AND witness.observation_stage_version = raw.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_raw AS (
    SELECT raw.*
    FROM published_raw AS raw
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = raw.chain_id
          AND evidence.address = raw.proxy_address
          AND evidence.code_hash = raw.proxy_code_hash
          AND evidence.candidate_kind = 'proxy'
          AND evidence.stage_version = raw.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= raw.context_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND raw.proxy_pattern = 'clone'
              AND raw.evidence_state = 'exact'
              AND octet_length(raw.immutable_args) > 0
              AND raw.details->>'immutable_args_creation_authenticated' = 'true'
          )
          AND (
              evidence.block_number > raw.block_number OR (
                  evidence.block_number = raw.block_number
                  AND evidence.block_hash = raw.block_hash
                  AND evidence.durable_job_id = raw.observation_durable_job_id
                  AND evidence.job_generation >= raw.observation_job_generation
              )
          )
    )
), resolved_proxy AS (
    SELECT raw.*, resolution.id AS artifact_resolution_id,
           resolution.proxy_artifact_job_id,
           resolution.implementation_artifact_job_id,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_kind
                ELSE resolution.proxy_kind END AS effective_kind,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_pattern
                WHEN resolution.proxy_pattern = 'uups' THEN 'erc1967'
                ELSE resolution.proxy_pattern END AS effective_pattern,
           CASE WHEN raw.proxy_pattern = 'clone' THEN NULL::text
                ELSE resolution.standard_version END AS effective_standard,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_address
                ELSE resolution.implementation_address END AS effective_implementation,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_code_hash
                ELSE resolution.implementation_code_hash END AS effective_implementation_hash,
           resolution.admin_address AS effective_admin,
           resolution.admin_code_hash AS effective_admin_hash,
           resolution.beacon_address AS effective_beacon,
           resolution.beacon_code_hash AS effective_beacon_hash
    FROM unshadowed_raw AS raw
    LEFT JOIN LATERAL (
        SELECT candidate.*
        FROM proxy_artifact_resolutions AS candidate
        JOIN published_block_stage_results AS published
          ON published.chain_id = candidate.chain_id
         AND published.block_hash = candidate.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = candidate.observation_stage_version
         AND published.durable_job_id = candidate.durable_job_id
         AND published.job_generation = candidate.job_generation
         AND published.state = 'complete'
        WHERE candidate.chain_id = raw.chain_id
          AND candidate.proxy_address = raw.proxy_address
          AND candidate.observation_block_hash = raw.block_hash
          AND candidate.observation_stage_version = raw.stage_version
          AND candidate.proxy_code_hash = raw.proxy_code_hash
          AND candidate.proxy_pattern <> 'uups'
        ORDER BY candidate.id DESC
        LIMIT 1
    ) AS resolution ON raw.proxy_pattern <> 'clone'
    WHERE (raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact')
       OR resolution.id IS NOT NULL
), resolved_epoch AS (
    SELECT proxy.*,
           COALESCE(code_epoch.block_number, 0::numeric) AS implementation_epoch_block
    FROM resolved_proxy AS proxy
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = proxy.chain_id
          AND change.address = proxy.effective_implementation
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
    ) AS code_epoch ON TRUE
), latest_uups_probe AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           latest.block_number, latest.block_hash,
           latest.implementation_code_hash, latest.verification_job_id,
           latest.standard_version, latest.probe_state,
           latest.proxiable_uuid, latest.upgrade_interface_version,
           latest.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT observation.*,
                   generation.id AS uups_generation_id
            FROM uups_implementation_observations AS observation
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = observation.chain_id
             AND canonical.number = observation.block_number
             AND canonical.block_hash = observation.block_hash
            JOIN uups_implementation_observation_generations AS generation
              ON generation.chain_id = observation.chain_id
             AND generation.implementation_address = observation.implementation_address
             AND generation.observation_block_hash = observation.block_hash
             AND generation.observation_stage_version = observation.stage_version
             AND generation.verification_job_id = observation.verification_job_id
            JOIN published_block_stage_results AS published
              ON published.chain_id = generation.chain_id
             AND published.block_hash = generation.observation_block_hash
             AND published.stage = 'proxy'
             AND published.stage_version = generation.observation_stage_version
             AND published.durable_job_id = generation.durable_job_id
             AND published.job_generation = generation.job_generation
             AND published.state = 'complete'
            WHERE observation.chain_id = proxy.chain_id
              AND observation.implementation_address = proxy.effective_implementation
              AND observation.implementation_code_hash = proxy.effective_implementation_hash
              AND observation.stage_version = 2
              AND observation.canonical = TRUE
              AND observation.block_number <= proxy.context_number
            ORDER BY observation.block_number DESC,
                     observation.block_hash DESC,
                     generation.id DESC,
                     observation.verification_job_id DESC
            LIMIT 1
        ) AS candidate
        WHERE NOT EXISTS (
            SELECT 1
            FROM uups_implementation_observations AS conflict
            JOIN canonical_blocks AS conflict_canonical
              ON conflict_canonical.chain_id = conflict.chain_id
             AND conflict_canonical.number = conflict.block_number
             AND conflict_canonical.block_hash = conflict.block_hash
            JOIN uups_implementation_observation_generations AS conflict_generation
              ON conflict_generation.chain_id = conflict.chain_id
             AND conflict_generation.implementation_address = conflict.implementation_address
             AND conflict_generation.observation_block_hash = conflict.block_hash
             AND conflict_generation.observation_stage_version = conflict.stage_version
             AND conflict_generation.verification_job_id = conflict.verification_job_id
            JOIN published_block_stage_results AS conflict_published
              ON conflict_published.chain_id = conflict_generation.chain_id
             AND conflict_published.block_hash = conflict_generation.observation_block_hash
             AND conflict_published.stage = 'proxy'
             AND conflict_published.stage_version = conflict_generation.observation_stage_version
             AND conflict_published.durable_job_id = conflict_generation.durable_job_id
             AND conflict_published.job_generation = conflict_generation.job_generation
             AND conflict_published.state = 'complete'
            WHERE conflict.chain_id = candidate.chain_id
              AND conflict.implementation_address = candidate.implementation_address
              AND conflict.implementation_code_hash = candidate.implementation_code_hash
              AND conflict.block_number = candidate.block_number
              AND conflict.block_hash = candidate.block_hash
              AND conflict.stage_version = candidate.stage_version
              AND conflict.canonical = TRUE
              AND (
                  conflict.probe_state || ':' ||
                  COALESCE(conflict.rejection_reason, '')
              ) IS DISTINCT FROM (
                  candidate.probe_state || ':' ||
                  COALESCE(candidate.rejection_reason, '')
              )
        )
    ) AS latest ON proxy.effective_pattern = 'erc1967'
), uups_overlay AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           probe.verification_job_id AS implementation_artifact_job_id,
           probe.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN latest_uups_probe AS probe
      ON probe.chain_id = proxy.chain_id
     AND probe.proxy_address = proxy.proxy_address
    JOIN verified_contract_proxy_artifacts AS artifact
      ON artifact.verification_job_id = probe.verification_job_id
     AND artifact.chain_id = probe.chain_id
     AND artifact.address = proxy.effective_implementation
     AND artifact.code_hash = proxy.effective_implementation_hash
     AND artifact.artifact_kind = 'uups_implementation'
     AND artifact.standard_version = '5.6.1'
     AND artifact.runtime_immutable_address = proxy.effective_implementation
    JOIN verified_contracts AS verified
      ON verified.chain_id = artifact.chain_id
     AND verified.address = artifact.address
     AND verified.code_hash = artifact.code_hash
     AND verified.valid_from_block = artifact.valid_from_block
     AND verified.verification_job_id = artifact.verification_job_id
     AND verified.request_digest = artifact.request_digest
    JOIN verification_jobs AS artifact_job
      ON artifact_job.id = artifact.verification_job_id
     AND artifact_job.kind = 'address'
     AND artifact_job.chain_id = artifact.chain_id
     AND artifact_job.address = artifact.address
     AND artifact_job.code_hash = artifact.code_hash
     AND artifact_job.status = 'succeeded'
    JOIN blocks AS artifact_block
      ON artifact_block.chain_id = artifact_job.chain_id
     AND artifact_block.hash = artifact_job.block_hash
    JOIN canonical_blocks AS artifact_canonical
      ON artifact_canonical.chain_id = artifact_block.chain_id
     AND artifact_canonical.number = artifact_block.number
     AND artifact_canonical.block_hash = artifact_block.hash
    WHERE proxy.effective_pattern = 'erc1967'
      AND probe.probe_state = 'compatible'
      AND probe.standard_version = '5.6.1'
      AND probe.proxiable_uuid = decode(
          '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
          'hex'
      )
      AND probe.upgrade_interface_version = '5.0.0'
      AND probe.block_number >= proxy.implementation_epoch_block
      AND artifact.valid_from_block >= proxy.implementation_epoch_block
      AND artifact.valid_from_block <= proxy.context_number
      AND (verified.valid_to_block IS NULL OR
           verified.valid_to_block >= proxy.context_number)
      AND proxy_interaction_coverage_contains(
          proxy.chain_id,
          probe.block_number, probe.block_hash,
          proxy.context_number, proxy.context_hash
      )
), effective_proxy AS (
    SELECT proxy.*,
           CASE WHEN overlay.uups_generation_id IS NOT NULL THEN 'uups'
                ELSE proxy.effective_pattern END AS current_pattern,
           CASE WHEN overlay.uups_generation_id IS NOT NULL
                THEN overlay.implementation_artifact_job_id
                ELSE NULL::uuid END AS current_implementation_artifact_job_id,
           overlay.uups_generation_id
    FROM resolved_epoch AS proxy
    LEFT JOIN uups_overlay AS overlay
      ON overlay.chain_id = proxy.chain_id
     AND overlay.proxy_address = proxy.proxy_address
), latest_beacon AS (
    SELECT observation.*, proxy.context_number AS proxy_context_number
    FROM effective_proxy AS proxy
    JOIN LATERAL (
        SELECT observation.*
        FROM beacon_implementation_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = proxy.chain_id
          AND observation.beacon_address = proxy.effective_beacon
          AND observation.beacon_code_hash = proxy.effective_beacon_hash
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= proxy.context_number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON proxy.current_pattern = 'beacon'
), published_beacon AS (
    SELECT beacon.*, generation.id AS beacon_generation_id,
           generation.durable_job_id AS beacon_durable_job_id,
           generation.job_generation AS beacon_job_generation
    FROM latest_beacon AS beacon
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = beacon.chain_id
          AND witness.beacon_address = beacon.beacon_address
          AND witness.observation_block_hash = beacon.block_hash
          AND witness.observation_stage_version = beacon.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_beacon AS (
    SELECT beacon.*
    FROM published_beacon AS beacon
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = beacon.chain_id
          AND evidence.address = beacon.beacon_address
          AND evidence.code_hash = beacon.beacon_code_hash
          AND evidence.candidate_kind = 'beacon'
          AND evidence.stage_version = beacon.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= beacon.proxy_context_number
          AND (
              evidence.block_number > beacon.block_number OR (
                  evidence.block_number = beacon.block_number
                  AND evidence.block_hash = beacon.block_hash
                  AND evidence.durable_job_id = beacon.beacon_durable_job_id
                  AND evidence.job_generation >= beacon.beacon_job_generation
              )
          )
    )
), current_proxy AS (
    SELECT proxy.block_number, proxy.proxy_code_hash, proxy.block_hash,
           proxy.effective_kind AS proxy_kind,
           proxy.current_pattern AS proxy_pattern,
           proxy.effective_standard AS standard_version,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_address
                ELSE proxy.effective_implementation END AS implementation_address,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_code_hash
                ELSE proxy.effective_implementation_hash END AS implementation_code_hash,
           proxy.effective_admin AS admin_address,
           proxy.effective_admin_hash AS admin_code_hash,
           proxy.effective_beacon AS beacon_address,
           proxy.effective_beacon_hash AS beacon_code_hash,
           proxy.observation_generation_id, proxy.artifact_resolution_id,
           proxy.proxy_artifact_job_id,
           proxy.current_implementation_artifact_job_id AS implementation_artifact_job_id,
           beacon.beacon_generation_id, proxy.uups_generation_id,
           proxy.context_number,
           proxy.context_hash,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN 'proxy_admin'
               WHEN 'beacon' THEN 'upgradeable_beacon'
               ELSE 'none'
           END AS management_kind,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin
               WHEN 'beacon' THEN proxy.effective_beacon
               ELSE NULL
           END AS management_address,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin_hash
               WHEN 'beacon' THEN proxy.effective_beacon_hash
               ELSE NULL
           END AS management_code_hash
    FROM effective_proxy AS proxy
    LEFT JOIN unshadowed_beacon AS beacon
      ON proxy.current_pattern = 'beacon'
     AND beacon.beacon_address = proxy.effective_beacon
    WHERE proxy.current_pattern <> 'beacon' OR beacon.beacon_generation_id IS NOT NULL
), expected_identity(address, code_hash, epoch_block) AS (
    SELECT DISTINCT identity.address, identity.code_hash,
           COALESCE(code_epoch.block_number, 0::numeric)
    FROM current_proxy
    CROSS JOIN LATERAL (VALUES
        ($2::bytea, current_proxy.proxy_code_hash),
        (current_proxy.implementation_address, current_proxy.implementation_code_hash),
        (current_proxy.admin_address, current_proxy.admin_code_hash),
        (current_proxy.beacon_address, current_proxy.beacon_code_hash),
        (current_proxy.management_address, current_proxy.management_code_hash)
    ) AS identity(address, code_hash)
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = $1::numeric
          AND change.address = identity.address
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= current_proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM
              lower(change.after_value)
    ) AS code_epoch ON TRUE
    WHERE identity.address IS NOT NULL
), current_identity AS (
    SELECT expected.address, expected.code_hash,
           current_code.code_hash AS current_code_hash
    FROM canonical_tip AS tip
    CROSS JOIN expected_identity AS expected
    LEFT JOIN LATERAL (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = expected.address
          AND observation.canonical = TRUE
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC,
                 observation.observed_at DESC,
                 observation.code_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
 )`

const proxyVerificationTargetSQL = proxyCurrentStateSQL + `
, reusable_binding AS (
    SELECT binding.verification_job_id
    FROM current_proxy
    JOIN verified_proxy_bindings AS binding
      ON binding.chain_id = $1::numeric
     AND binding.proxy_address = $2
     AND binding.observation_stage_version = 2
     AND binding.observation_block_number = current_proxy.block_number
     AND binding.observation_block_hash = current_proxy.block_hash
     AND binding.observation_generation_id = current_proxy.observation_generation_id
     AND binding.artifact_resolution_id IS NOT DISTINCT FROM
         current_proxy.artifact_resolution_id
     AND binding.beacon_generation_id IS NOT DISTINCT FROM
         current_proxy.beacon_generation_id
     AND binding.uups_generation_id IS NOT DISTINCT FROM
         current_proxy.uups_generation_id
     AND binding.proxy_code_hash = current_proxy.proxy_code_hash
     AND binding.proxy_kind = current_proxy.proxy_kind
     AND binding.proxy_pattern = current_proxy.proxy_pattern
     AND binding.standard_version IS NOT DISTINCT FROM current_proxy.standard_version
     AND binding.implementation_address = current_proxy.implementation_address
     AND binding.implementation_code_hash = current_proxy.implementation_code_hash
     AND binding.admin_address IS NOT DISTINCT FROM current_proxy.admin_address
     AND binding.admin_code_hash IS NOT DISTINCT FROM current_proxy.admin_code_hash
     AND binding.beacon_address IS NOT DISTINCT FROM current_proxy.beacon_address
     AND binding.beacon_code_hash IS NOT DISTINCT FROM current_proxy.beacon_code_hash
     AND binding.management_kind = current_proxy.management_kind
     AND binding.management_address IS NOT DISTINCT FROM
         current_proxy.management_address
     AND binding.management_code_hash IS NOT DISTINCT FROM
         current_proxy.management_code_hash
    JOIN canonical_blocks AS binding_context
      ON binding_context.chain_id = binding.chain_id
     AND binding_context.number = binding.context_block_number
     AND binding_context.block_hash = binding.context_block_hash
    WHERE proxy_interaction_coverage_contains(
              binding.chain_id,
              binding.observation_block_number,
              binding.observation_block_hash,
              current_proxy.context_number,
              current_proxy.context_hash
          )
      AND NOT EXISTS (
        SELECT 1
        FROM (VALUES
            (binding.proxy_address, binding.proxy_code_hash),
            (binding.implementation_address, binding.implementation_code_hash),
            (binding.admin_address, binding.admin_code_hash),
            (binding.beacon_address, binding.beacon_code_hash),
            (binding.management_address, binding.management_code_hash)
        ) AS identity(address, code_hash)
        JOIN contract_code_observations AS observation
          ON observation.chain_id = binding.chain_id
         AND observation.address = identity.address
         AND observation.canonical = TRUE
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE identity.address IS NOT NULL
          AND observation.block_number > binding.context_block_number
          AND observation.block_number <= current_proxy.context_number
          AND observation.code_hash IS DISTINCT FROM identity.code_hash
    )
      AND NOT EXISTS (
          SELECT 1
          FROM expected_identity AS identity
          JOIN transaction_state_changes AS change
            ON change.chain_id = binding.chain_id
           AND change.address = identity.address
           AND change.field_kind = 'code'
           AND change.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = change.chain_id
           AND canonical.number = change.block_number
           AND canonical.block_hash = change.block_hash
          WHERE change.block_number > binding.context_block_number
            AND change.block_number <= current_proxy.context_number
            AND lower(change.before_value) IS DISTINCT FROM
                lower(change.after_value)
      )
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
    LIMIT 1
)
SELECT current_proxy.proxy_code_hash, current_proxy.block_hash,
       current_proxy.context_number::text, current_proxy.context_hash,
       current_proxy.proxy_kind, current_proxy.proxy_pattern,
       current_proxy.standard_version, current_proxy.implementation_address,
       current_proxy.implementation_code_hash, current_proxy.admin_address,
       current_proxy.admin_code_hash, current_proxy.beacon_address,
       current_proxy.beacon_code_hash, current_proxy.management_kind,
       current_proxy.management_address, current_proxy.management_code_hash,
       current_proxy.observation_generation_id,
       current_proxy.artifact_resolution_id,
       current_proxy.beacon_generation_id,
       current_proxy.uups_generation_id,
       current_proxy.proxy_pattern = 'clone' OR (
           EXISTS (
               SELECT 1
               FROM expected_identity AS identity
               JOIN verified_contracts AS verified
                 ON verified.chain_id = $1::numeric
                AND verified.address = identity.address
                AND verified.code_hash = identity.code_hash
               WHERE identity.address = $2
                 AND identity.code_hash = current_proxy.proxy_code_hash
                 AND verified.valid_from_block >= identity.epoch_block
                 AND verified.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
           AND EXISTS (
               SELECT 1
               FROM verified_contract_proxy_artifacts AS artifact
               JOIN verified_contracts AS verified
                 ON verified.chain_id = artifact.chain_id
                AND verified.address = artifact.address
                AND verified.code_hash = artifact.code_hash
                AND verified.valid_from_block = artifact.valid_from_block
                AND verified.verification_job_id = artifact.verification_job_id
                AND verified.request_digest = artifact.request_digest
               JOIN expected_identity AS identity
                 ON identity.address = artifact.address
                AND identity.code_hash = artifact.code_hash
               WHERE artifact.verification_job_id =
                     current_proxy.proxy_artifact_job_id
                 AND artifact.chain_id = $1::numeric
                 AND artifact.address = $2
                 AND artifact.code_hash = current_proxy.proxy_code_hash
                 AND artifact.standard_version = '5.6.1'
                 AND artifact.artifact_kind = CASE current_proxy.proxy_pattern
                     WHEN 'erc1967' THEN 'erc1967_proxy'
                     WHEN 'transparent' THEN 'transparent_proxy'
                     WHEN 'uups' THEN 'erc1967_proxy'
                     WHEN 'beacon' THEN 'beacon_proxy'
                 END
                 AND artifact.valid_from_block >= identity.epoch_block
                 AND artifact.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
       ),
       EXISTS (
           SELECT 1
           FROM expected_identity AS identity
           JOIN verified_contracts AS verified
             ON verified.chain_id = $1::numeric
            AND verified.address = identity.address
            AND verified.code_hash = identity.code_hash
           WHERE identity.address = current_proxy.implementation_address
             AND identity.code_hash = current_proxy.implementation_code_hash
             AND verified.valid_from_block >= identity.epoch_block
             AND verified.valid_from_block <= current_proxy.context_number
             AND (verified.valid_to_block IS NULL
                  OR verified.valid_to_block >= current_proxy.context_number)
       ) AND (
           current_proxy.proxy_pattern <> 'uups' OR EXISTS (
               SELECT 1
               FROM verified_contract_proxy_artifacts AS artifact
               JOIN verified_contracts AS verified
                 ON verified.chain_id = artifact.chain_id
                AND verified.address = artifact.address
                AND verified.code_hash = artifact.code_hash
                AND verified.valid_from_block = artifact.valid_from_block
                AND verified.verification_job_id = artifact.verification_job_id
                AND verified.request_digest = artifact.request_digest
               JOIN expected_identity AS identity
                 ON identity.address = artifact.address
                AND identity.code_hash = artifact.code_hash
               WHERE artifact.verification_job_id =
                     current_proxy.implementation_artifact_job_id
                 AND artifact.chain_id = $1::numeric
                 AND artifact.address = current_proxy.implementation_address
                 AND artifact.code_hash = current_proxy.implementation_code_hash
                 AND artifact.standard_version = '5.6.1'
                 AND artifact.artifact_kind = 'uups_implementation'
                 AND artifact.valid_from_block >= identity.epoch_block
                 AND artifact.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
       ),
       current_proxy.management_kind = 'none' OR EXISTS (
           SELECT 1
           FROM verified_contract_proxy_artifacts AS artifact
           JOIN verified_contracts AS verified
             ON verified.chain_id = artifact.chain_id
            AND verified.address = artifact.address
            AND verified.code_hash = artifact.code_hash
            AND verified.valid_from_block = artifact.valid_from_block
            AND verified.verification_job_id = artifact.verification_job_id
            AND verified.request_digest = artifact.request_digest
           JOIN expected_identity AS identity
             ON identity.address = artifact.address
            AND identity.code_hash = artifact.code_hash
           WHERE artifact.chain_id = $1::numeric
             AND artifact.address = current_proxy.management_address
             AND artifact.code_hash = current_proxy.management_code_hash
             AND artifact.standard_version = '5.6.1'
             AND artifact.artifact_kind = CASE current_proxy.management_kind
                 WHEN 'proxy_admin' THEN 'proxy_admin'
                 WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
             END
             AND artifact.valid_from_block >= identity.epoch_block
             AND artifact.valid_from_block <= current_proxy.context_number
             AND (verified.valid_to_block IS NULL
                  OR verified.valid_to_block >= current_proxy.context_number)
       ),
       (SELECT binding.verification_job_id::text FROM reusable_binding AS binding)
FROM current_proxy
WHERE proxy_interaction_coverage_contains(
          $1::numeric,
          current_proxy.block_number,
          current_proxy.block_hash,
          current_proxy.context_number,
          current_proxy.context_hash
      )
  AND NOT EXISTS (
    SELECT 1
    FROM current_identity AS identity
    WHERE identity.current_code_hash IS DISTINCT FROM identity.code_hash
)
`
