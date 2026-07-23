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
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/verify"
	"golang.org/x/crypto/sha3"
)

const defaultVerificationInputBytes = 5 << 20

var compilerIdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)

// VerificationService is the public subset of verify.Service used by the
// Etherscan compatibility boundary. Production wiring supplies the same
// durable service used by the native API only when public verification is
// enabled.
type VerificationService interface {
	Submit(context.Context, verify.Request) (verify.VerificationJob, bool, error)
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
	creation, err := stripConstructorArguments(target.creationBytecode, form.constructorArguments, maximum)
	if err != nil {
		return "", err
	}
	request := verify.Request{
		ChainID: b.chainID, Address: strings.ToLower(address),
		CodeHash:    "0x" + hex.EncodeToString(target.codeHash),
		AtBlockHash: "0x" + hex.EncodeToString(target.blockHash),
		Language:    form.language, CompilerVersion: form.compilerVersion,
		ContractIdentifier: form.contractIdentifier, StandardJSON: form.standardJSON,
		CreationBytecode: creation,
		RuntimeBytecode:  "0x" + hex.EncodeToString(target.runtimeBytecode),
		ConstructorArgs:  form.constructorArguments, LicenseType: form.licenseType,
		SubmitToSourcify: false,
	}
	job, _, err := b.verification.Submit(ctx, request)
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
	if !found {
		return "", ErrVerificationJobNotFound
	}
	switch job.Status {
	case verify.JobQueued, verify.JobRunning:
		return "", ErrPending
	case verify.JobSucceeded:
		if job.ResultKind == nil {
			return "", errors.New("succeeded verification job has no result kind")
		}
		if *job.ResultKind == verify.MatchMismatch {
			return "", ErrVerificationFailed
		}
		if *job.ResultKind != verify.MatchExact && *job.ResultKind != verify.MatchMetadataOnly {
			return "", errors.New("succeeded verification job has an invalid result kind")
		}
		return "Pass - Verified", nil
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
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(target.runtimeBytecode)
	if !bytes.Equal(hasher.Sum(nil), target.codeHash) {
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
	case "vyper-json":
		language = verify.LanguageVyper
		compilerVersion = strings.TrimPrefix(compilerVersion, "vyper:")
	default:
		return etherscanVerificationForm{}, nil, "", invalidParameter("unsupported codeformat %q", codeFormat)
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
		if language == verify.LanguageVyper {
			return settings, invalidParameter("runs is not valid for vyper-json")
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
	if language == verify.LanguageVyper {
		wantLanguage = "Vyper"
	}
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
	if language == verify.LanguageVyper {
		base := strings.TrimSuffix(path.Base(source), path.Ext(source))
		if name != base {
			return "", nil, invalidParameter("Vyper contract name must match source filename %q", base)
		}
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
	if form.evmVersion != "" {
		if existing, exists := settings["evmVersion"]; exists && existing != form.evmVersion {
			return invalidParameter("evmVersion conflicts with Standard JSON settings")
		}
		settings["evmVersion"] = form.evmVersion
	}
	if language == verify.LanguageSolidity {
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
	if form.optimizationSet {
		if existing, exists := settings["optimize"]; exists {
			enabled, ok := vyperOptimizationEnabled(existing)
			if !ok || enabled != form.optimized {
				return invalidParameter("optimizationUsed conflicts with vyper-json settings")
			}
		} else {
			settings["optimize"] = form.optimized
		}
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

func vyperOptimizationEnabled(value any) (bool, bool) {
	switch value := value.(type) {
	case bool:
		return value, true
	case string:
		return value != "none", value == "none" || value == "gas" || value == "codesize"
	default:
		return false, false
	}
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
