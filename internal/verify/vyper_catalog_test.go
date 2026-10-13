package verify

import (
	"testing"
	"time"
)

func TestVyperApplicationVersionsIgnoreRemoteFreshness(t *testing.T) {
	catalog := &CompilerCatalog{vyperEntries: map[string]CatalogEntry{
		"0.4.3": {Version: "0.4.3", GenerationID: 7, FetchedAt: time.Unix(0, 0)},
		"0.2.1": {Version: "0.2.1", GenerationID: 7},
	}}
	entry, err := catalog.Lookup(t.Context(), LanguageVyper, "0.4.3")
	if err != nil || entry.GenerationID != 7 {
		t.Fatalf("local lock lookup: %+v %v", entry, err)
	}
	versions, err := catalog.Versions(t.Context(), LanguageVyper)
	if err != nil || len(versions) != 2 || versions[0] != "0.2.1" {
		t.Fatalf("versions: %v %v", versions, err)
	}
	if _, err = catalog.Lookup(t.Context(), LanguageVyper, "0.4.4"); err == nil {
		t.Fatal("unlocked version accepted")
	}
}
