package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/apiops"
	"gopkg.in/yaml.v3"
)

const (
	errorResponseRef  = "#/components/responses/Error"
	opaqueCursorRef   = "#/components/schemas/OpaqueCursor"
	maximumUint256Dec = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
)

func TestOpenAPIContractFoundation(t *testing.T) {
	t.Parallel()
	document := loadOpenAPI(t)
	root := document.Content[0]

	assertScalar(t, mappingValue(t, root, "openapi"), "3.0.3")
	servers := mappingValue(t, root, "servers")
	if servers.Kind != yaml.SequenceNode || len(servers.Content) != 1 {
		t.Fatalf("servers must contain exactly the same-origin API base, got kind=%d entries=%d", servers.Kind, len(servers.Content))
	}
	assertScalar(t, mappingValue(t, servers.Content[0], "url"), "/api/v1")

	components := mappingValue(t, root, "components")
	schemas := mappingValue(t, components, "schemas")
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "Quantity"), "type"), "string")
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "Quantity"), "pattern"), `^(0|[1-9][0-9]*)$`)
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "Quantity"), "maxLength"), "78")
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "BillingAggregateQuantity"), "type"), "string")
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "BillingAggregateQuantity"), "pattern"), `^(0|[1-9][0-9]*)$`)
	assertScalar(t, mappingValue(t, mappingValue(t, schemas, "BillingAggregateQuantity"), "maxLength"), "97")

	address := mappingValue(t, schemas, "Address")
	assertScalar(t, mappingValue(t, address, "type"), "string")
	assertScalar(t, mappingValue(t, address, "pattern"), `^0x[0-9a-fA-F]{40}$`)
	if description := scalarValue(t, mappingValue(t, address, "description")); !strings.Contains(description, "EIP-55") {
		t.Fatalf("Address description must require checksummed responses, got %q", description)
	}

	hash := mappingValue(t, schemas, "Hash")
	assertScalar(t, mappingValue(t, hash, "type"), "string")
	assertScalar(t, mappingValue(t, hash, "pattern"), `^0x[0-9a-fA-F]{64}$`)
	if description := scalarValue(t, mappingValue(t, hash, "description")); !strings.Contains(description, "lowercase") {
		t.Fatalf("Hash description must require normalized response values, got %q", description)
	}

	cursor := mappingValue(t, schemas, "OpaqueCursor")
	assertScalar(t, mappingValue(t, cursor, "type"), "string")
	assertScalar(t, mappingValue(t, cursor, "maxLength"), "1024")
	parameters := mappingValue(t, components, "parameters")
	assertScalar(t, mappingValue(t, mappingValue(t, mappingValue(t, parameters, "Cursor"), "schema"), "$ref"), opaqueCursorRef)
	for _, schemaName := range []string{"Meta", "PendingMeta"} {
		properties := mappingValue(t, mappingValue(t, schemas, schemaName), "properties")
		assertScalar(t, mappingValue(t, mappingValue(t, properties, "next_cursor"), "$ref"), opaqueCursorRef)
	}

	assertRequired(t, mappingValue(t, schemas, "APIError"), "code", "message", "request_id")
	assertRequired(t, mappingValue(t, schemas, "ErrorResponse"), "error")
	transactionProperties := mappingValue(t, mappingValue(t, schemas, "Transaction"), "properties")
	for _, field := range []string{"base_fee_per_gas", "blob_base_fee_per_gas"} {
		assertScalar(t, mappingValue(t, mappingValue(t, transactionProperties, field), "$ref"), "#/components/schemas/Quantity")
	}
	originProperties := mappingValue(t, mappingValue(t, schemas, "AddressOrigin"), "properties")
	assertEnum(t, mappingValue(t, originProperties, "state"), "found", "genesis", "not_found", "unavailable")
	assertSuccessEnvelopes(t, schemas)
	paths := mappingValue(t, root, "paths")
	assertJSONOperationsUseCommonErrors(t, paths)
	assertVerificationBoundary(t, paths, schemas)
	assertProxyInteractionBoundary(t, paths, components, schemas)
	billingProperties := mappingValue(t, mappingValue(t, schemas, "BillingConfig"), "properties")
	billingRouteLimit := mappingValue(t, mappingValue(t, billingProperties, "routes"), "maxItems")
	assertScalar(t, billingRouteLimit, strconv.Itoa(len(apiops.EligibleIDs())))

	responses := mappingValue(t, components, "responses")
	commonError := mappingValue(t, responses, "Error")
	errorContent := mappingValue(t, mappingValue(t, commonError, "content"), "application/json")
	assertScalar(t, mappingValue(t, mappingValue(t, errorContent, "schema"), "$ref"), "#/components/schemas/ErrorResponse")
}

