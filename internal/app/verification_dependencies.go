package app

import (
	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
	"github.com/jackc/pgx/v5/pgxpool"
)

type verificationDependencies struct {
	repository *verify.PostgresRepository
	service    *verify.Service
	catalog    *verify.CompilerCatalog
	sourcify   *verify.SourcifyClient
}

func buildVerificationDependencies(cfg config.Config, roleSet map[components.Role]bool, db *pgxpool.Pool, natsWake *accelerator.NATSWake) (verificationDependencies, error) {
	var err error
	var verificationRepository *verify.PostgresRepository
	var verificationService *verify.Service
	var compilerCatalog *verify.CompilerCatalog
	if cfg.Features.Verification && (roleSet[components.RoleAPI] || roleSet[components.RoleVerification]) {
		sources := map[verify.Language]string{verify.LanguageSolidity: cfg.Verification.CatalogURLs["solidity"]}
		compilerCatalog, err = verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{
			Sources:        sources,
			Platform:       verify.CompilerPlatformEmscriptenWASM32,
			AllowedOrigins: cfg.Verification.AllowedDownloadOrigins,
			Timeout:        cfg.Verification.Timeout, Freshness: cfg.Verification.CatalogMaxStaleness,
			UnsafeAllowPrivateNetworks: cfg.Verification.UnsafeAllowPrivateDownloadNetworks,
		})
		if err != nil {
			return verificationDependencies{}, err
		}
		verificationRepository, err = verify.NewPostgresRepository(db, verify.RepositoryOptions{
			MaxRequestBytes: cfg.Verification.MaxJobBytes,
			MaxInputBytes:   cfg.Verification.MaxInputBytes, MaxQueuedJobs: int64(cfg.Verification.MaxQueuedJobs), MaxQueuedBytes: int64(cfg.Verification.MaxQueuedBytes), MaxUserJobs: int64(cfg.Verification.MaxUserJobs),
			MaxResultBytes: cfg.Verification.MaxOutputBytes,
			SignalJobs: func() {
				if natsWake != nil {
					natsWake.Signal(accelerator.WakeJobs)
				}
			},
		})
		if err != nil {
			return verificationDependencies{}, err
		}
		credentials, credentialErr := sourcifyCredentials(cfg)
		if credentialErr != nil {
			return verificationDependencies{}, credentialErr
		}
		verificationService, err = verify.NewService(
			verificationRepository,
			cfg.Verification.MaxInputBytes,
			verify.ServiceOptions{SourcifyCredentials: credentials},
		)
		if err != nil {
			return verificationDependencies{}, err
		}
	}
	var sourcify *verify.SourcifyClient
	if roleSet[components.RoleVerification] && cfg.Features.Verification {
		sourcify, err = sourcifyClient(cfg)
		if err != nil {
			return verificationDependencies{}, err
		}
	}
	return verificationDependencies{verificationRepository, verificationService, compilerCatalog, sourcify}, nil
}
