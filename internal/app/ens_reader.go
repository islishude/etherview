package app

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ensread"
)

func newENSReader(db dbaccess.Database, cfg config.Config) (*ensread.Reader, error) {
	if !cfg.Features.ENS {
		return nil, nil
	}
	sources := []enscontract.Namespace{enscontract.Official}
	if cfg.ENS.Mode == "custom_only" {
		sources = nil
	}
	if cfg.ENS.Custom.UniversalResolver != "" {
		sources = append(sources, enscontract.Custom)
	}
	return ensread.New(db, ensread.Options{ChainID: cfg.Chain.ID, Policy: common.HexToHash(cfg.ENS.PolicyDigest), MaxBatch: cfg.ENS.MaxBatchAddresses, SnapshotTTL: cfg.ENS.SnapshotTTL, Sources: sources})
}
