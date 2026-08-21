package app

import "github.com/islishude/etherview/internal/components"

func (assembly runtimeAssembly) registerIdleRoleComponents() error {
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	componentRegistry := assembly.componentRegistry
	for _, role := range []components.Role{
		components.RoleEnrich, components.RoleTrace, components.RoleMetadata,
	} {
		if !roleSet[role] {
			continue
		}
		if role == components.RoleEnrich || role == components.RoleTrace && cfg.Features.Trace ||
			role == components.RoleMetadata && cfg.Features.NFTMetadata {
			continue
		}
		role := role
		key := "50-role-" + string(role)
		if err := componentRegistry.Register(role, key, func() (components.Service, error) {
			return &databaseRoleService{name: string(role) + "-worker", db: db, interval: cfg.Runtime.PollInterval}, nil
		}); err != nil {
			return err
		}
	}
	return nil
}
