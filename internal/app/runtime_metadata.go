package app

import (
	"errors"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/ethrpc"
)

func (assembly runtimeAssembly) registerMetadataComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	logger := assembly.logger
	businessObserver := assembly.businessObserver
	rpcBuild := assembly.rpcBuild
	componentRegistry := assembly.componentRegistry
	if roleSet[components.RoleMetadata] && cfg.Features.NFTMetadata {
		if rpcBuild == nil || len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 {
			return errors.New("metadata role requires an HTTP state RPC endpoint for block-pinned source discovery")
		}
		if err := registerMetadataWorkers(componentRegistry, db, rpcBuild.Pool, cfg, logger, businessObserver); err != nil {
			return err
		}
	}
	return nil
}
