// Package defi implements authenticated, exact-block DeFi observations.
package defi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/jsonstrict"
)

// Contract binds an adapter role to reviewed deployment and implementation code.
type Contract struct {
	Role                   string         `json:"role"`
	Address                common.Address `json:"address"`
	CodeHash               common.Hash    `json:"code_hash"`
	Implementation         common.Address `json:"implementation,omitempty"`
	ImplementationCodeHash common.Hash    `json:"implementation_code_hash,omitempty"`
	ImplementationSlot     common.Hash    `json:"implementation_slot,omitempty"`
}
type Deployment struct {
	ID        string                `json:"id"`
	Protocol  deficontract.Protocol `json:"protocol"`
	Start     uint64                `json:"start_block"`
	StartHash common.Hash           `json:"start_hash"`
	Contracts []Contract            `json:"contracts"`
	// Pool code identities authorize factory-discovered instances, never symbols.
	PoolCodeHashes []common.Hash `json:"pool_code_hashes,omitempty"`
	// Aave receipt proxies can upgrade independently of the Pool proxy.
	ReceiptContracts []Contract `json:"receipt_contracts,omitempty"`
}
type Manifest struct {
	Version     int          `json:"version"`
	Revision    uint64       `json:"revision"`
	ChainID     string       `json:"chain_id"`
	Genesis     common.Hash  `json:"genesis_hash"`
	Deployments []Deployment `json:"deployments"`
}

var deploymentID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func ReadManifest(r io.Reader) (Manifest, error) {
	var m Manifest
	raw, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || jsonstrict.Validate(raw, jsonstrict.Limits{MaxNodes: 16384}) != nil {
		return m, deficontract.ErrInput
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF {
		return Manifest{}, deficontract.ErrInput
	}
	return m, m.Validate()
}
func (m Manifest) Validate() error {
	chain, ok := new(big.Int).SetString(m.ChainID, 10)
	if !ok || chain.Sign() <= 0 || chain.BitLen() > 64 || chain.String() != m.ChainID || m.Version != 1 || m.Genesis == (common.Hash{}) || len(m.Deployments) == 0 || len(m.Deployments) > 32 {
		return deficontract.ErrInput
	}
	seen := map[string]bool{}
	for _, d := range m.Deployments {
		if !deploymentID.MatchString(d.ID) || seen[d.ID] || !d.Protocol.Valid() || d.StartHash == (common.Hash{}) || (d.Start == 0 && d.StartHash != m.Genesis) || len(d.Contracts) == 0 || len(d.Contracts) > 16 || len(d.PoolCodeHashes) > 32 {
			return deficontract.ErrInput
		}
		seen[d.ID] = true
		roles := map[string]bool{}
		addresses := map[common.Address]bool{}
		for _, c := range d.Contracts {
			if !deploymentID.MatchString(c.Role) || roles[c.Role] || addresses[c.Address] || c.Address == (common.Address{}) || c.CodeHash == (common.Hash{}) {
				return deficontract.ErrInput
			}
			roles[c.Role] = true
			addresses[c.Address] = true
			proxy := c.Implementation != (common.Address{})
			if proxy != (c.ImplementationCodeHash != (common.Hash{})) || proxy != (c.ImplementationSlot != (common.Hash{})) {
				return deficontract.ErrInput
			}
		}
		if len(d.ReceiptContracts) > 1024 || (len(d.ReceiptContracts) != 0 && d.Protocol != deficontract.AaveV3) {
			return deficontract.ErrInput
		}
		for _, c := range d.ReceiptContracts {
			if c.Role != "receipt" || addresses[c.Address] || c.Address == (common.Address{}) || c.CodeHash == (common.Hash{}) {
				return deficontract.ErrInput
			}
			addresses[c.Address] = true
			proxy := c.Implementation != (common.Address{})
			if proxy != (c.ImplementationCodeHash != (common.Hash{})) || proxy != (c.ImplementationSlot != (common.Hash{})) {
				return deficontract.ErrInput
			}
		}
		for _, role := range requiredRoles(d.Protocol) {
			if !roles[role] {
				return deficontract.ErrInput
			}
		}
		if slices.Contains(d.PoolCodeHashes, common.Hash{}) {
			return deficontract.ErrInput
		}
	}
	return nil
}
func requiredRoles(p deficontract.Protocol) []string {
	switch p {
	case deficontract.UniswapV2:
		return []string{"factory"}
	case deficontract.UniswapV3:
		return []string{"factory", "position_manager"}
	case deficontract.UniswapV4:
		return []string{"pool_manager", "position_manager", "state_view"}
	case deficontract.AaveV3:
		return []string{"pool", "data_provider"}
	case deficontract.Curve:
		return []string{"metaregistry"}
	case deficontract.Lido:
		return []string{"steth", "wsteth", "withdrawal_queue"}
	}
	return nil
}
func (d Deployment) Address(role string) common.Address {
	for _, c := range d.Contracts {
		if c.Role == role {
			return c.Address
		}
	}
	return common.Address{}
}
func (m Manifest) Digest() string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
