// Package apiops defines the single static identity, request schema, and
// billing metadata for native OpenAPI operations. HTTP routing and billing
// consume these values rather than infer policy from hostile request paths.
package apiops

import "slices"

const DefaultBillableResponseBytes int64 = 8 << 20

type ID string

type ParameterLocation string

const (
	ParameterPath  ParameterLocation = "path"
	ParameterQuery ParameterLocation = "query"
)

type ParameterType string

const (
	ParameterAddress         ParameterType = "address"
	ParameterBillingState    ParameterType = "billing_state"
	ParameterBlockIdentifier ParameterType = "block_identifier"
	ParameterEVMNetwork      ParameterType = "evm_network"
	ParameterHash            ParameterType = "hash"
	ParameterInteger         ParameterType = "integer"
	ParameterOpaqueCursor    ParameterType = "opaque_cursor"
	ParameterRFC3339         ParameterType = "rfc3339"
	ParameterText            ParameterType = "text"
	ParameterUint256         ParameterType = "uint256"
	ParameterUUID            ParameterType = "uuid"
)

type RepetitionPolicy string

const (
	// RepetitionReject requires exactly one value whenever the parameter is
	// present. Path parameters are intrinsically singular but retain the same
	// explicit policy so the catalog has no implicit duplicate semantics.
	RepetitionReject RepetitionPolicy = "reject"
)

// ParameterSpec is the canonical resource schema for one path or query
// parameter. Bounds count UTF-8 bytes at the HTTP boundary. DefaultValue is
// meaningful only when HasDefault is true.
type ParameterSpec struct {
	Name         string
	In           ParameterLocation
	Type         ParameterType
	Required     bool
	Repetition   RepetitionPolicy
	HasDefault   bool
	DefaultValue string
	HasMinimum   bool
	Minimum      uint64
	HasMaximum   bool
	Maximum      uint64
	MinimumBytes int
	MaximumBytes int
	TrimSpace    bool
}

type Spec struct {
	ID               ID
	Method           string
	OpenAPIPath      string
	MuxPattern       string
	Parameters       []ParameterSpec
	BillingEligible  bool
	MaxResponseBytes int64
}

