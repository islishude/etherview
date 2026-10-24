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

// RequestsReader serves only durable, authenticated block request publications.
type RequestsReader interface {
	BlockRequests(context.Context, string, string, string, int) (gen.BlockRequests, string, error)
}
