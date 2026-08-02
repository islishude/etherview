package verify

import "encoding/json"

type SubmissionV2 struct {
	Kind                 JobKind                  `json:"kind"`
	Language             Language                 `json:"language,omitempty"`
	CompilerVersion      string                   `json:"compiler_version,omitempty"`
	StandardJSON         json.RawMessage          `json:"standard_json,omitempty"`
	StandardJSONVariants []json.RawMessage        `json:"standard_json_variants,omitempty"`
	Multipart            *MultipartRequest        `json:"-"`
	Bytecodes            []BytecodePair           `json:"bytecodes,omitempty"`
	ContractNameHint     string                   `json:"contract_name_hint,omitempty"`
	Target               *VerificationTarget      `json:"target,omitempty"`
	ProxyTarget          *ProxyVerificationTarget `json:"proxy_target,omitempty"`
	SourcifyRequest      json.RawMessage          `json:"sourcify_request,omitempty"`
	CatalogGenerationID  int64                    `json:"catalog_generation_id,omitempty"`
	CompilerPlatform     string                   `json:"compiler_platform,omitempty"`
	CompilerDigest       string                   `json:"compiler_sha256,omitempty"`
	ExecutorKind         string                   `json:"executor_kind,omitempty"`
	ExecutionPolicy      string                   `json:"execution_policy,omitempty"`
	ExecutorDigest       string                   `json:"executor_sha256,omitempty"`
}

// ProxyVerificationTarget binds a compatibility request to one exact
// block-scoped proxy observation and the canonical tip at which it was
// submitted. The submission context creates a new code-identity epoch after a
// real transition, while the compatibility boundary reuses a still-current
// published binding across ordinary tip growth. ExpectedImplementation is
// always normalized to ImplementationAddress before persistence.
type ProxyVerificationTarget struct {
	Kind                         string `json:"kind"`
	Pattern                      string `json:"pattern"`
	StandardVersion              string `json:"standard_version,omitempty"`
	ImplementationAddress        string `json:"implementation_address"`
	ImplementationCodeHash       string `json:"implementation_code_hash"`
	AdminAddress                 string `json:"admin_address,omitempty"`
	AdminCodeHash                string `json:"admin_code_hash,omitempty"`
	BeaconAddress                string `json:"beacon_address,omitempty"`
	BeaconCodeHash               string `json:"beacon_code_hash,omitempty"`
	ManagementKind               string `json:"management_kind"`
	ManagementAddress            string `json:"management_address,omitempty"`
	ManagementCodeHash           string `json:"management_code_hash,omitempty"`
	SubmissionContextBlockNumber string `json:"submission_context_block_number"`
	SubmissionContextBlockHash   string `json:"submission_context_block_hash"`
	ObservationGenerationID      string `json:"observation_generation_id"`
	ArtifactResolutionID         string `json:"artifact_resolution_id,omitempty"`
	BeaconGenerationID           string `json:"beacon_generation_id,omitempty"`
	UUPSGenerationID             string `json:"uups_generation_id,omitempty"`
	ExpectedImplementation       string `json:"expected_implementation"`
}
