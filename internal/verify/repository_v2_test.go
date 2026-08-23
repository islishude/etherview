package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"strings"
	"testing"
)

func validVerifyRequest() Request {
	runtimeBytecode := []byte{0x60, 0x01}
	return Request{
		ChainID:            1,
		Address:            "0x" + strings.Repeat("11", 20),
		CodeHash:           "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)),
		AtBlockHash:        "0x" + strings.Repeat("33", 32),
		Language:           LanguageSolidity,
		CompilerVersion:    "0.8.30",
		ContractIdentifier: "A.sol:A",
		StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		CreationBytecode:   "0x6001",
		RuntimeBytecode:    "0x" + hex.EncodeToString(runtimeBytecode),
	}
}

func verificationID(sequence int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence)
}

func TestDecodeV2ResultFieldsRequiresRuntimeMatchForPublicationData(t *testing.T) {
	t.Parallel()
	outcome := json.RawMessage(`{
		"kind":"verification_success",
		"file_name":"A.sol",
		"contract_name":"A",
		"language":"solidity",
		"compiler_version":"0.8.30+commit.73712a01",
		"sources":{"A.sol":{"content":"contract A {}"}},
		"settings":{},
		"abi":[],
		"compilation_artifacts":{},
		"creation_code_artifacts":{},
		"runtime_code_artifacts":{},
		"creation_match":{"match_type":"partial","transformations":[],"values":{}},
		"runtime_match":{"match_type":"full","transformations":[],"values":{}},
		"libraries":{},
		"is_blueprint":false
	}`)
	fields, err := decodeV2ResultFields("verification_success", outcome)
	if err != nil {
		t.Fatal(err)
	}
	if fields.CreationMatch != "partial" || fields.RuntimeMatch != "full" ||
		fields.MatchType != VerificationMatchFull {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestDecodeV2ResultFieldsRejectsMalformedSuccess(t *testing.T) {
	t.Parallel()
	for _, outcome := range []json.RawMessage{
		json.RawMessage(`{"kind":"verification_success"}`),
		json.RawMessage(`{
			"kind":"verification_success",
			"file_name":"A.sol","contract_name":"A","language":"solidity",
			"compiler_version":"0.8.30","sources":{},"settings":{},
			"compilation_artifacts":{},"creation_code_artifacts":{},
			"runtime_code_artifacts":{},"libraries":{},
			"runtime_match":{"match_type":"mismatch","transformations":[],"values":{}},
			"is_blueprint":false
		}`),
		json.RawMessage(`{
			"kind":"verification_success",
			"file_name":"A.sol","contract_name":"A","language":"solidity",
			"compiler_version":"0.8.30","sources":{},"settings":{},
			"compilation_artifacts":{},"creation_code_artifacts":{},
			"runtime_code_artifacts":{},"libraries":{},
			"creation_match":{"match_type":"mismatch","transformations":[],"values":{}},
			"runtime_match":{"match_type":"full","transformations":[],"values":{}},
			"is_blueprint":false
		}`),
	} {
		if _, err := decodeV2ResultFields("verification_success", outcome); err == nil {
			t.Fatalf("expected malformed outcome rejection: %s", outcome)
		}
	}
}

func TestValidateAddressSuccessEvidenceFencesGenesisCreationEvidence(t *testing.T) {
	t.Parallel()
	ordinary := &VerificationTarget{}
	genesis := &VerificationTarget{GenesisPredeploy: true}
	runtimeOnly := v2ResultFields{RuntimeMatch: "full"}
	if err := validateAddressSuccessEvidence(ordinary, runtimeOnly); err != nil {
		t.Fatalf("ordinary runtime-only result changed existing semantics: %v", err)
	}
	if err := validateAddressSuccessEvidence(genesis, runtimeOnly); err != nil {
		t.Fatalf("Genesis runtime-only result: %v", err)
	}
	if err := validateAddressSuccessEvidence(genesis, v2ResultFields{
		CreationMatch: "full", RuntimeMatch: "full",
	}); err == nil {
		t.Fatal("Genesis result with a creation match was accepted")
	}
	if err := validateAddressSuccessEvidence(genesis, v2ResultFields{
		RuntimeMatch: "full", ConstructorArguments: []byte{},
	}); err == nil {
		t.Fatal("Genesis result with constructor arguments was accepted")
	}
	if err := validateAddressSuccessEvidence(ordinary, v2ResultFields{}); err == nil {
		t.Fatal("address result without a runtime match was accepted")
	}
}

func TestDecodeStoredVerificationMatchRestoresPublicationDetails(t *testing.T) {
	t.Parallel()
	details, err := decodeStoredVerificationMatch([]byte(`{
		"match_type":"partial",
		"transformations":[{"type":"replace","reason":"immutable","offset":12,"id":"1"}],
		"values":{"immutables":{"1":"0x1234"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if details == nil || details.MatchType != VerificationMatchPartial ||
		len(details.Transformations) != 1 || details.Transformations[0].Offset != 12 ||
		details.Values.Immutables["1"] != "0x1234" {
		t.Fatalf("details = %+v", details)
	}
	missing, err := decodeStoredVerificationMatch([]byte(`null`))
	if err != nil || missing != nil {
		t.Fatalf("null details = %+v, error = %v", missing, err)
	}
	if _, err := decodeStoredVerificationMatch([]byte(`{"match_type":"mismatch"}`)); err == nil {
		t.Fatal("expected invalid stored match rejection")
	}
	empty, err := decodeStoredVerificationMatch([]byte(`{
		"match_type":"full","transformations":null,"values":{}
	}`))
	if err != nil || empty == nil || empty.Transformations == nil || len(empty.Transformations) != 0 {
		t.Fatalf("null transformations = %+v, error = %v", empty, err)
	}
}

func TestProxyCompletionQueryFencesExactCurrentBinding(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.VerifyLegacyProxyVerificationCurrentTarget), " ")
	for _, required := range []string{
		"observation.stage_version = 2",
		"raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact'",
		"evidence.reason = 'immutable_args_creation_unverified' AND raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact' AND octet_length(raw.immutable_args) > 0 AND raw.details->>'immutable_args_creation_authenticated' = 'true'",
		"resolution.id IS NOT NULL",
		"current_proxy.proxy_pattern = $8",
		"current_proxy.standard_version IS NOT DISTINCT FROM $9::text",
		"current_proxy.admin_address IS NOT DISTINCT FROM $10::bytea",
		"current_proxy.beacon_address IS NOT DISTINCT FROM $12::bytea",
		"current_proxy.observation_generation_id = $17::bigint",
		"current_proxy.artifact_resolution_id IS NOT DISTINCT FROM $18::bigint",
		"current_proxy.beacon_generation_id IS NOT DISTINCT FROM $19::bigint",
		"current_proxy.uups_generation_id IS NOT DISTINCT FROM $22::bigint",
		"number = $20::numeric",
		"block_hash = $21",
		"current_proxy.block_number <= submission_context.number",
		"proxy_interaction_coverage_contains( $1::numeric, current_proxy.block_number, current_proxy.block_hash, current_proxy.context_number, current_proxy.context_hash )",
		"candidate.proxy_code_hash = raw.proxy_code_hash",
		"observation.beacon_code_hash = proxy.effective_beacon_hash",
		"observation.confidence IN ('verified', 'high')",
		"FROM contract_code_observations AS observation",
		"identity.current_code_hash IS DISTINCT FROM identity.code_hash",
		"observation.block_number > submission_context.number",
		"observation.code_hash IS DISTINCT FROM identity.code_hash",
		"FROM transaction_state_changes AS change",
		"change.block_number > submission_context.number",
		"change.block_number <= current_proxy.context_number",
		"change.field_kind = 'code'",
		"lower(change.before_value) IS DISTINCT FROM lower(change.after_value)",
		"COALESCE(code_epoch.block_number, 0::numeric)",
		"verified.valid_from_block >= publication.epoch_block",
		"artifact.valid_from_block >= identity.epoch_block",
		"artifact.verification_job_id = current_proxy.proxy_artifact_job_id",
		"current_proxy.implementation_artifact_job_id",
		"ORDER BY observation.block_number DESC, observation.block_hash DESC, generation.id DESC, observation.verification_job_id DESC LIMIT 1 ) AS candidate WHERE NOT EXISTS",
		"conflict.block_number = candidate.block_number",
		"conflict.probe_state || ':' || COALESCE(conflict.rejection_reason, '')",
		"candidate.probe_state || ':' || COALESCE(candidate.rejection_reason, '')",
		"published.state = 'complete'",
		"FROM proxy_detection_evidence AS evidence",
		"evidence.candidate_kind = 'proxy'",
		"evidence.job_generation >= raw.observation_job_generation",
		"evidence.candidate_kind = 'beacon'",
		"evidence.job_generation >= beacon.beacon_job_generation",
		"probe.probe_state = 'compatible'",
		"probe.block_number >= proxy.implementation_epoch_block",
		"artifact.valid_from_block >= proxy.implementation_epoch_block",
		"probe.block_number, probe.block_hash, proxy.context_number, proxy.context_hash",
		"FROM required_publication AS publication",
		"verified.address = publication.address",
		"verified.verification_job_id = artifact.verification_job_id",
		"verified.request_digest = artifact.request_digest",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("proxy completion query lacks %q: %s", required, query)
		}
	}
	for _, forbidden := range []string{
		"required_interaction_stage",
		"canonical_blocks AS coverage_block",
		"generate_series",
		"dense_rank()",
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("proxy completion query retains height-dependent coverage scan %q: %s", forbidden, query)
		}
	}
	if joins, complete := strings.Count(query, "published_block_stage_results AS"), strings.Count(query, "state = 'complete'"); joins != complete {
		t.Fatalf("proxy completion query complete-publication guards=%d, want one for each of %d joins: %s", complete, joins, query)
	}
}

func TestVerificationProxyReplayPersistsOnlyDirectTarget(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.VerifyLegacyVerificationProxyReplayTarget), " ")
	for _, required := range []string{
		"INSERT INTO proxy_replay_targets",
		"$1::numeric, $3::numeric, $4, $2, $5",
		"'verification_publication', $6::uuid",
		"ON CONFLICT DO NOTHING",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("verification proxy replay query lacks %q: %s", required, query)
		}
	}
	for _, forbidden := range []string{
		"proxy_observations", "proxy_artifact_resolutions", "published_block_stage_results",
		" UNION ", " LIMIT ",
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("verification proxy replay expands associations through %q: %s", forbidden, query)
		}
	}
}

func TestDerivedCreatorEpochAndHistoricalPublicationStayCodeBound(t *testing.T) {
	t.Parallel()
	epoch := strings.Join(strings.Fields(dbgen.DerivedVerifyCreatorCodeEpochStart), " ")
	for _, required := range []string{
		"observation.code_hash = $3",
		"observation.block_number = $4::numeric",
		"observation.block_hash = $5",
		"observation.code_hash <> $3",
		"max(observation.block_number)",
		"observation.block_number > COALESCE(boundary.block_number, -1::numeric)",
		"ORDER BY observation.block_number, observation.observed_at, observation.block_hash",
	} {
		if !strings.Contains(epoch, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("creator epoch query lacks %q: %s", required, epoch)
		}
	}
	publication := strings.Join(strings.Fields(dbgen.DerivedVerifyPublicationEvidence), " ")
	for _, required := range []string{
		"trace.block_number >= scan.valid_from_block",
		"scan.valid_to_block IS NULL OR trace.block_number <= scan.valid_to_block",
		"scan.creator_code_hash = ( SELECT observation.code_hash",
	} {
		if !strings.Contains(publication, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("derived publication query lacks %q: %s", required, publication)
		}
	}
	if strings.Contains(publication, "trace.block_number >= parent.valid_from_block") {
		t.Fatalf("derived publication still starts at verification publication: %s", publication)
	}
}

func TestVerificationRequestDigestV2SeparatesKinds(t *testing.T) {
	t.Parallel()
	first, _ := json.Marshal(SubmissionV2{
		Kind: JobSolidityStandardJSON, Language: LanguageSolidity,
		CompilerVersion: "0.8.30", StandardJSON: json.RawMessage(`{}`),
	})
	second, _ := json.Marshal(SubmissionV2{
		Kind: JobSolidityBatchStandardJSON, Language: LanguageSolidity,
		CompilerVersion: "0.8.30", StandardJSON: json.RawMessage(`{}`),
	})
	if string(first) == string(second) {
		t.Fatal("job kind did not affect durable request payload")
	}
}

func TestGenesisPredeployMarkerIsDurableWithoutChangingOrdinaryPayload(t *testing.T) {
	t.Parallel()
	target := VerificationTarget{
		ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
		CodeHash: "0x" + strings.Repeat("22", 32), AtBlockHash: "0x" + strings.Repeat("33", 32),
		CreationBytecode: "0x6000", RuntimeBytecode: "0x6001",
	}
	ordinaryRequest := SubmissionV2{
		Kind: JobAddress, Language: LanguageSolidity, CompilerVersion: "0.8.30",
		Bytecodes: []BytecodePair{{Creation: "0x6000", Runtime: "0x6001"}},
		Target:    &target,
	}
	ordinary, err := json.Marshal(ordinaryRequest)
	if err != nil {
		t.Fatal(err)
	}
	wantOrdinary := `{"kind":"address","language":"solidity","compiler_version":"0.8.30","bytecodes":[{"creation_bytecode":"0x6000","runtime_bytecode":"0x6001"}],"target":{"ChainID":1,"Address":"0x1111111111111111111111111111111111111111","CodeHash":"0x2222222222222222222222222222222222222222222222222222222222222222","AtBlockHash":"0x3333333333333333333333333333333333333333333333333333333333333333","CreationBytecode":"0x6000","RuntimeBytecode":"0x6001"}}`
	if string(ordinary) != wantOrdinary || strings.Contains(string(ordinary), "genesis_predeploy") {
		t.Fatalf("ordinary target payload changed: %s", ordinary)
	}
	runtimeOnlyTarget := target
	runtimeOnlyTarget.CreationBytecode = ""
	runtimeOnlyRequest := ordinaryRequest
	runtimeOnlyRequest.Target = &runtimeOnlyTarget
	runtimeOnlyRequest.Bytecodes = []BytecodePair{{Runtime: "0x6001"}}
	unmarked, err := json.Marshal(runtimeOnlyRequest)
	if err != nil {
		t.Fatal(err)
	}
	markedTarget := runtimeOnlyTarget
	markedTarget.GenesisPredeploy = true
	markedRequest := runtimeOnlyRequest
	markedRequest.Target = &markedTarget
	marked, err := json.Marshal(markedRequest)
	if err != nil {
		t.Fatal(err)
	}
	unmarkedDigest := sha256.Sum256(append([]byte("etherview:verification-request:v2\x00"), unmarked...))
	markedDigest := sha256.Sum256(append([]byte("etherview:verification-request:v2\x00"), marked...))
	if !strings.Contains(string(marked), `"genesis_predeploy":true`) ||
		unmarkedDigest == markedDigest {
		t.Fatalf("Genesis marker was not durably bound: %s", marked)
	}
}

func TestGenesisCanonicalTargetQueryRechecksAndLocksExactProof(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.VerifyLegacyVerificationCanonicalGenesisTarget), " ")
	for _, required := range []string{
		"imported.state = 'complete'",
		"genesis_canonical.number = 0",
		"genesis_canonical.block_hash = imported.block_hash",
		"account.address = observation.address",
		"octet_length(account.code) > 0",
		"account.code_hash = observation.code_hash",
		"account.code = observation.code",
		"observation.block_hash = $4",
		"observation.code = $5",
		"FOR SHARE OF observation, canonical, imported, genesis_canonical, account",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("Genesis publication query lacks %q: %s", required, query)
		}
	}
}

func TestProxyVerificationSubmissionContextChangesRequestDigest(t *testing.T) {
	t.Parallel()
	first := validProxyVerificationSubmission()
	second := validProxyVerificationSubmission()
	second.ProxyTarget.SubmissionContextBlockNumber = "10"
	second.ProxyTarget.SubmissionContextBlockHash = "0x" + strings.Repeat("15", 32)
	firstPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("etherview:verification-request:v2\x00")
	firstDigest := sha256.Sum256(append(append([]byte(nil), prefix...), firstPayload...))
	secondDigest := sha256.Sum256(append(append([]byte(nil), prefix...), secondPayload...))
	if firstDigest == secondDigest {
		t.Fatal("different canonical submission contexts shared a request digest")
	}
}

func TestValidateProxyVerificationSubmissionNormalizesExactTransparentBinding(t *testing.T) {
	t.Parallel()
	request := validProxyVerificationSubmission()
	request.Target.Address = "0x" + strings.Repeat("AA", 20)
	request.ProxyTarget.ImplementationAddress = "0x" + strings.Repeat("BB", 20)
	request.ProxyTarget.SubmissionContextBlockHash = "0x" + strings.Repeat("AB", 32)
	request.ProxyTarget.AdminAddress = "0x" + strings.Repeat("CC", 20)
	request.ProxyTarget.ManagementAddress = "0x" + strings.Repeat("CC", 20)
	request.ProxyTarget.ExpectedImplementation = "0x" + strings.Repeat("DD", 20)
	request.Language = LanguageSolidity
	request.CompilerVersion = "0.8.30"
	request.StandardJSON = json.RawMessage(`{"language":"Solidity"}`)
	request.Bytecodes = []BytecodePair{{Runtime: "0x6000"}}

	if err := validateProxyVerificationSubmission(&request); err != nil {
		t.Fatal(err)
	}
	if request.Target.Address != "0x"+strings.Repeat("aa", 20) ||
		request.ProxyTarget.ImplementationAddress != "0x"+strings.Repeat("bb", 20) ||
		request.ProxyTarget.AdminAddress != "0x"+strings.Repeat("cc", 20) ||
		request.ProxyTarget.ManagementAddress != "0x"+strings.Repeat("cc", 20) ||
		request.ProxyTarget.SubmissionContextBlockHash != "0x"+strings.Repeat("ab", 32) ||
		request.ProxyTarget.ExpectedImplementation != request.ProxyTarget.ImplementationAddress {
		t.Fatalf("normalized request = %+v", request)
	}
	if request.Language != "" || request.CompilerVersion != "" || request.StandardJSON != nil ||
		request.Bytecodes != nil {
		t.Fatalf("compiler inputs were not cleared: %+v", request)
	}
}

func TestValidateProxyVerificationSubmissionRejectsInexactBindings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*SubmissionV2)
	}{
		{name: "kind pattern mismatch", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.Pattern = "beacon"
		}},
		{name: "unknown standard", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.StandardVersion = "5.6.0"
		}},
		{name: "partial admin identity", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.AdminCodeHash = ""
		}},
		{name: "zero implementation", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ImplementationAddress = "0x" + strings.Repeat("00", 20)
		}},
		{name: "transparent without OpenZeppelin 5", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.StandardVersion = ""
		}},
		{name: "different proxy admin", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ManagementAddress = "0x" + strings.Repeat("77", 20)
		}},
		{name: "unmanaged transparent", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ManagementKind = "none"
			request.ProxyTarget.ManagementAddress = ""
			request.ProxyTarget.ManagementCodeHash = ""
		}},
		{name: "missing observation generation", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ObservationGenerationID = ""
		}},
		{name: "noncanonical observation generation", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ObservationGenerationID = "01"
		}},
		{name: "missing artifact resolution", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.ArtifactResolutionID = ""
		}},
		{name: "unexpected beacon generation", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.BeaconGenerationID = "3"
		}},
		{name: "missing submission context number", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.SubmissionContextBlockNumber = ""
		}},
		{name: "noncanonical submission context number", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.SubmissionContextBlockNumber = "01"
		}},
		{name: "malformed submission context hash", mutate: func(request *SubmissionV2) {
			request.ProxyTarget.SubmissionContextBlockHash = "0x1234"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := validProxyVerificationSubmission()
			test.mutate(&request)
			if err := validateProxyVerificationSubmission(&request); err == nil {
				t.Fatal("expected exact binding validation failure")
			}
		})
	}
}

func TestValidateProxyVerificationSubmissionAcceptsSupportedPatterns(t *testing.T) {
	t.Parallel()
	for _, target := range []ProxyVerificationTarget{
		{
			Kind: "eip1167", Pattern: "clone", ManagementKind: "none",
			ObservationGenerationID: "1",
			ImplementationAddress:   "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:  "0x" + strings.Repeat("33", 32),
		},
		{
			Kind: "cwia", Pattern: "clone", ManagementKind: "none",
			ObservationGenerationID: "1",
			ImplementationAddress:   "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:  "0x" + strings.Repeat("33", 32),
		},
		{
			Kind: "eip1967", Pattern: "erc1967", StandardVersion: "5.6.1",
			ManagementKind:          "none",
			ObservationGenerationID: "1",
			ArtifactResolutionID:    "2",
			ImplementationAddress:   "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:  "0x" + strings.Repeat("33", 32),
		},
		{
			Kind: "eip1967", Pattern: "uups", StandardVersion: "5.6.1",
			ManagementKind:          "none",
			ObservationGenerationID: "1",
			ArtifactResolutionID:    "2",
			UUPSGenerationID:        "4",
			ImplementationAddress:   "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:  "0x" + strings.Repeat("33", 32),
		},
		{
			Kind: "beacon", Pattern: "beacon", StandardVersion: "5.6.1",
			ManagementKind:          "upgradeable_beacon",
			ObservationGenerationID: "1",
			ArtifactResolutionID:    "2",
			BeaconGenerationID:      "3",
			ImplementationAddress:   "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:  "0x" + strings.Repeat("33", 32),
			BeaconAddress:           "0x" + strings.Repeat("66", 20),
			BeaconCodeHash:          "0x" + strings.Repeat("77", 32),
			ManagementAddress:       "0x" + strings.Repeat("66", 20),
			ManagementCodeHash:      "0x" + strings.Repeat("77", 32),
		},
	} {
		t.Run(target.Pattern, func(t *testing.T) {
			t.Parallel()
			target.SubmissionContextBlockNumber = "9"
			target.SubmissionContextBlockHash = "0x" + strings.Repeat("88", 32)
			request := validProxyVerificationSubmission()
			request.ProxyTarget = &target
			if err := validateProxyVerificationSubmission(&request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateProxyVerificationSubmissionRejectsPatternGenerationMismatch(t *testing.T) {
	t.Parallel()
	clone := validProxyVerificationSubmission()
	clone.ProxyTarget = &ProxyVerificationTarget{
		Kind: "eip1167", Pattern: "clone", ManagementKind: "none",
		ObservationGenerationID: "1", ArtifactResolutionID: "2",
		SubmissionContextBlockNumber: "9",
		SubmissionContextBlockHash:   "0x" + strings.Repeat("88", 32),
		ImplementationAddress:        "0x" + strings.Repeat("22", 20),
		ImplementationCodeHash:       "0x" + strings.Repeat("33", 32),
	}
	if err := validateProxyVerificationSubmission(&clone); err == nil {
		t.Fatal("clone accepted an artifact resolution generation")
	}

	beacon := validProxyVerificationSubmission()
	beacon.ProxyTarget = &ProxyVerificationTarget{
		Kind: "beacon", Pattern: "beacon", StandardVersion: "5.6.1",
		ManagementKind:               "upgradeable_beacon",
		ObservationGenerationID:      "1",
		ArtifactResolutionID:         "2",
		SubmissionContextBlockNumber: "9",
		SubmissionContextBlockHash:   "0x" + strings.Repeat("88", 32),
		ImplementationAddress:        "0x" + strings.Repeat("22", 20),
		ImplementationCodeHash:       "0x" + strings.Repeat("33", 32),
		BeaconAddress:                "0x" + strings.Repeat("66", 20),
		BeaconCodeHash:               "0x" + strings.Repeat("77", 32),
		ManagementAddress:            "0x" + strings.Repeat("66", 20),
		ManagementCodeHash:           "0x" + strings.Repeat("77", 32),
	}
	if err := validateProxyVerificationSubmission(&beacon); err == nil {
		t.Fatal("beacon accepted a missing beacon generation")
	}

	uups := validProxyVerificationSubmission()
	uups.ProxyTarget.Pattern = "uups"
	uups.ProxyTarget.ManagementKind = "none"
	uups.ProxyTarget.ManagementAddress = ""
	uups.ProxyTarget.ManagementCodeHash = ""
	uups.ProxyTarget.AdminAddress = ""
	uups.ProxyTarget.AdminCodeHash = ""
	if err := validateProxyVerificationSubmission(&uups); err == nil {
		t.Fatal("UUPS accepted a missing shared probe generation")
	}
	uups.ProxyTarget.UUPSGenerationID = "3"
	uups.ProxyTarget.Pattern = "erc1967"
	if err := validateProxyVerificationSubmission(&uups); err == nil {
		t.Fatal("ERC-1967 accepted a UUPS probe generation")
	}
}

func validProxyVerificationSubmission() SubmissionV2 {
	return SubmissionV2{
		Kind: JobProxy,
		Target: &VerificationTarget{
			ChainID:     1,
			Address:     "0x" + strings.Repeat("11", 20),
			CodeHash:    "0x" + strings.Repeat("12", 32),
			AtBlockHash: "0x" + strings.Repeat("13", 32),
		},
		ProxyTarget: &ProxyVerificationTarget{
			Kind: "eip1967", Pattern: "transparent", StandardVersion: "5.6.1",
			SubmissionContextBlockNumber: "9",
			SubmissionContextBlockHash:   "0x" + strings.Repeat("14", 32),
			ObservationGenerationID:      "1",
			ArtifactResolutionID:         "2",
			ImplementationAddress:        "0x" + strings.Repeat("22", 20),
			ImplementationCodeHash:       "0x" + strings.Repeat("23", 32),
			AdminAddress:                 "0x" + strings.Repeat("24", 20),
			AdminCodeHash:                "0x" + strings.Repeat("25", 32),
			ManagementKind:               "proxy_admin",
			ManagementAddress:            "0x" + strings.Repeat("24", 20),
			ManagementCodeHash:           "0x" + strings.Repeat("25", 32),
		},
	}
}
