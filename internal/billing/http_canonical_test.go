package billing

import (
	"net/url"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/apiops"
)

func TestCanonicalResourceIgnoresRequestAuthorityAndNormalizesSemantics(t *testing.T) {
	t.Parallel()
	spec, ok := apiops.Lookup("listBlocks")
	if !ok {
		t.Fatal("listBlocks is absent")
	}
	requestURL, err := url.Parse("https://attacker.invalid/api/v1/blocks?limit=025&cursor=opaque%2Dbytes")
	if err != nil {
		t.Fatal(err)
	}
	resource, err := canonicalResource("https://explorer.example", requestURL, spec)
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://explorer.example/api/v1/blocks?cursor=opaque-bytes&limit=25"
	if resource.URL != want {
		t.Fatalf("resource URL=%q want=%q", resource.URL, want)
	}

	requestURL, _ = url.Parse("/api/v1/blocks")
	resource, err = canonicalResource("https://explorer.example/", requestURL, spec)
	if err != nil {
		t.Fatal(err)
	}
	if resource.URL != "https://explorer.example/api/v1/blocks?limit=25" {
		t.Fatalf("defaulted resource URL=%q", resource.URL)
	}

	search, _ := apiops.Lookup("search")
	requestURL, _ = url.Parse("/api/v1/search?cursor=Aa-_9&q=%20MiXeD%20")
	resource, err = canonicalResource("https://explorer.example", requestURL, search)
	if err != nil {
		t.Fatal(err)
	}
	if resource.URL != "https://explorer.example/api/v1/search?cursor=Aa-_9&limit=20&q=MiXeD" {
		t.Fatalf("search resource URL=%q", resource.URL)
	}
}

func TestCanonicalResourceReadsDefaultsAndBoundsOnlyFromCatalog(t *testing.T) {
	t.Parallel()
	spec, _ := apiops.Lookup("listBlocks")
	for index := range spec.Parameters {
		switch spec.Parameters[index].Name {
		case "limit":
			spec.Parameters[index].DefaultValue = "17"
		case "cursor":
			spec.Parameters[index].MaximumBytes = 3
		}
	}
	resource, err := canonicalResource(
		"https://explorer.example",
		&url.URL{Path: "/api/v1/blocks", RawQuery: "cursor=Aa_"},
		spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resource.URL != "https://explorer.example/api/v1/blocks?cursor=Aa_&limit=17" {
		t.Fatalf("resource=%q", resource.URL)
	}
	if _, err := canonicalResource(
		"https://explorer.example",
		&url.URL{Path: "/api/v1/blocks", RawQuery: "cursor=Aa_9"},
		spec,
	); err == nil {
		t.Fatal("mutated catalog cursor bound was ignored")
	}
}

func TestCanonicalResourceNormalizesEveryPathIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		operation string
		path      string
		want      string
	}{
		{"getBlock", "/api/v1/blocks/0x000A", "/api/v1/blocks/10"},
		{"getBlock", "/api/v1/blocks/0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "/api/v1/blocks/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"getTransaction", "/api/v1/transactions/0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "/api/v1/transactions/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"getAddress", "/api/v1/addresses/0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "/api/v1/addresses/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"getNFTOwner", "/api/v1/nfts/0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/42", "/api/v1/nfts/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/42"},
		{"getVerificationJob", "/api/v1/verification/jobs/550E8400-E29B-41D4-A716-446655440000", "/api/v1/verification/jobs/550e8400-e29b-41d4-a716-446655440000"},
		{"getSourcifyJob", "/api/v1/sourcify/jobs/550E8400-E29B-41D4-A716-446655440000", "/api/v1/sourcify/jobs/550e8400-e29b-41d4-a716-446655440000"},
	}
	for _, test := range tests {
		t.Run(test.operation+"/"+test.path, func(t *testing.T) {
			spec, ok := apiops.Lookup(test.operation)
			if !ok {
				t.Fatal("operation absent")
			}
			requestURL := &url.URL{Path: test.path}
			resource, err := canonicalResource("http://localhost:8080", requestURL, spec)
			if err != nil {
				t.Fatal(err)
			}
			if resource.URL != "http://localhost:8080"+test.want {
				t.Fatalf("resource=%q", resource.URL)
			}
		})
	}
}

func TestCanonicalResourceRejectsAmbiguousOrInvalidParameters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		operation string
		target    string
	}{
		{"listBlocks", "/api/v1/blocks?unknown=1"},
		{"listBlocks", "/api/v1/blocks?limit=1&limit=1"},
		{"listBlocks", "/api/v1/blocks?li%6dit=1&limit=2"},
		{"listBlocks", "/api/v1/blocks?limit="},
		{"listBlocks", "/api/v1/blocks?cursor="},
		{"listBlocks", "/api/v1/blocks?cursor=%zz"},
		{"listBlocks", "/api/v1/blocks?cursor=a;b=c"},
		{"search", "/api/v1/search"},
		{"search", "/api/v1/search?q=%20%20"},
		{"getBlockStats", "/api/v1/stats/blocks?from_block=1"},
		{"getAggregateStats", "/api/v1/stats/summary?to_block=2"},
		{"getBlockStats", "/api/v1/stats/blocks?from_block=01&to_block=2"},
		{"getVerifiedContract", "/api/v1/contracts/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/verification"},
		{"getVerifiedContract", "/api/v1/contracts/0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/verification?code_hash=0x01"},
		{"getBlock", "/api/v1/blocks/not-a-block"},
	}
	for _, test := range tests {
		t.Run(test.operation+"/"+test.target, func(t *testing.T) {
			spec, _ := apiops.Lookup(test.operation)
			path, rawQuery := test.target, ""
			if separator := strings.IndexByte(test.target, '?'); separator >= 0 {
				path, rawQuery = test.target[:separator], test.target[separator+1:]
			}
			requestURL := &url.URL{Path: path, RawQuery: rawQuery}
			if _, err := canonicalResource("https://explorer.example", requestURL, spec); err == nil {
				t.Fatal("ambiguous resource passed")
			}
		})
	}
}
