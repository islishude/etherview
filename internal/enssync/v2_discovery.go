package enssync

import (
	"bytes"
	"context"
	"maps"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (c *Collector) copyForStep() *Collector {
	copy := *c
	copy.manifest.Deployments = slices.Clone(c.manifest.Deployments)
	copy.profiles = maps.Clone(c.profiles)
	return &copy
}
func (c *Collector) deploymentAt(address common.Address, number uint64) (Deployment, bool) {
	for _, d := range c.manifest.Deployments {
		if d.Address == address && d.Number <= number {
			return d, true
		}
	}
	return Deployment{}, false
}

// Factory coverage starts before instance creation. Reading all bounded block
// logs lets a newly authenticated proxy contribute initialization logs emitted
// before ProxyDeployed in the same transaction, and records written before link.
func (c *Collector) discoverV2(ctx context.Context, source int64, session rpcSession, block collectedBlock) (collectedBlock, []verifiedDeployment, error) {
	if !c.manifest.hasV2() {
		return block, nil, nil
	}
	addresses := make([][]byte, 0, len(block.Logs))
	seen := map[common.Address]bool{}
	for _, log := range block.Logs {
		if !seen[log.Address] {
			addresses = append(addresses, log.Address.Bytes())
			seen[log.Address] = true
		}
	}
	rows, e := dbgen.New(c.repo.db).ListENSSyncEmitterDeployments(ctx, source, addresses)
	if e != nil {
		return block, nil, ErrStorage
	}
	for _, row := range rows {
		address := common.BytesToAddress(row.Address)
		if _, ok := c.profiles[address]; ok {
			continue
		}
		profileName := ""
		var profile contractProfile
		for _, name := range []string{"v2_registry", "v2_resolver"} {
			p, e := loadProfile(name)
			if e != nil {
				return block, nil, e
			}
			if bytes.Equal(p.digest[:], row.AbiDigest) {
				profileName = name
				profile = p
				break
			}
		}
		if profileName == "" {
			return block, nil, ErrUnsupportedDeployment
		}
		number, e := uintNumber(row.Number)
		if e != nil {
			return block, nil, e
		}
		c.manifest.Deployments = append(c.manifest.Deployments, Deployment{Address: address, Profile: profileName, Number: number, Hash: common.BytesToHash(row.BlockHash), CodeHash: common.BytesToHash(row.CodeHash), Implementation: common.BytesToAddress(row.ImplementationAddress), ImplementationCodeHash: common.BytesToHash(row.ImplementationCodeHash)})
		c.profiles[address] = profile
	}
	discovered := []verifiedDeployment{}
	for _, log := range block.Logs {
		d, known := c.deploymentAt(log.Address, log.BlockNumber)
		if !known || d.Profile != "v2_factory" || len(log.Topics) == 0 {
			continue
		}
		profile := c.profiles[log.Address]
		event, e := profile.abi.EventByID(log.Topics[0])
		if e != nil {
			continue
		}
		v, e := unpackEvent(*event, log)
		if e != nil {
			return block, nil, e
		}
		if event.Name != "ProxyDeployed" {
			continue
		}
		implementation, address := v["implementation"].(common.Address), v["proxyAddress"].(common.Address)
		var template Implementation
		found := false
		for _, p := range c.manifest.Implementations {
			if p.Address == implementation {
				template = p
				found = true
				break
			}
		}
		if !found {
			continue
		} // The factory may deploy unrelated implementations.
		if address == implementation || address == (common.Address{}) {
			return block, nil, ErrUnsupportedDeployment
		}
		if previous, known := c.deploymentAt(address, log.BlockNumber); known {
			if previous.Hash != log.BlockHash || previous.Implementation != implementation {
				return block, nil, ErrSourceIdentity
			}
			continue
		}
		instance := Deployment{Address: address, Profile: template.Profile, Number: log.BlockNumber, Hash: log.BlockHash, CodeHash: template.ProxyCodeHash, Implementation: implementation, ImplementationCodeHash: template.CodeHash}
		if session.endpoint == nil {
			return block, nil, ErrUnsupportedDeployment
		}
		header, e := session.verifyDeployment(ctx, instance, block.Header)
		if e != nil {
			return block, nil, e
		}
		p, e := loadProfile(template.Profile)
		if e != nil {
			return block, nil, e
		}
		c.manifest.Deployments = append(c.manifest.Deployments, instance)
		c.profiles[address] = p
		discovered = append(discovered, verifiedDeployment{Deployment: instance, Header: header})
	}
	// User-owned subregistry and resolver links are retained by projectV2,
	// but never enroll their targets as standard-event emitters. An unknown
	// target leaves that branch without trusted projections; it cannot disable
	// collection for the entire source. Only reviewed deployments and verified
	// factory instances may contribute the logs below.
	filtered := make([]types.Log, 0, len(block.Logs))
	for _, log := range block.Logs {
		if _, known := c.deploymentAt(log.Address, log.BlockNumber); known {
			filtered = append(filtered, log)
		}
	}
	block.Logs = filtered
	return block, discovered, nil
}
