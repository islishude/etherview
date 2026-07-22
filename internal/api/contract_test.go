package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
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
	assertSuccessEnvelopes(t, schemas)
	paths := mappingValue(t, root, "paths")
	assertJSONOperationsUseCommonErrors(t, paths)
	assertVerificationBoundary(t, paths, schemas)

	responses := mappingValue(t, components, "responses")
	commonError := mappingValue(t, responses, "Error")
	errorContent := mappingValue(t, mappingValue(t, commonError, "content"), "application/json")
	assertScalar(t, mappingValue(t, mappingValue(t, errorContent, "schema"), "$ref"), "#/components/schemas/ErrorResponse")
}

func assertVerificationBoundary(t *testing.T, paths, schemas *yaml.Node) {
	t.Helper()
	for _, operation := range []struct {
		path   string
		method string
	}{
		{path: "/verification/jobs", method: "post"},
		{path: "/verification/jobs/{id}", method: "get"},
		{path: "/contracts/{address}/verification", method: "get"},
		{path: "/sourcify/contracts/{address}", method: "get"},
		{path: "/sourcify/imports", method: "post"},
		{path: "/verification/jobs/{id}/sourcify", method: "post"},
		{path: "/sourcify/jobs/{verification_id}", method: "get"},
	} {
		security := mappingValue(t, mappingValue(t, mappingValue(t, paths, operation.path), operation.method), "security")
		if security.Kind != yaml.SequenceNode || len(security.Content) != 1 {
			t.Fatalf("%s %s must declare exactly one API-key security requirement", operation.method, operation.path)
		}
		mappingValue(t, security.Content[0], "APIKey")
	}

	for _, schemaName := range []string{"VerificationSubmission", "SourcifyImportRequest"} {
		properties := mappingValue(t, mappingValue(t, schemas, schemaName), "properties")
		for _, forbidden := range []string{"code_hash", "at_block_hash", "creation_bytecode", "runtime_bytecode"} {
			if optionalMappingValue(properties, forbidden) != nil {
				t.Fatalf("%s exposes server-owned field %q", schemaName, forbidden)
			}
		}
		mappingValue(t, properties, "address")
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
