package app

import (
	"errors"
	"fmt"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/verify"
)

func (assembly runtimeAssembly) registerVerificationComponents() error {
	ctx := assembly.ctx
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	logger := assembly.logger
	businessObserver := assembly.businessObserver
	verificationRepository := assembly.verificationRepository
	compilerCatalog := assembly.compilerCatalog
	sourcify := assembly.sourcify
	componentRegistry := assembly.componentRegistry
	if roleSet[components.RoleAPI] && cfg.Features.Verification {
		compilerCacheLocker, err := verify.NewPostgresCompilerCacheInstallLocker(db)
		if err != nil {
			return err
		}
		compiler, err := verificationCompiler(cfg, compilerCatalog, compilerCacheLocker)
		if err != nil {
			return err
		}
		runtimeValidator, ok := compiler.(verify.RuntimeValidator)
		if !ok {
			return errors.New("verification compiler lacks runtime validation")
		}
		if err := runtimeValidator.ValidateRuntime(ctx); err != nil {
			return fmt.Errorf("validate verification compiler runtime: %w", err)
		}
		catalogRefresher, err := verify.NewCatalogRefresher(
			compilerCatalog, cfg.Verification.CatalogRefreshInterval, logger,
		)
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(
			components.RoleAPI,
			"35-compiler-catalog-refresher",
			func() (components.Service, error) { return catalogRefresher, nil },
		); err != nil {
			return err
		}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleAPI,
			"40-contract-verification",
			"contract-verification-worker",
			cfg.Verification.WorkerCount,
			func(index int, serviceName string) (components.Service, error) {
				return verify.NewWorker(verificationRepository, compiler, verify.WorkerOptions{
					ServiceName: serviceName, WorkerID: verificationWorkerID(index),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, MaxOutputBytes: cfg.Verification.MaxOutputBytes,
					Observer: businessObserver, Sourcify: sourcify,
				})
			},
		); err != nil {
			return err
		}
	}
	return nil
}
