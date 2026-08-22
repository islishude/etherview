package app

import (
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

func componentRoles(names []string) ([]components.Role, map[components.Role]bool, error) {
	normalized, err := config.NormalizeRoles(names)
	if err != nil {
		return nil, nil, err
	}
	roles := make([]components.Role, 0, len(normalized))
	set := make(map[components.Role]bool, len(normalized))
	for _, name := range normalized {
		role := components.Role(name)
		roles = append(roles, role)
		set[role] = true
	}
	return roles, set, nil
}

func needsRPC(roles map[components.Role]bool) bool {
	return roles[components.RoleSync] || roles[components.RoleEnrich] || roles[components.RoleTrace] || roles[components.RoleMaintenance]
}

func needsRPCForServe(roles map[components.Role]bool, cfg config.Config) bool {
	return needsRPC(roles) || roles[components.RoleMetadata] && cfg.Features.NFTMetadata
}

func roleUsesNATSWake(role components.Role, cfg config.Config) bool {
	return role == components.RoleAPI || role == components.RoleSync || role == components.RoleEnrich ||
		role == components.RoleTrace && cfg.Features.Trace
}

func rolesUseNATSWake(roles []components.Role, cfg config.Config) bool {
	for _, role := range roles {
		if roleUsesNATSWake(role, cfg) {
			return true
		}
	}
	return false
}
