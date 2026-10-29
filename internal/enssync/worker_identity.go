package enssync

import (
	"bytes"
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

func (w *Worker) verifySource(ctx context.Context) error {
	source, e := dbgen.New(w.repo.db).GetENSSyncSource(ctx, w.options.SourceID)
	if e != nil {
		return ErrStorage
	}
	m := w.options.Manifest
	chain, ok := unsignedNumeric(m.SourceChainID)
	if !ok {
		return ErrSourceIdentity
	}
	target, ok := unsignedNumeric(m.TargetChainID)
	if !ok {
		return ErrSourceIdentity
	}
	if source.Namespace != string(m.Namespace) || !bytes.Equal(source.GenesisHash, m.Genesis.Bytes()) || !bytes.Equal(source.PolicyDigest, m.Policy.Bytes()) || !bytes.Equal(source.ManifestDigest, w.digest.Bytes()) || source.SourceChainID.Int.Cmp(chain.Int) != 0 || source.TargetChainID.Int.Cmp(target.Int) != 0 {
		return ErrSourceIdentity
	}
	return nil
}
func (w *Worker) verifyEndpoint(ctx context.Context, endpoint *ethrpc.Endpoint, job dbgen.EnsSyncJob, number uint64) error {
	var chain hexutil.Big
	if e := endpoint.CallContext(ctx, &chain, "eth_chainId"); e != nil {
		return ErrRPCUnavailable
	}
	if (*big.Int)(&chain).String() != w.options.Manifest.SourceChainID {
		return ErrSourceIdentity
	}
	session := rpcSession{endpoint: endpoint}
	genesis, e := session.byNumber(ctx, 0)
	if e != nil {
		return e
	}
	if genesis.Hash() != w.options.Manifest.Genesis {
		return ErrSourceIdentity
	}
	block, e := session.header(ctx, "eth_getBlockByHash", common.BytesToHash(job.TargetHash))
	if e != nil {
		return e
	}
	if block.Number.Uint64() != number || !bytes.Equal(block.Hash().Bytes(), job.TargetHash) {
		return ErrSourceIdentity
	}
	return nil
}
