package contractartifact

import (
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/db/gen"
)

func TestArtifactResolverRestoresImmutableOutcomesAndRanksStableSources(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.ContractArtifactArtifactSource), " ")
	for _, required := range []string{
		"JOIN verification_results AS result",
		"result.job_id = verified.verification_job_id",
		"result.request_digest = verified.request_digest",
		"result.outcome->'creation_match'",
		"result.outcome->'runtime_match'",
		"(verified.address = $2",
		"(verified.abi IS NOT NULL) DESC",
		"(verified.match_type = 'full') DESC",
		"verified.request_digest ASC",
		"verified.verification_job_id ASC",
		"verified.address ASC",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("artifact resolver query lacks %q: %s", required, query)
		}
	}
}

func TestArtifactResolverHistoricalTargetRequiresExactCanonicalBlock(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.ContractArtifactTargetAtBlock), " ")
	for _, required := range []string{
		"canonical.number = $3::numeric",
		"canonical.block_hash = $4",
		"candidate.address = $2",
		"candidate.block_number <= context.number",
		"candidate.canonical",
		"ORDER BY candidate.block_number DESC",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("historical artifact resolver lacks %q: %s", required, query)
		}
	}
}
