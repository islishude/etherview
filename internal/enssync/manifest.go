package enssync

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enscontract"
)

// Event profiles are reviewed subsets of go-ens/v4 v4.0.0 and ENS contracts
// v1.1.4 / v1.7.0. See abi/README.md. Signatures alone never select a profile.
//
//go:embed abi/*.json
var eventFiles embed.FS

type Deployment struct {
	Address  common.Address `json:"address"`
	Profile  string         `json:"profile"`
	Number   uint64         `json:"number"`
	Hash     common.Hash    `json:"hash"`
	CodeHash common.Hash    `json:"code_hash"`
	// Proxy profiles require exact EIP-1967 state verification by the RPC source.
	Implementation         common.Address `json:"implementation"`
	ImplementationCodeHash common.Hash    `json:"implementation_code_hash"`
}

type Manifest struct {
	Registry          common.Address        `json:"registry,omitempty"`
	UniversalResolver common.Address        `json:"universal_resolver,omitempty"`
	TargetChainID     string                `json:"target_chain_id"`
	SourceChainID     string                `json:"source_chain_id"`
	Genesis           common.Hash           `json:"genesis"`
	Namespace         enscontract.Namespace `json:"namespace"`
	Policy            common.Hash           `json:"policy"`
	Start             uint64                `json:"start"`
	StartHash         common.Hash           `json:"start_hash"`
	ParentName        string                `json:"parent_name"`
	CoinType          string                `json:"coin_type"`
	Deployments       []Deployment          `json:"deployments"`
	Roots             []RegistryRoot        `json:"roots,omitempty"`
	Implementations   []Implementation      `json:"implementations,omitempty"`
}

type contractProfile struct {
	abi    abi.ABI
	digest common.Hash
}

var (
	ErrSourceIdentity        = errors.New("ens_source_identity")
	ErrSourceIntegrity       = errors.New("ens_finalized_integrity")
	ErrCoverageGap           = errors.New("ens_coverage_gap")
	ErrUnsupportedDeployment = errors.New("ens_unsupported_deployment")
	ErrCollectionLimit       = errors.New("ens_collection_limit")
	ErrInvalidEvent          = errors.New("ens_invalid_event")
	ErrRPCUnavailable        = errors.New("ens_rpc_unavailable")
)

func loadProfile(name string) (contractProfile, error) {
	switch name {
	case "universal", "registry", "registrar", "controller_legacy", "controller_v3", "controller", "wrapper", "resolver", "reverse", "default_reverse", "v2_registry", "v2_resolver", "v2_factory":
	default:
		return contractProfile{}, ErrUnsupportedDeployment
	}
	raw, err := eventFiles.ReadFile("abi/" + name + ".json")
	if err != nil {
		return contractProfile{}, ErrUnsupportedDeployment
	}
	parsed, err := abi.JSON(strings.NewReader(string(raw)))
	if err != nil {
		return contractProfile{}, ErrUnsupportedDeployment
	}
	return contractProfile{abi: parsed, digest: sha256.Sum256(raw)}, nil
}

func validateManifest(input Manifest) (Manifest, common.Hash, map[common.Address]contractProfile, error) {
	m := input
	m.Deployments = append([]Deployment(nil), input.Deployments...)
	m.Roots = append([]RegistryRoot(nil), input.Roots...)
	m.Implementations = append([]Implementation(nil), input.Implementations...)
	source, ok := unsignedNumeric(m.SourceChainID)
	_, targetOK := unsignedNumeric(m.TargetChainID)
	_, coinOK := unsignedNumeric(m.CoinType)
	if !ok || !targetOK || !coinOK || source.Int.Sign() == 0 || m.TargetChainID == "0" || m.Genesis == (common.Hash{}) || m.Policy == (common.Hash{}) || m.StartHash == (common.Hash{}) ||
		len(m.Deployments) == 0 || len(m.Deployments) > 64 || m.Start == 0 && m.StartHash != m.Genesis {
		return m, common.Hash{}, nil, ErrInvalidInput
	}
	if m.Namespace == enscontract.Official {
		if source.Int.Cmp(big.NewInt(1)) != 0 || m.Genesis != common.HexToHash("0xd4e56740f876aef8c010b86a40d5f56745a118d0906a34e69aec8c0db1cb8fa3") || m.ParentName != "eth" {
			return m, common.Hash{}, nil, ErrSourceIdentity
		}
	} else if m.Namespace != enscontract.Custom || m.SourceChainID != m.TargetChainID || m.Start != 0 {
		return m, common.Hash{}, nil, ErrSourceIdentity
	}
	if name, valid := normalizedName(m.ParentName); !valid || name != m.ParentName {
		return m, common.Hash{}, nil, ErrInvalidInput
	}
	sort.Slice(m.Deployments, func(i, j int) bool {
		return strings.Compare(m.Deployments[i].Address.Hex(), m.Deployments[j].Address.Hex()) < 0
	})
	profiles := make(map[common.Address]contractProfile, len(m.Deployments))
	digests := make([]common.Hash, 0, len(m.Deployments))
	for _, d := range m.Deployments {
		if d.Address == (common.Address{}) || d.Hash == (common.Hash{}) || d.CodeHash == (common.Hash{}) || d.Number < m.Start ||
			(d.Implementation == (common.Address{})) != (d.ImplementationCodeHash == (common.Hash{})) {
			return m, common.Hash{}, nil, ErrInvalidInput
		}
		if _, exists := profiles[d.Address]; exists {
			return m, common.Hash{}, nil, ErrInvalidInput
		}
		if m.Namespace == enscontract.Custom && d.Implementation != (common.Address{}) {
			return m, common.Hash{}, nil, ErrUnsupportedDeployment
		}
		profile, err := loadProfile(d.Profile)
		if err != nil {
			return m, common.Hash{}, nil, err
		}
		profiles[d.Address] = profile
		digests = append(digests, profile.digest)
	}
	if err := validateV2Manifest(m); err != nil {
		return m, common.Hash{}, nil, err
	}
	for _, template := range m.Implementations {
		profile, err := loadProfile(template.Profile)
		if err != nil {
			return m, common.Hash{}, nil, err
		}
		digests = append(digests, profile.digest)
	}
	raw, err := json.Marshal(struct {
		Manifest Manifest
		Profiles []common.Hash
	}{m, digests})
	if err != nil {
		return m, common.Hash{}, nil, ErrInvalidInput
	}
	return m, crypto.Keccak256Hash(raw), profiles, nil
}
