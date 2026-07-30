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
// block-scoped proxy observation. ExpectedImplementation is always normalized
// to ImplementationAddress before persistence so equivalent requests are
// idempotent.
type ProxyVerificationTarget struct {
	Kind                   string `json:"kind"`
	ImplementationAddress  string `json:"implementation_address"`
	ImplementationCodeHash string `json:"implementation_code_hash"`
	ExpectedImplementation string `json:"expected_implementation"`
}
