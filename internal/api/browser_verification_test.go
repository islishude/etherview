package api_test

import (
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestBrowserVerificationRequestSchemas(t *testing.T) {
	t.Parallel()
	document, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
	}{
		{"AddressVerificationSubmission", `{"language":"solidity","compiler_version":"0.8.30","input_kind":"standard_json","input":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}}}}`},
		{"SourcifySourceImportRequest", `{"chain_id":"1"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal([]byte(test.body), &body); err != nil {
				t.Fatal(err)
			}
			native := document.Components.Schemas[test.name].Value
			browser := document.Components.Schemas["Browser"+test.name].Value
			if err := native.VisitJSON(body); err != nil {
				t.Fatalf("native request rejected: %v", err)
			}
			if err := browser.VisitJSON(body); err == nil {
				t.Fatal("browser request must require a target address")
			}
			body["address"] = "0x1111111111111111111111111111111111111111"
			if err := browser.VisitJSON(body); err != nil {
				t.Fatalf("browser request rejected: %v", err)
			}
			if err := native.VisitJSON(body); err == nil {
				t.Fatal("native request must still reject additional properties")
			}
			body["api_key"] = "unexpected"
			if err := browser.VisitJSON(body); err == nil {
				t.Fatal("browser request must reject additional properties")
			}
		})
	}
}
