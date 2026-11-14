package app

import (
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/defi"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/defiread"
)

func loadDeFiManifest(cfg config.Config) (defi.Manifest, error) {
	f, e := os.Open(cfg.DeFi.Manifest)
	if e != nil {
		return defi.Manifest{}, deficontract.ErrInput
	}
	defer func() { _ = f.Close() }()
	m, e := defi.ReadManifest(f)
	if e != nil {
		return m, e
	}
	if m.ChainID != strconv.FormatUint(cfg.Chain.ID, 10) || (cfg.Chain.GenesisHash != "" && m.Genesis != common.HexToHash(cfg.Chain.GenesisHash)) {
		return m, deficontract.ErrInput
	}
	return m, nil
}
func (a runtimeAssembly) registerDeFiComponents() error {
	if !a.cfg.DeFi.Enabled || !a.roleSet[components.RoleEnrich] {
		return nil
	}
	m, e := loadDeFiManifest(a.cfg)
	if e != nil {
		return e
	}
	if a.rpcBuild == nil || a.rpcBuild.Pool == nil {
		return deficontract.ErrUnavailable
	}
	return a.componentRegistry.Register(components.RoleEnrich, "40-defi-index", func() (components.Service, error) {
		return &defi.Worker{DB: a.db, Pool: a.rpcBuild.Pool, Manifest: m, Logger: a.logger}, nil
	})
}
func newDeFiReader(db dbaccess.Database, cfg config.Config) (*defiread.Reader, error) {
	reader := &defiread.Reader{DB: db}
	if !cfg.DeFi.Enabled {
		return reader, nil
	}
	m, e := loadDeFiManifest(cfg)
	if e != nil {
		return nil, e
	}
	reader.Digest = m.Digest()
	for _, d := range m.Deployments {
		reader.Sources = append(reader.Sources, defiread.Source{ID: m.Digest() + ":" + d.ID, Deployment: d.ID, Protocol: d.Protocol})
	}
	return reader, nil
}
