package verify

import (
	"context"
	"encoding/hex"

	vyper "github.com/islishude/etherview/compiler/vyper"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

// PublicCompilerCatalog reads worker-published catalog facts without requiring
// a compiler runtime in the API process. Local worker availability stays local.
type PublicCompilerCatalog struct{ Catalog *CompilerCatalog }

func (reader PublicCompilerCatalog) Versions(ctx context.Context, language Language) ([]string, error) {
	if reader.Catalog == nil {
		return nil, ErrCompilerVersionUnavailable
	}
	if language != LanguageVyper {
		return reader.Catalog.Versions(ctx, language)
	}
	recipes, err := vyper.Recipes()
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(recipes))
	expected := make(map[string]string, len(recipes))
	for _, recipe := range recipes {
		url := "embedded:vyper/" + recipe.Digest()
		urls = append(urls, url)
		expected[recipe.Version+":"+url] = recipe.CompilerSHA256
	}
	rows, err := dbgen.New(reader.Catalog.db).VerifyVyperPublicVersions(ctx, urls)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(rows))
	for _, row := range rows {
		if expected[row.Version+":"+row.ArtifactUrl] == hex.EncodeToString(row.ArtifactSha256) {
			versions = append(versions, row.Version)
		}
	}
	if len(versions) == 0 {
		return nil, ErrVyperRuntimeUnavailable
	}
	sortCompilerVersions(versions)
	return versions, nil
}
