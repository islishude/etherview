package apiops

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type openAPISchema struct {
	Ref       string  `yaml:"$ref"`
	Type      string  `yaml:"type"`
	Format    string  `yaml:"format"`
	Pattern   string  `yaml:"pattern"`
	Default   any     `yaml:"default"`
	Minimum   *uint64 `yaml:"minimum"`
	Maximum   *uint64 `yaml:"maximum"`
	MinLength *int    `yaml:"minLength"`
	MaxLength *int    `yaml:"maxLength"`
}

type openAPIParameter struct {
	Ref       string        `yaml:"$ref"`
	Name      string        `yaml:"name"`
	In        string        `yaml:"in"`
	Required  bool          `yaml:"required"`
	TrimSpace bool          `yaml:"x-etherview-trim-space"`
	Schema    openAPISchema `yaml:"schema"`
}

type openAPIResponse struct {
	Ref     string `yaml:"$ref"`
	Headers map[string]struct {
		Ref string `yaml:"$ref"`
	} `yaml:"headers"`
}

type openAPIOperation struct {
	OperationID string                     `yaml:"operationId"`
	Parameters  []openAPIParameter         `yaml:"parameters"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
}

type openAPIDocument struct {
	Paths      map[string]map[string]openAPIOperation `yaml:"paths"`
	Components struct {
		Parameters map[string]openAPIParameter `yaml:"parameters"`
		Schemas    map[string]openAPISchema    `yaml:"schemas"`
	} `yaml:"components"`
}

func readOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()
	document, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed openAPIDocument
	if err := yaml.Unmarshal(document, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestCatalogMatchesOpenAPIOperations(t *testing.T) {
	parsed := readOpenAPI(t)
	actual := make(map[string]string)
	for path, methods := range parsed.Paths {
		for method, operation := range methods {
			if operation.OperationID == "" {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			if prior, exists := actual[key]; exists {
				t.Fatalf("duplicate OpenAPI operation %s: %s and %s", key, prior, operation.OperationID)
			}
			actual[key] = operation.OperationID
		}
	}
	if len(actual) != len(catalog) {
		t.Fatalf("OpenAPI has %d operations; catalog has %d", len(actual), len(catalog))
	}
	seenIDs := make(map[ID]struct{}, len(catalog))
	for _, operation := range catalog {
		if _, exists := seenIDs[operation.ID]; exists {
			t.Fatalf("duplicate catalog operation ID %q", operation.ID)
		}
		seenIDs[operation.ID] = struct{}{}
		key := operation.Method + " " + operation.OpenAPIPath
		if got := actual[key]; got != string(operation.ID) {
			t.Errorf("%s operationId=%q, want %q", key, got, operation.ID)
		}
		if operation.MuxPattern != operation.Method+" /api/v1"+operation.OpenAPIPath {
			t.Errorf("%s has inconsistent mux pattern %q", operation.ID, operation.MuxPattern)
		}
		if operation.BillingEligible != (operation.MaxResponseBytes > 0) {
			t.Errorf("%s eligibility and response bound disagree", operation.ID)
		}
		assertCatalogParameterIntegrity(t, operation)
	}
}

func assertCatalogParameterIntegrity(t *testing.T, operation Spec) {
	t.Helper()
	seen := make(map[string]struct{}, len(operation.Parameters))
	var queryNames []string
	var pathNames []string
	for _, parameter := range operation.Parameters {
		key := string(parameter.In) + ":" + parameter.Name
		if _, exists := seen[key]; exists {
			t.Errorf("%s has duplicate parameter %s", operation.ID, key)
		}
		seen[key] = struct{}{}
		if parameter.Repetition != RepetitionReject {
			t.Errorf("%s parameter %s has implicit repetition policy", operation.ID, key)
		}
		switch parameter.In {
		case ParameterPath:
			pathNames = append(pathNames, parameter.Name)
			if !parameter.Required || parameter.HasDefault {
				t.Errorf("%s path parameter %s is not required and default-free", operation.ID, parameter.Name)
			}
		case ParameterQuery:
			queryNames = append(queryNames, parameter.Name)
			if parameter.Required && parameter.HasDefault {
				t.Errorf("%s query parameter %s is both required and defaulted", operation.ID, parameter.Name)
			}
		default:
			t.Errorf("%s parameter %s has invalid location %q", operation.ID, parameter.Name, parameter.In)
		}
	}
	if !slices.IsSorted(queryNames) {
		t.Errorf("%s query parameters are not sorted: %v", operation.ID, queryNames)
	}
	var placeholders []string
	for part := range strings.SplitSeq(operation.OpenAPIPath, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			placeholders = append(
				placeholders,
				strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}"),
			)
		}
	}
	if !slices.Equal(placeholders, pathNames) {
		t.Errorf(
			"%s path placeholders=%v, catalog parameters=%v",
			operation.ID, placeholders, pathNames,
		)
	}
}

func TestCatalogBindsOpenAPIParameterAndPaymentContracts(t *testing.T) {
	parsed := readOpenAPI(t)
	for _, spec := range catalog {
		defined := parsed.Paths[spec.OpenAPIPath][strings.ToLower(spec.Method)]
		actual := make([]ParameterSpec, 0, len(defined.Parameters))
		hasPaymentSignature := false
		for _, item := range defined.Parameters {
			resolved, referenceName, err := resolveOpenAPIParameter(parsed, item)
			if err != nil {
				t.Fatalf("%s: %v", spec.ID, err)
			}
			if resolved.In == "header" {
				if referenceName == "PaymentSignature" {
					hasPaymentSignature = true
				}
				continue
			}
			if resolved.In != string(ParameterPath) &&
				resolved.In != string(ParameterQuery) {
				continue
			}
			parameter, err := catalogParameterFromOpenAPI(
				parsed, resolved,
			)
			if err != nil {
				t.Fatalf("%s parameter %s: %v", spec.ID, resolved.Name, err)
			}
			actual = append(actual, parameter)
		}
		expected := slices.Clone(spec.Parameters)
		sortParameters(actual)
		sortParameters(expected)
		if !slices.Equal(actual, expected) {
			t.Errorf(
				"%s OpenAPI parameters=%#v, catalog=%#v",
				spec.ID, actual, expected,
			)
		}

		paymentRequired := defined.Responses["402"].Ref == "#/components/responses/PaymentRequired"
		paymentResponse := false
		for status, candidate := range defined.Responses {
			if strings.HasPrefix(status, "2") &&
				candidate.Headers["PAYMENT-RESPONSE"].Ref == "#/components/headers/PaymentResponse" {
				paymentResponse = true
			}
		}
		if hasPaymentSignature != spec.BillingEligible ||
			paymentRequired != spec.BillingEligible ||
			paymentResponse != spec.BillingEligible {
			t.Errorf(
				"%s payment contract signature=%v required=%v response=%v eligible=%v",
				spec.ID, hasPaymentSignature, paymentRequired, paymentResponse, spec.BillingEligible,
			)
		}
	}
}

func resolveOpenAPIParameter(
	document openAPIDocument,
	parameter openAPIParameter,
) (openAPIParameter, string, error) {
	if parameter.Ref == "" {
		return parameter, "", nil
	}
	const prefix = "#/components/parameters/"
	if !strings.HasPrefix(parameter.Ref, prefix) {
		return openAPIParameter{}, "", fmt.Errorf(
			"unsupported parameter ref %q", parameter.Ref,
		)
	}
	name := strings.TrimPrefix(parameter.Ref, prefix)
	resolved, ok := document.Components.Parameters[name]
	if !ok {
		return openAPIParameter{}, "", fmt.Errorf(
			"missing parameter ref %q", parameter.Ref,
		)
	}
	return resolved, name, nil
}

func catalogParameterFromOpenAPI(
	document openAPIDocument,
	parameter openAPIParameter,
) (ParameterSpec, error) {
	schema := parameter.Schema
	schemaName := ""
	if schema.Ref != "" {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(schema.Ref, prefix) {
			return ParameterSpec{}, fmt.Errorf(
				"unsupported schema ref %q", schema.Ref,
			)
		}
		schemaName = strings.TrimPrefix(schema.Ref, prefix)
		var ok bool
		schema, ok = document.Components.Schemas[schemaName]
		if !ok {
			return ParameterSpec{}, fmt.Errorf(
				"missing schema ref %q", parameter.Schema.Ref,
			)
		}
	}
	parameterType, err := openAPIParameterType(schemaName, schema)
	if err != nil {
		return ParameterSpec{}, err
	}
	result := ParameterSpec{
		Name: parameter.Name, In: ParameterLocation(parameter.In),
		Type: parameterType, Required: parameter.Required,
		Repetition: RepetitionReject, TrimSpace: parameter.TrimSpace,
	}
	if schema.Default != nil {
		result.HasDefault = true
		result.DefaultValue = fmt.Sprint(schema.Default)
	}
	if parameterType == ParameterInteger {
		if schema.Minimum != nil {
			result.HasMinimum = true
			result.Minimum = *schema.Minimum
		}
		if schema.Maximum != nil {
			result.HasMaximum = true
			result.Maximum = *schema.Maximum
		}
	}
	switch parameterType {
	case ParameterOpaqueCursor, ParameterText, ParameterEVMNetwork:
		if schema.MinLength != nil {
			result.MinimumBytes = *schema.MinLength
		}
		if schema.MaxLength != nil {
			result.MaximumBytes = *schema.MaxLength
		}
	}
	return result, nil
}

func openAPIParameterType(
	schemaName string,
	schema openAPISchema,
) (ParameterType, error) {
	switch schemaName {
	case "Address":
		return ParameterAddress, nil
	case "BillingPaymentState":
		return ParameterBillingState, nil
	case "Hash":
		return ParameterHash, nil
	case "OpaqueCursor":
		return ParameterOpaqueCursor, nil
	case "Quantity":
		return ParameterUint256, nil
	}
	switch {
	case schema.Type == "integer":
		return ParameterInteger, nil
	case schema.Type == "string" && schema.Format == "block-identifier":
		return ParameterBlockIdentifier, nil
	case schema.Type == "string" && schema.Format == "date-time":
		return ParameterRFC3339, nil
	case schema.Type == "string" && schema.Format == "uuid":
		return ParameterUUID, nil
	case schema.Type == "string" &&
		schema.Pattern == `^eip155:[1-9][0-9]*$`:
		return ParameterEVMNetwork, nil
	case schema.Type == "string":
		return ParameterText, nil
	default:
		return "", fmt.Errorf(
			"unsupported parameter schema type=%q format=%q ref=%q",
			schema.Type, schema.Format, schemaName,
		)
	}
}

func sortParameters(parameters []ParameterSpec) {
	sort.Slice(parameters, func(left, right int) bool {
		if parameters[left].In != parameters[right].In {
			return parameters[left].In < parameters[right].In
		}
		return parameters[left].Name < parameters[right].Name
	})
}

func TestEligibleInventoryIsClosed(t *testing.T) {
	want := []string{
		"getAddress",
		"getAddressDelegation",
		"getAggregateStats",
		"getBlock",
		"getBlockStats",
		"getChartMetric",
		"getChartOverview",
		"getNFTOwner",
		"getToken",
		"getTransaction",
		"getTransactionCalldata",
		"getTransactionFailure",
		"getTransactionTrace",
		"getVerifierJob",
		"listAddressDelegations",
		"listAddressERC20Balances",
		"listAddressERC20Transfers",
		"listAddressInternalTransactions",
		"listAddressNFTBalances",
		"listAddressNFTTransfers",
		"listAddressTransactions",
		"listAddressWithdrawals",
		"listBlockTransactions",
		"listBlocks",
		"listPendingTransactions",
		"listTokenTransfers",
		"listTokens",
		"listTransactionAuthorizations",
		"listTransactionInternalTransactions",
		"listTransactionLogs",
		"listTransactionStateChanges",
		"listTransactionTokenTransfers",
		"listTransactions",
		"search",
	}
	got := EligibleIDs()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("eligible operations=%v, want %v", got, want)
	}
}

func TestProxyAndVerifiedArtifactReadsAreFree(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"getVerifiedContract",
		"getContractProxy",
		"listContractProxyUpgrades",
		"listContractProxyInitializations",
	} {
		operation, ok := Lookup(id)
		if !ok {
			t.Fatalf("operation %s is absent", id)
		}
		if operation.Method != "GET" || operation.BillingEligible || operation.MaxResponseBytes != 0 {
			t.Fatalf("operation %s is not a free GET: %#v", id, operation)
		}
	}
}
