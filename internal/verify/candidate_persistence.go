package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// AuthenticatedCompilation is a bounded compiler input and its complete
// candidate set. It becomes durable only with a successful address result.
type AuthenticatedCompilation struct {
	StandardJSON json.RawMessage
	Candidates   []CandidateArtifact
}

func hydrateCandidateArtifact(candidate CandidateArtifact) (CandidateArtifact, error) {
	if !validStandardJSONSourceName(candidate.FileName) ||
		!solidityContractNamePattern.MatchString(candidate.ContractName) ||
		candidate.Language != LanguageSolidity ||
		!versionPattern.MatchString(candidate.CompilerVersion) ||
		!jsonArray(candidate.ABI) || !jsonObject(candidate.CompilationArtifacts) ||
		!jsonObject(candidate.CreationCodeArtifacts) ||
		!jsonObject(candidate.RuntimeCodeArtifacts) {
		return CandidateArtifact{}, errors.New("persisted candidate identity is invalid")
	}
	creation, err := decodeBytecode(candidate.CreationBytecode)
	if err != nil || len(creation) == 0 || len(creation) > maxMatcherBytecodeBytes {
		return CandidateArtifact{}, errors.New("persisted candidate creation bytecode is invalid")
	}
	runtime, err := decodeBytecode(candidate.RuntimeBytecode)
	if err != nil || len(runtime) == 0 || len(runtime) > maxMatcherBytecodeBytes {
		return CandidateArtifact{}, errors.New("persisted candidate runtime bytecode is invalid")
	}
	creationAuxdata, creationLinks, _, err := decodePersistedCodeArtifacts(
		candidate.CreationCodeArtifacts, creation, false,
	)
	if err != nil {
		return CandidateArtifact{}, fmt.Errorf("persisted candidate creation artifacts: %w", err)
	}
	runtimeAuxdata, runtimeLinks, runtimeImmutables, err := decodePersistedCodeArtifacts(
		candidate.RuntimeCodeArtifacts, runtime, true,
	)
	if err != nil {
		return CandidateArtifact{}, fmt.Errorf("persisted candidate runtime artifacts: %w", err)
	}
	candidate.creationBytes = append([]byte(nil), creation...)
	candidate.runtimeBytes = append([]byte(nil), runtime...)
	candidate.creationAuxdata = creationAuxdata
	candidate.runtimeAuxdata = runtimeAuxdata
	candidate.creationLinks = creationLinks
	candidate.runtimeLinks = runtimeLinks
	candidate.runtimeImmutables = runtimeImmutables
	return candidate, nil
}

func decodePersistedCodeArtifacts(
	raw json.RawMessage,
	code []byte,
	withImmutables bool,
) (map[string]AuxdataValue, map[string]map[string][]bytecodeRange, map[string][]bytecodeRange, error) {
	fields, err := decodeRawJSONObject(raw)
	if err != nil {
		return nil, nil, nil, errCompilerOutputMalformed
	}
	auxdata := make(map[string]AuxdataValue)
	if value, exists := fields["cborAuxdata"]; exists && decodeStrictJSON(value, &auxdata) != nil {
		return nil, nil, nil, errCompilerOutputMalformed
	}
	links := make(map[string]map[string][]bytecodeRange)
	if value, exists := fields["linkReferences"]; exists && decodeStrictJSON(value, &links) != nil {
		return nil, nil, nil, errCompilerOutputMalformed
	}
	immutables := make(map[string][]bytecodeRange)
	if value, exists := fields["immutableReferences"]; exists && decodeStrictJSON(value, &immutables) != nil {
		return nil, nil, nil, errCompilerOutputMalformed
	}
	if !withImmutables && len(immutables) != 0 {
		return nil, nil, nil, errCompilerOutputMalformed
	}
	if err := validateLinkReferences(links, len(code)); err != nil {
		return nil, nil, nil, err
	}
	if err := validateImmutableReferences(immutables, code); err != nil {
		return nil, nil, nil, err
	}
	for id, value := range auxdata {
		expected, decodeErr := decodeBytecode(value.Value)
		if id == "" || decodeErr != nil || len(expected) == 0 ||
			value.Offset > uint64(len(code)) || uint64(len(expected)) > uint64(len(code))-value.Offset ||
			!bytes.Equal(code[value.Offset:value.Offset+uint64(len(expected))], expected) {
			return nil, nil, nil, errCompilerOutputMalformed
		}
	}
	return auxdata, links, immutables, nil
}

func validateAuthenticatedCompilation(
	job VerificationJob,
	unit AuthenticatedCompilation,
	maxBytes int,
) (AuthenticatedCompilation, error) {
	if job.Kind != JobAddress || job.RequestV2 == nil ||
		job.RequestV2.Language != LanguageSolidity || job.Compiler == nil ||
		!jsonObject(unit.StandardJSON) || len(unit.StandardJSON) > maxBytes ||
		len(unit.Candidates) == 0 || len(unit.Candidates) > maxStandardJSONSelectorEntries {
		return AuthenticatedCompilation{}, errors.New("authenticated compilation is invalid")
	}
	foundInput := false
	for _, input := range job.RequestV2.StandardJSONVariants {
		if bytes.Equal(input, unit.StandardJSON) {
			foundInput = true
			break
		}
	}
	if !foundInput {
		return AuthenticatedCompilation{}, errors.New("authenticated compilation input is not job-bound")
	}
	seen := make(map[string]struct{}, len(unit.Candidates))
	validated := AuthenticatedCompilation{
		StandardJSON: append(json.RawMessage(nil), unit.StandardJSON...),
		Candidates:   make([]CandidateArtifact, 0, len(unit.Candidates)),
	}
	totalBytes := len(unit.StandardJSON)
	for _, candidate := range unit.Candidates {
		if candidate.Language != job.RequestV2.Language ||
			candidate.CompilerVersion != normalizeCompilerVersion(job.RequestV2.CompilerVersion) {
			return AuthenticatedCompilation{}, errors.New("authenticated candidate compiler identity conflicts with job")
		}
		name := candidate.FullyQualifiedName()
		if _, exists := seen[name]; exists {
			return AuthenticatedCompilation{}, errors.New("authenticated candidate name is duplicated")
		}
		seen[name] = struct{}{}
		hydrated, err := hydrateCandidateArtifact(candidate)
		if err != nil {
			return AuthenticatedCompilation{}, err
		}
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > maxBytes-totalBytes {
			return AuthenticatedCompilation{}, errors.New("authenticated compilation exceeds configured bounds")
		}
		totalBytes += len(encoded)
		validated.Candidates = append(validated.Candidates, hydrated)
	}
	return validated, nil
}
