package app

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/ensread"
)

func newENSReader(db dbaccess.Database, cfg config.Config) (*ensread.Reader, error) {
	if !cfg.Features.ENS {
		return nil, nil
	}
	return ensread.New(db, ensread.Options{ChainID: cfg.Chain.ID, Policy: common.HexToHash(cfg.ENS.PolicyDigest), MaxBatch: cfg.ENS.MaxBatchAddresses, SnapshotTTL: cfg.ENS.SnapshotTTL})
}
