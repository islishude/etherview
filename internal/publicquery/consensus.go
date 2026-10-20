package publicquery

import (
	"context"
	"github.com/islishude/etherview/internal/api/gen"
)

type ConsensusReader interface {
	BlockConsensus(context.Context, string) (gen.ConsensusSummary, error)
	BlockBlobs(context.Context, string) (gen.BlockBlobs, error)
	BlobContent(context.Context, string, uint64) ([]byte, error)
}
