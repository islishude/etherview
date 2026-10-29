package publicquery

import (
	"context"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/browsing"
)

type NativeTransferRequest struct {
	Address, TransactionHash, Cursor string
	Limit                            int
}
type NativeTransferPage struct {
	Browsing                   *browsing.Status
	Items                      []gen.NativeTransfer
	NextCursor                 string
	Applicable                 bool
	CoverageStart, CoverageEnd uint64
}
type NativeTransferReader interface {
	NativeTransfers(context.Context, NativeTransferRequest) (NativeTransferPage, error)
}
