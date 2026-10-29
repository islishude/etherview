package enssync

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/enscontract"
)

// RegistryRoot establishes a reviewed registry-to-DNS path. Empty Name is root.
type RegistryRoot struct {
	Address common.Address `json:"address"`
	Name    string         `json:"name"`
}

// Implementation pins both proxy runtime and implementation identity. It never
// enrolls the implementation address itself as a registry or resolver instance.
type Implementation struct {
	Address       common.Address `json:"address"`
	CodeHash      common.Hash    `json:"code_hash"`
	ProxyCodeHash common.Hash    `json:"proxy_code_hash"`
	Profile       string         `json:"profile"`
}

func validateV2Manifest(m Manifest) error {
	if len(m.Roots) > 16 || len(m.Implementations) > 32 {
		return ErrCollectionLimit
	}
	roots := map[common.Address]bool{}
	implementations := map[common.Address]bool{}
	for _, root := range m.Roots {
		if root.Name != "" {
			if _, ok := normalizedName(root.Name); !ok {
				return ErrInvalidInput
			}
		}
		if roots[root.Address] {
			return ErrInvalidInput
		}
		roots[root.Address] = true
		found := false
		for _, d := range m.Deployments {
			if d.Address == root.Address && d.Profile == "v2_registry" {
				found = true
			}
		}
		if !found {
			return ErrInvalidInput
		}
	}
	for _, impl := range m.Implementations {
		if m.Namespace != enscontract.Official || impl.Address == (common.Address{}) || impl.CodeHash == (common.Hash{}) || impl.ProxyCodeHash == (common.Hash{}) || implementations[impl.Address] || (impl.Profile != "v2_registry" && impl.Profile != "v2_resolver") {
			return ErrInvalidInput
		}
		implementations[impl.Address] = true
		for _, d := range m.Deployments {
			if d.Address == impl.Address {
				return ErrInvalidInput
			}
		}
	}
	return nil
}
func (m Manifest) hasV2() bool {
	for _, d := range m.Deployments {
		if d.Profile == "v2_registry" || d.Profile == "v2_factory" {
			return true
		}
	}
	return false
}