var catalog = []Spec{
	spec("getStatus", "GET", "/status", false),
	spec("getPublicConfig", "GET", "/config", false),
	spec("listGenesisAccounts", "GET", "/genesis/accounts", false,
		cursorParameter(), limitParameter("25")),
	spec("createAuthChallenge", "POST", "/auth/challenge", false),
	spec("verifyAuthChallenge", "POST", "/auth/verify", false),
	spec("getAuthSession", "GET", "/auth/session", false),
	spec("logoutAuthSession", "POST", "/auth/logout", false),
	spec("updateCurrentUser", "PATCH", "/users/me", false),
	spec("listAdminUsers", "GET", "/admin/users", false,
		cursorParameter(), limitParameter("25")),
	spec("updateAdminUser", "PATCH", "/admin/users/{id}", false,
		pathParameter("id", ParameterUUID)),
	spec("revokeAdminUserSessions", "POST", "/admin/users/{id}/sessions/revoke", false,
		pathParameter("id", ParameterUUID)),
	spec("getBillingConfig", "GET", "/billing/config", false),
	spec("listCurrentUserBillingPayments", "GET", "/billing/payments", false,
		cursorParameter(), limitParameter("25")),
	spec("listAdminBillingPayments", "GET", "/admin/billing/payments", false,
		queryParameter("asset", ParameterAddress),
		cursorParameter(),
		queryParameter("from_time", ParameterRFC3339),
		limitParameter("25"),
		networkParameter(),
		boundedTextParameter("operation", false, 1, 128, false),
		queryParameter("state", ParameterBillingState),
		queryParameter("to_time", ParameterRFC3339)),
	spec("getAdminBillingSummary", "GET", "/admin/billing/summary", false,
		queryParameter("asset", ParameterAddress),
		queryParameter("from_time", ParameterRFC3339),
		networkParameter(),
		boundedTextParameter("operation", false, 1, 128, false),
		queryParameter("state", ParameterBillingState),
		queryParameter("to_time", ParameterRFC3339)),
	spec("listBlocks", "GET", "/blocks", true,
		cursorParameter(), limitParameter("25")),
	spec("getBlock", "GET", "/blocks/{id}", true,
		pathParameter("id", ParameterBlockIdentifier)),
	spec("listTransactions", "GET", "/transactions", true,
		cursorParameter(), limitParameter("25")),
	spec("getTransaction", "GET", "/transactions/{hash}", true,
		pathParameter("hash", ParameterHash)),
	spec("listPendingTransactions", "GET", "/pending", true,
		cursorParameter(), limitParameter("25")),
	spec("getTransactionTrace", "GET", "/transactions/{hash}/trace", true,
		pathParameter("hash", ParameterHash)),
	spec("listTransactionTokenTransfers", "GET", "/transactions/{hash}/token-transfers", true,
		pathParameter("hash", ParameterHash),
		cursorParameter(), limitParameter("25")),
	spec("listTransactionLogs", "GET", "/transactions/{hash}/logs", true,
		pathParameter("hash", ParameterHash),
		cursorParameter(), limitParameter("25")),
	spec("listTransactionStateChanges", "GET", "/transactions/{hash}/state-changes", true,
		pathParameter("hash", ParameterHash),
		cursorParameter(), limitParameter("25")),
	spec("getAddress", "GET", "/addresses/{address}", true,
		pathParameter("address", ParameterAddress)),
	spec("listAddressTransactions", "GET", "/addresses/{address}/transactions", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("listAddressInternalTransactions", "GET", "/addresses/{address}/internal-transactions", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("listAddressERC20Transfers", "GET", "/addresses/{address}/erc20-transfers", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("listAddressNFTTransfers", "GET", "/addresses/{address}/nft-transfers", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("listAddressNFTBalances", "GET", "/addresses/{address}/nfts", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("listTokens", "GET", "/tokens", true,
		cursorParameter(), limitParameter("25")),
	spec("getToken", "GET", "/tokens/{address}", true,
		pathParameter("address", ParameterAddress)),
	spec("listTokenTransfers", "GET", "/tokens/{address}/transfers", true,
		pathParameter("address", ParameterAddress),
		cursorParameter(), limitParameter("25")),
	spec("getNFTOwner", "GET", "/nfts/{address}/{token_id}", true,
		pathParameter("address", ParameterAddress),
		pathParameter("token_id", ParameterUint256)),
	spec("getNFTMedia", "GET", "/nfts/{address}/{token_id}/media", false,
		pathParameter("address", ParameterAddress),
		pathParameter("token_id", ParameterUint256)),
	spec("getBlockStats", "GET", "/stats/blocks", true,
		requiredQueryParameter("from_block", ParameterUint256),
		requiredQueryParameter("to_block", ParameterUint256)),
	spec("getAggregateStats", "GET", "/stats/summary", true,
		requiredQueryParameter("from_block", ParameterUint256),
		requiredQueryParameter("to_block", ParameterUint256)),
	spec("getChartOverview", "GET", "/stats/charts/overview", true),
	spec("getChartMetric", "GET", "/stats/charts/{metric}", true,
		pathParameter("metric", ParameterText),
		requiredQueryParameter("from_time", ParameterRFC3339),
		chartIntervalParameter(),
		requiredQueryParameter("to_time", ParameterRFC3339)),
	spec("search", "GET", "/search", true,
		cursorParameter(),
		limitParameter("20"),
		boundedTextParameter("q", true, 1, 256, true)),
	spec("streamHeadEvents", "GET", "/events", false),
	spec("streamHomeSnapshots", "GET", "/home/stream", false),
	spec("getVerifierJob", "GET", "/verifier/jobs/{id}", true,
		pathParameter("id", ParameterUUID)),
	spec("getVerifiedContract", "GET", "/contracts/{address}/verification", true,
		pathParameter("address", ParameterAddress)),
	spec("submitAddressVerification", "POST", "/contracts/{address}/verification", false,
		pathParameter("address", ParameterAddress)),
	spec("verifySolidityMultipart", "POST", "/verifier/solidity/multipart", false),
	spec("verifySolidityStandardJson", "POST", "/verifier/solidity/standard-json", false),
	spec("batchVerifySolidityMultipart", "POST", "/verifier/solidity/batch/multipart", false),
	spec("batchVerifySolidityStandardJson", "POST", "/verifier/solidity/batch/standard-json", false),
	spec("verifyVyperMultipart", "POST", "/verifier/vyper/multipart", false),
	spec("verifyVyperStandardJson", "POST", "/verifier/vyper/standard-json", false),
	spec("listVerifierCompilers", "GET", "/verifier/compilers", false,
		requiredQueryParameter("language", ParameterText)),
	spec("lookupVerifierMethods", "POST", "/verifier/lookup-methods", false),
	spec("submitSourcifyVerification", "POST", "/verifier/sourcify", false),
	spec("submitSourcifyFromEtherscan", "POST", "/verifier/sourcify/from-etherscan", false),
}

func spec(
	id, method, path string,
	eligible bool,
	parameters ...ParameterSpec,
) Spec {
	maximum := int64(0)
	if eligible {
		maximum = DefaultBillableResponseBytes
	}
	return Spec{
		ID: ID(id), Method: method, OpenAPIPath: path,
		MuxPattern:      method + " /api/v1" + path,
		Parameters:      slices.Clone(parameters),
		BillingEligible: eligible, MaxResponseBytes: maximum,
	}
}

func pathParameter(name string, parameterType ParameterType) ParameterSpec {
	return ParameterSpec{
		Name: name, In: ParameterPath, Type: parameterType,
		Required: true, Repetition: RepetitionReject,
	}
}

func queryParameter(name string, parameterType ParameterType) ParameterSpec {
	return ParameterSpec{
		Name: name, In: ParameterQuery, Type: parameterType,
		Repetition: RepetitionReject,
	}
}

func requiredQueryParameter(name string, parameterType ParameterType) ParameterSpec {
	parameter := queryParameter(name, parameterType)
	parameter.Required = true
	return parameter
}

func cursorParameter() ParameterSpec {
	parameter := queryParameter("cursor", ParameterOpaqueCursor)
	parameter.MinimumBytes = 1
	parameter.MaximumBytes = 1024
	return parameter
}

func limitParameter(defaultValue string) ParameterSpec {
	parameter := queryParameter("limit", ParameterInteger)
	parameter.HasDefault = true
	parameter.DefaultValue = defaultValue
	parameter.HasMinimum = true
	parameter.Minimum = 1
	parameter.HasMaximum = true
	parameter.Maximum = 100
	return parameter
}

func chartIntervalParameter() ParameterSpec {
	parameter := queryParameter("interval", ParameterText)
	parameter.HasDefault = true
	parameter.DefaultValue = "auto"
	parameter.MinimumBytes = 3
	parameter.MaximumBytes = 5
	return parameter
}

func networkParameter() ParameterSpec {
	parameter := queryParameter("network", ParameterEVMNetwork)
	parameter.MaximumBytes = 96
	return parameter
}

func boundedTextParameter(
	name string,
	required bool,
	minimumBytes, maximumBytes int,
	trimSpace bool,
) ParameterSpec {
	parameter := queryParameter(name, ParameterText)
	parameter.Required = required
	parameter.MinimumBytes = minimumBytes
	parameter.MaximumBytes = maximumBytes
	parameter.TrimSpace = trimSpace
	return parameter
}

func All() []Spec {
	result := make([]Spec, len(catalog))
	for index := range catalog {
		result[index] = cloneSpec(catalog[index])
	}
	return result
}

func Lookup(id string) (Spec, bool) {
	for _, operation := range catalog {
		if string(operation.ID) == id {
			return cloneSpec(operation), true
		}
	}
	return Spec{}, false
}

func cloneSpec(operation Spec) Spec {
	operation.Parameters = slices.Clone(operation.Parameters)
	return operation
}

func EligibleIDs() []string {
	var result []string
	for _, operation := range catalog {
		if operation.BillingEligible {
			result = append(result, string(operation.ID))
		}
	}
	return result
}