func TestGenesisAddressOriginOmitsTransactionIdentities(t *testing.T) {
	t.Parallel()
	origin := gen.AddressOrigin{Kind: gen.Funding, State: gen.AddressOriginStateGenesis}
	encoded, err := json.Marshal(origin)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"kind":"funding","state":"genesis"}`; got != want {
		t.Fatalf("genesis origin JSON=%s, want %s", got, want)
	}
	if !gen.AddressOriginStateGenesis.Valid() {
		t.Fatal("generated genesis origin state is not valid")
	}
}

func assertProxyInteractionBoundary(
	t *testing.T,
	paths, components, schemas *yaml.Node,
) {
	t.Helper()
	for name, want := range map[string][]string{
		"ProxyDetailStatus": {
			"not_detected", "detected_unverified", "verified", "unavailable", "failed",
		},
		"ProxyMechanism":            {"eip1167", "eip1967", "beacon"},
		"ProxyPattern":              {"clone", "erc1967", "transparent", "uups", "beacon", "unknown"},
		"ProxyManagementKind":       {"proxy_admin", "upgradeable_beacon"},
		"ProxyHistoryCoverageState": {"complete", "partial"},
	} {
		assertEnum(t, mappingValue(t, schemas, name), want...)
	}
	parameters := mappingValue(t, components, "parameters")
	historyLimit := mappingValue(t, mappingValue(t, parameters, "ProxyHistoryLimit"), "schema")
	assertScalar(t, mappingValue(t, historyLimit, "type"), "integer")
	assertScalar(t, mappingValue(t, historyLimit, "minimum"), "1")
	assertScalar(t, mappingValue(t, historyLimit, "maximum"), "100")
	assertScalar(t, mappingValue(t, historyLimit, "default"), "20")

	details := mappingValue(t, schemas, "ProxyDetails")
	assertRequired(t, details, "address", "status", "snapshot", "evidence")
	detailProperties := mappingValue(t, details, "properties")
	for _, identity := range []string{"proxy", "implementation", "admin", "beacon"} {
		assertScalar(
			t,
			mappingValue(t, mappingValue(t, detailProperties, identity), "$ref"),
			"#/components/schemas/ProxyContractIdentity",
		)
	}
	assertScalar(t, mappingValue(t, mappingValue(t, detailProperties, "binding_id"), "format"), "uuid")

	for _, operation := range []struct {
		path      string
		operation string
		response  string
		paginated bool
	}{
		{path: "/contracts/{address}/proxy", operation: "getContractProxy", response: "ProxyDetailsResponse"},
		{path: "/contracts/{address}/proxy/upgrades", operation: "listContractProxyUpgrades", response: "ProxyUpgradeHistoryResponse", paginated: true},
		{path: "/contracts/{address}/proxy/initializations", operation: "listContractProxyInitializations", response: "ProxyInitializationHistoryResponse", paginated: true},
		{path: "/contracts/{address}/proxy/diamond-cuts", operation: "listContractDiamondCuts", response: "DiamondCutHistoryResponse", paginated: true},
	} {
		defined := mappingValue(t, mappingValue(t, paths, operation.path), "get")
		assertScalar(t, mappingValue(t, defined, "operationId"), operation.operation)
		response := mappingValue(t, mappingValue(t, mappingValue(t, defined, "responses"), "200"), "content")
		response = mappingValue(t, mappingValue(t, response, "application/json"), "schema")
		assertScalar(t, mappingValue(t, response, "$ref"), "#/components/schemas/"+operation.response)
		if operation.paginated {
			foundLimit := false
			for _, parameter := range mappingValue(t, defined, "parameters").Content {
				if ref := optionalMappingValue(parameter, "$ref"); ref != nil &&
					scalarValue(t, ref) == "#/components/parameters/ProxyHistoryLimit" {
					foundLimit = true
				}
			}
			if !foundLimit {
				t.Fatalf("%s does not use the proxy history limit", operation.operation)
			}
		}
	}
}

func assertVerificationBoundary(t *testing.T, paths, schemas *yaml.Node) {
	t.Helper()
	for _, operation := range []struct {
		path     string
		method   string
		billable bool
	}{
		{path: "/contracts/{address}/verification", method: "post"},
		{path: "/verifier/jobs/{id}", method: "get", billable: true},
		{path: "/verifier/solidity/multipart", method: "post"},
		{path: "/verifier/solidity/standard-json", method: "post"},
		{path: "/verifier/solidity/batch/multipart", method: "post"},
		{path: "/verifier/solidity/batch/standard-json", method: "post"},
		{path: "/verifier/sourcify", method: "post"},
		{path: "/verifier/sourcify/from-etherscan", method: "post"},
	} {
		security := mappingValue(t, mappingValue(t, mappingValue(t, paths, operation.path), operation.method), "security")
		wantRequirements := 1
		if operation.billable {
			wantRequirements = 2
		}
		if security.Kind != yaml.SequenceNode || len(security.Content) != wantRequirements {
			t.Fatalf("%s %s must declare %d security requirements", operation.method, operation.path, wantRequirements)
		}
		mappingValue(t, security.Content[0], "APIKey")
		if operation.billable {
			mappingValue(t, security.Content[1], "X402Payment")
		}
	}
	for _, operation := range []struct {
		path   string
		method string
	}{
		{path: "/contracts/{address}/verification", method: "get"},
		{path: "/contracts/{address}/proxy", method: "get"},
		{path: "/contracts/{address}/proxy/upgrades", method: "get"},
		{path: "/contracts/{address}/proxy/initializations", method: "get"},
		{path: "/contracts/{address}/proxy/diamond-cuts", method: "get"},
	} {
		defined := mappingValue(t, mappingValue(t, paths, operation.path), operation.method)
		if security := optionalMappingValue(defined, "security"); security != nil {
			t.Fatalf("%s %s must be anonymous, got security=%v", operation.method, operation.path, security.Value)
		}
		parameters := optionalMappingValue(defined, "parameters")
		if parameters != nil {
			for _, parameter := range parameters.Content {
				if ref := optionalMappingValue(parameter, "$ref"); ref != nil &&
					scalarValue(t, ref) == "#/components/parameters/PaymentSignature" {
					t.Fatalf("%s %s exposes a payment signature", operation.method, operation.path)
				}
			}
		}
		responses := mappingValue(t, defined, "responses")
		if optionalMappingValue(responses, "402") != nil {
			t.Fatalf("%s %s exposes a payment-required response", operation.method, operation.path)
		}
		for index := 0; index < len(responses.Content); index += 2 {
			if headers := optionalMappingValue(responses.Content[index+1], "headers"); headers != nil && optionalMappingValue(headers, "PAYMENT-RESPONSE") != nil {
				t.Fatalf("%s %s exposes a payment-response header", operation.method, operation.path)
			}
		}
	}

	for _, schemaName := range []string{
		"AddressVerificationSubmission",
		"VerifierMultipartRequest",
		"VerifierStandardJSONRequest",
	} {
		properties := mappingValue(t, mappingValue(t, schemas, schemaName), "properties")
		for _, forbidden := range []string{
			"address", "code_hash", "at_block_hash", "contract_identifier",
			"constructor_arguments", "submit_to_sourcify",
		} {
			if optionalMappingValue(properties, forbidden) != nil {
				t.Fatalf("%s exposes server-owned field %q", schemaName, forbidden)
			}
		}
	}

	for _, removed := range []string{
		"/verification/jobs", "/verification/jobs/{id}",
		"/sourcify/contracts/{address}", "/sourcify/imports",
		"/verification/jobs/{id}/sourcify", "/sourcify/jobs/{verification_id}",
		"/verifier/vyper/multipart", "/verifier/vyper/standard-json",
	} {
		if optionalMappingValue(paths, removed) != nil {
			t.Fatalf("removed verifier path %q is still public", removed)
		}
	}
}

func TestGeneratedGoContractsUseStringScalarsAndNativeEnvelopes(t *testing.T) {
	t.Parallel()
	quantity := gen.Quantity(maximumUint256Dec)
	address := gen.Address("0x52908400098527886E0F7030069857D2E4169EE7")
	response := gen.ErrorResponse{Error: gen.APIError{
		Code: "capability_unavailable", Message: "capability unavailable", RequestId: "request-1",
		Details: &map[string]any{"quantity": quantity, "address": address},
	}}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"error":{`, `"request_id":"request-1"`, `"quantity":"` + maximumUint256Dec + `"`,
		`"address":"0x52908400098527886E0F7030069857D2E4169EE7"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("generated Go contract JSON %s is missing %s", text, expected)
		}
	}
}

func loadOpenAPI(t *testing.T) *yaml.Node {
	t.Helper()
	path := filepath.Join("..", "..", "api", "openapi.yaml")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		t.Fatalf("%s does not contain exactly one YAML document", path)
	}
	assertNoDuplicateMappingKeys(t, &document, "openapi")
	return &document
}

func assertNoDuplicateMappingKeys(t *testing.T, node *yaml.Node, path string) {
	t.Helper()
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for index, child := range node.Content {
			assertNoDuplicateMappingKeys(t, child, path+"["+strconv.Itoa(index)+"]")
		}
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode {
				t.Fatalf("%s contains a non-scalar mapping key", path)
			}
			if _, exists := seen[key.Value]; exists {
				t.Fatalf("%s contains duplicate key %q", path, key.Value)
			}
			seen[key.Value] = struct{}{}
			assertNoDuplicateMappingKeys(t, value, path+"."+key.Value)
		}
	}
}

func assertSuccessEnvelopes(t *testing.T, schemas *yaml.Node) {
	t.Helper()
	for index := 0; index < len(schemas.Content); index += 2 {
		name, schema := schemas.Content[index].Value, schemas.Content[index+1]
		if !strings.HasSuffix(name, "Response") || name == "ErrorResponse" {
			continue
		}
		assertRequired(t, schema, "data", "meta")
		properties := mappingValue(t, schema, "properties")
		mappingValue(t, properties, "data")
		mappingValue(t, properties, "meta")
	}
}

func assertJSONOperationsUseCommonErrors(t *testing.T, paths *yaml.Node) {
	t.Helper()
	for pathIndex := 0; pathIndex < len(paths.Content); pathIndex += 2 {
		item := paths.Content[pathIndex+1]
		for methodIndex := 0; methodIndex < len(item.Content); methodIndex += 2 {
			method, operation := item.Content[methodIndex].Value, item.Content[methodIndex+1]
			if method != "get" && method != "post" && method != "put" && method != "patch" && method != "delete" {
				continue
			}
			responses := mappingValue(t, operation, "responses")
			if !hasJSONSuccessResponse(responses) {
				continue
			}
			fallback := mappingValue(t, responses, "default")
			assertScalar(t, mappingValue(t, fallback, "$ref"), errorResponseRef)
		}
	}
}

func hasJSONSuccessResponse(responses *yaml.Node) bool {
	for index := 0; index < len(responses.Content); index += 2 {
		status, response := responses.Content[index].Value, responses.Content[index+1]
		if len(status) != 3 || status[0] != '2' {
			continue
		}
		content := optionalMappingValue(response, "content")
		if content != nil && optionalMappingValue(content, "application/json") != nil {
			return true
		}
	}
	return false
}

func assertRequired(t *testing.T, schema *yaml.Node, names ...string) {
	t.Helper()
	required := mappingValue(t, schema, "required")
	if required.Kind != yaml.SequenceNode {
		t.Fatalf("required must be a sequence, got kind %d", required.Kind)
	}
	values := make(map[string]struct{}, len(required.Content))
	for _, value := range required.Content {
		values[value.Value] = struct{}{}
	}
	for _, name := range names {
		if _, ok := values[name]; !ok {
			t.Fatalf("required is missing %q", name)
		}
	}
}

func assertEnum(t *testing.T, schema *yaml.Node, expected ...string) {
	t.Helper()
	values := mappingValue(t, schema, "enum")
	if values.Kind != yaml.SequenceNode {
		t.Fatalf("enum must be a sequence, got kind %d", values.Kind)
	}
	actual := make([]string, 0, len(values.Content))
	for _, value := range values.Content {
		actual = append(actual, scalarValue(t, value))
	}
	if !slices.Equal(actual, expected) {
		t.Fatalf("enum=%v, want %v", actual, expected)
	}
}

func mappingValue(t *testing.T, node *yaml.Node, key string) *yaml.Node {
	t.Helper()
	value := optionalMappingValue(node, key)
	if value == nil {
		t.Fatalf("mapping is missing key %q", key)
	}
	return value
}

func optionalMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func assertScalar(t *testing.T, node *yaml.Node, expected string) {
	t.Helper()
	if actual := scalarValue(t, node); actual != expected {
		t.Fatalf("scalar = %q, want %q", actual, expected)
	}
}

func scalarValue(t *testing.T, node *yaml.Node) string {
	t.Helper()
	if node == nil || node.Kind != yaml.ScalarNode {
		t.Fatalf("expected scalar node, got %#v", node)
	}
	return node.Value
}
