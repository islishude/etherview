package enssync

import (
	"bytes"
	"context"

	"github.com/ethereum/go-ethereum/common"
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
	if !bytes.Equal(source.GenesisHash, m.Genesis.Bytes()) || !bytes.Equal(source.PolicyDigest, m.Policy.Bytes()) || !bytes.Equal(source.ManifestDigest, w.digest.Bytes()) || !equalUnsignedNumeric(source.SourceChainID, chain) || !equalUnsignedNumeric(source.TargetChainID, target) {
		return ErrSourceIdentity
	}
	return nil
}
func (w *Worker) verifyEndpoint(ctx context.Context, endpoint *ethrpc.Endpoint, job dbgen.EnsSyncJob, number uint64) error {
	session := rpcSession{endpoint: endpoint}
	if e := session.verifyIdentity(ctx, w.options.Manifest); e != nil {
		return e
	}
	block, e := session.header(ctx, "eth_getBlockByHash", common.BytesToHash(job.TargetHash))
	if e != nil {
		return e
	}
	if block.Number.Uint64() != number || !bytes.Equal(block.Hash().Bytes(), job.TargetHash) {
		return ErrSourceIdentity
	}
	for _, deployment := range w.options.Manifest.Deployments {
		if deployment.Number > number {
			continue
		}
		if e := session.verifyCode(ctx, deployment, common.BytesToHash(job.TargetHash)); e != nil {
			return e
		}
	}
	return nil
}
