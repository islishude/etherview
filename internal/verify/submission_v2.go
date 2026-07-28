package verify

import "encoding/json"

type SubmissionV2 struct {
	Kind                 JobKind             `json:"kind"`
	Language             Language            `json:"language,omitempty"`
	CompilerVersion      string              `json:"compiler_version,omitempty"`
	StandardJSON         json.RawMessage     `json:"standard_json,omitempty"`
	StandardJSONVariants []json.RawMessage   `json:"standard_json_variants,omitempty"`
	Multipart            *MultipartRequest   `json:"-"`
	Bytecodes            []BytecodePair      `json:"bytecodes,omitempty"`
	ContractNameHint     string              `json:"contract_name_hint,omitempty"`
	Target               *VerificationTarget `json:"target,omitempty"`
	SourcifyRequest      json.RawMessage     `json:"sourcify_request,omitempty"`
	CatalogGenerationID  int64               `json:"catalog_generation_id,omitempty"`
	CompilerPlatform     string              `json:"compiler_platform,omitempty"`
	CompilerDigest       string              `json:"compiler_sha256,omitempty"`
	RunnerDigest         string              `json:"runner_sha256,omitempty"`
}
