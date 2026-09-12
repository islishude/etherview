package verify

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignedVyperCatalog(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parser := &CompilerCatalog{options: CompilerCatalogOptions{VyperPublicKey: public, MaxEntries: 4096, MaxArtifactBytes: defaultCompilerArtifactMB}, origins: map[string]struct{}{"https://compilers.example": {}}}
	base := vyperCatalogDocument{Schema: "etherview-vyper-catalog-v1", ExpiresAt: time.Now().Add(time.Hour), Builds: []vyperCatalogBuild{{Version: "0.4.3", CompilerSHA256: strings.Repeat("a", 64), Runtimes: []VyperRuntimeArtifact{
		{Platform: "linux-amd64", URL: "https://compilers.example/amd64.tar.gz", SHA256: strings.Repeat("b", 64), ManifestSHA256: strings.Repeat("c", 64), MaxBytes: 1024, Protocol: vyperDynamicSchema},
		{Platform: "linux-arm64", URL: "https://compilers.example/arm64.tar.gz", SHA256: strings.Repeat("d", 64), ManifestSHA256: strings.Repeat("e", 64), MaxBytes: 1024, Protocol: vyperDynamicSchema},
	}}}}
	cases := []struct {
		name   string
		mutate func(*vyperCatalogDocument)
		valid  bool
	}{
		{"valid", func(*vyperCatalogDocument) {}, true},
		{"expired", func(d *vyperCatalogDocument) { d.ExpiresAt = time.Now().Add(-time.Second) }, false},
		{"prerelease", func(d *vyperCatalogDocument) { d.Builds[0].Version = "0.4.3rc1" }, false},
		{"build metadata", func(d *vyperCatalogDocument) { d.Builds[0].Version = "0.4.3+abc" }, false},
		{"withdrawn", func(d *vyperCatalogDocument) { d.Builds[0].Withdrawn = true }, false},
		{"missing architecture", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes = d.Builds[0].Runtimes[:1] }, false},
		{"duplicate architecture", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes[1] = d.Builds[0].Runtimes[0] }, false},
		{"untrusted origin", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes[0].URL = "https://attacker.example/runtime" }, false},
		{"wrong protocol", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes[0].Protocol = "unrestricted" }, false},
		{"zero digest", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes[0].SHA256 = strings.Repeat("0", 64) }, false},
		{"oversize", func(d *vyperCatalogDocument) { d.Builds[0].Runtimes[0].MaxBytes = defaultCompilerArtifactMB + 1 }, false},
	}
	initial, _ := json.Marshal(base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d vyperCatalogDocument
			if err := json.Unmarshal(initial, &d); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&d)
			payload, _ := json.Marshal(d)
			signature := ed25519.Sign(private, payload)
			raw, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(signature)})
			entries, err := parser.parseVyper("https://compilers.example/catalog.json", raw)
			if (err == nil) != tc.valid {
				t.Fatalf("entries=%d error=%v", len(entries), err)
			}
		})
	}
	payload, _ := json.Marshal(base)
	raw, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))})
	if _, err := parser.parseVyper("https://compilers.example/catalog.json", raw); err == nil {
		t.Fatal("accepted forged signature")
	}
}
