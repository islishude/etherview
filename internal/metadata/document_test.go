package metadata

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateDocumentRequiresBoundedObjectAndKnownFieldTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		document  string
		wantError string
	}{
		{name: "valid", document: `{"name":"NFT","image":"ipfs://bafybeigdyrzt1234567890/1.png","attributes":[{"trait_type":"level","value":3}]}`},
		{name: "array root", document: `[]`, wantError: "root must be an object"},
		{name: "typed image", document: `{"image":{"url":"https://example.invalid"}}`, wantError: `field "image" must be a string`},
		{name: "typed attributes", document: `{"attributes":{}}`, wantError: `field "attributes" must be an array`},
		{name: "multiple values", document: `{} {}`, wantError: "multiple JSON values"},
		{name: "oversized string", document: `{"description":"` + strings.Repeat("x", maxDocumentStringSize+1) + `"}`, wantError: "string exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateDocument([]byte(test.document))
			if test.wantError == "" && err != nil {
				t.Fatalf("validate document: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestValidateDocumentRejectsExcessiveDepthAndCardinality(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat(`{"x":`, maxDocumentDepth+1) + `null` + strings.Repeat(`}`, maxDocumentDepth+1)
	if err := validateDocument([]byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("deep document error = %v", err)
	}

	values := make([]string, maxDocumentNodes+1)
	for index := range values {
		values[index] = fmt.Sprintf("%d", index)
	}
	wide := `{"values":[` + strings.Join(values, ",") + `]}`
	if err := validateDocument([]byte(wide)); err == nil || !strings.Contains(err.Error(), "values") {
		t.Fatalf("wide document error = %v", err)
	}
}
