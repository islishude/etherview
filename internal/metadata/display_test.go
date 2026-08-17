package metadata

import (
	"strings"
	"testing"
)

func TestProjectNFTMetadataDocumentBoundsDisplayFields(t *testing.T) {
	resolver, err := New(Policy{IPFSGateway: "https://ipfs.example/base"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attributes := make([]string, 0, maximumDisplayAttributes+4)
	attributes = append(attributes,
		`{"trait_type":"Level","value":9007199254740993,"display_type":"number"}`,
		`{"trait_type":"Active","value":true}`,
		`{"trait_type":"Empty","value":null}`,
		`{"value":"missing trait"}`,
		`{"trait_type":"Nested","value":{"unsafe":true}}`,
	)
	for range maximumDisplayAttributes {
		attributes = append(attributes, `{"trait_type":"Extra","value":"value"}`)
	}
	document := `{"name":"` + strings.Repeat("界", maximumDisplayNameRunes+1) +
		`","description":"` + strings.Repeat("d", maximumDisplayDescriptionRunes+1) +
		`","image":"ipfs://bafybeigdyrzt1234567890/image.png?download=1","attributes":[` +
		strings.Join(attributes, ",") + `]}`

	projection, err := projectNFTMetadataDocument([]byte(document), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(projection.Name)); got != maximumDisplayNameRunes || !projection.NameTruncated {
		t.Fatalf("name runes=%d truncated=%v", got, projection.NameTruncated)
	}
	if got := len([]rune(projection.Description)); got != maximumDisplayDescriptionRunes || !projection.DescriptionTruncated {
		t.Fatalf("description runes=%d truncated=%v", got, projection.DescriptionTruncated)
	}
	if len(projection.Attributes) != maximumDisplayAttributes || projection.OmittedAttributeCount != 5 {
		t.Fatalf("attributes=%d omitted=%d", len(projection.Attributes), projection.OmittedAttributeCount)
	}
	if got := projection.Attributes[0]; got.TraitType != "Level" || got.Value != "9007199254740993" || got.DisplayType != "number" {
		t.Fatalf("exact numeric attribute=%#v", got)
	}
	if got := projection.Attributes[1].Value; got != "true" {
		t.Fatalf("boolean value=%q", got)
	}
	if got := projection.Attributes[2].Value; got != "null" {
		t.Fatalf("null value=%q", got)
	}
	if projection.Image.State != NFTMetadataImageAvailable || projection.Image.SourceScheme != "ipfs" ||
		projection.Image.URL != "https://ipfs.example/base/ipfs/bafybeigdyrzt1234567890/image.png?download=1" {
		t.Fatalf("image=%#v", projection.Image)
	}
}

func TestProjectNFTMetadataImagePolicy(t *testing.T) {
	resolver, err := New(Policy{IPFSGateway: "https://ipfs.example/base"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	withoutGateway, err := New(Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		value  string
		client *Client
		state  NFTMetadataImageState
		url    string
	}{
		{name: "https", value: "https://media.example/image.png?token=secret", client: resolver, state: NFTMetadataImageAvailable, url: "https://media.example/image.png?token=secret"},
		{name: "ipfs", value: "ipfs://bafybeigdyrzt1234567890/image.png", client: resolver, state: NFTMetadataImageAvailable, url: "https://ipfs.example/base/ipfs/bafybeigdyrzt1234567890/image.png"},
		{name: "missing gateway", value: "ipfs://bafybeigdyrzt1234567890/image.png", client: withoutGateway, state: NFTMetadataImageGatewayUnavailable},
		{name: "missing", value: "  ", client: resolver, state: NFTMetadataImageMissing},
		{name: "http", value: "http://media.example/image.png", client: resolver, state: NFTMetadataImageUnsupported},
		{name: "javascript", value: "javascript:alert(1)", client: resolver, state: NFTMetadataImageUnsupported},
		{name: "credentials", value: "https://user:secret@media.example/image.png", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "fragment", value: "https://media.example/image.png#fragment", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "localhost", value: "https://assets.localhost/image.png", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "private literal", value: "https://127.0.0.1/image.png", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "special literal", value: "https://192.0.2.1/image.png", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "invalid ipfs", value: "ipfs://bad/../secret", client: resolver, state: NFTMetadataImageUnsafe},
		{name: "control", value: "https://media.example/image.png\nsecret", client: resolver, state: NFTMetadataImageUnsafe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := projectNFTMetadataImage(test.value, test.client)
			if got.State != test.state || got.URL != test.url {
				t.Fatalf("image=%#v want state=%s url=%q", got, test.state, test.url)
			}
		})
	}
}

func TestProjectNFTMetadataDocumentRendersOnlyPlainFields(t *testing.T) {
	resolver, err := New(Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	document := []byte(`{"name":"<img src=x onerror=alert(1)>","description":"**not markdown**","animation_url":"https://ignored.example/a","external_url":"https://ignored.example/e","attributes":[]}`)
	projection, err := projectNFTMetadataDocument(document, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Name != "<img src=x onerror=alert(1)>" || projection.Description != "**not markdown**" ||
		len(projection.Attributes) != 0 || projection.Image.State != NFTMetadataImageMissing {
		t.Fatalf("projection=%#v", projection)
	}
}
