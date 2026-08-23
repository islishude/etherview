package app

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/query"
)

func (adapter proxyReaderAdapter) proxyUpgradeHistory(page query.ProxyUpgradePage) (gen.ProxyUpgradeHistory, error) {
	if len(page.Items) > 100 {
		return gen.ProxyUpgradeHistory{}, errors.New("proxy upgrade history exceeds the public bound")
	}
	address, err := proxyAddress(page.ProxyAddress)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, fmt.Errorf("invalid upgrade-history proxy address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.ProxyUpgradeHistory{}, err
	}
	model := gen.ProxyUpgradeHistory{
		ProxyAddress: address,
		Snapshot:     snapshot,
		Coverage:     coverage,
		Items:        make([]gen.ProxyUpgrade, len(page.Items)),
	}
	for index := range page.Items {
		model.Items[index], err = proxyUpgrade(page.Items[index])
		if err != nil {
			return gen.ProxyUpgradeHistory{}, fmt.Errorf("invalid proxy upgrade: %w", err)
		}
		if greaterProxyQuantity(page.Items[index].BlockNumber, page.Snapshot.Number) {
			return gen.ProxyUpgradeHistory{}, errors.New("proxy upgrade is newer than its snapshot")
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) proxyInitializationHistory(page query.ProxyInitializationPage) (gen.ProxyInitializationHistory, error) {
	if len(page.Items) > 100 {
		return gen.ProxyInitializationHistory{}, errors.New("proxy initialization history exceeds the public bound")
	}
	address, err := proxyAddress(page.ContractAddress)
	if err != nil {
		return gen.ProxyInitializationHistory{}, fmt.Errorf("invalid initialization-history address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.ProxyInitializationHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.ProxyInitializationHistory{}, err
	}
	model := gen.ProxyInitializationHistory{
		ContractAddress: address,
		Snapshot:        snapshot,
		Coverage:        coverage,
		Items:           make([]gen.ProxyInitialization, len(page.Items)),
	}
	for index := range page.Items {
		item := page.Items[index]
		if greaterProxyQuantity(item.BlockNumber, page.Snapshot.Number) {
			return gen.ProxyInitializationHistory{}, errors.New("proxy initialization is newer than its snapshot")
		}
		blockNumber, parseErr := proxyQuantity(item.BlockNumber)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		logIndex, parseErr := proxyQuantity(item.LogIndex)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		version, parseErr := proxyQuantity(item.Version)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		blockHash, parseErr := proxyHash(item.BlockHash)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		transactionHash, parseErr := proxyHash(item.TransactionHash)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		implementation, parseErr := proxyHistoricalIdentity(item.Implementation)
		if parseErr != nil {
			return gen.ProxyInitializationHistory{}, parseErr
		}
		model.Items[index] = gen.ProxyInitialization{
			Version:         version,
			BlockNumber:     blockNumber,
			BlockHash:       blockHash,
			BlockTimestamp:  item.BlockTimestamp.UTC(),
			TransactionHash: transactionHash,
			LogIndex:        logIndex,
			Implementation:  implementation,
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) diamondCutHistory(page query.DiamondCutPage) (gen.DiamondCutHistory, error) {
	if len(page.Items) > 100 {
		return gen.DiamondCutHistory{}, errors.New("DiamondCut history exceeds the public bound")
	}
	address, err := proxyAddress(page.DiamondAddress)
	if err != nil {
		return gen.DiamondCutHistory{}, fmt.Errorf("invalid Diamond history address: %w", err)
	}
	snapshot, err := adapter.proxySnapshot(page.Snapshot)
	if err != nil {
		return gen.DiamondCutHistory{}, err
	}
	coverage, err := proxyCoverage(page.Coverage, page.Snapshot.Number)
	if err != nil {
		return gen.DiamondCutHistory{}, err
	}
	model := gen.DiamondCutHistory{
		DiamondAddress: address, Snapshot: snapshot, Coverage: coverage,
		Items: make([]gen.DiamondCut, len(page.Items)),
	}
	for index, item := range page.Items {
		if greaterProxyQuantity(item.BlockNumber, page.Snapshot.Number) {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut is newer than its snapshot")
		}
		blockNumber, parseErr := proxyQuantity(item.BlockNumber)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		transactionIndex, parseErr := proxyQuantity(item.TransactionIndex)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		logIndex, parseErr := proxyQuantity(item.LogIndex)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		blockHash, parseErr := proxyHash(item.BlockHash)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		transactionHash, parseErr := proxyHash(item.TransactionHash)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		initAddress, parseErr := proxyAddress(item.InitAddress)
		if parseErr != nil {
			return gen.DiamondCutHistory{}, parseErr
		}
		if len(item.InitCalldata) > enrich.DiamondMaxRawReturnBytes*2+2 {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut init calldata exceeds the public bound")
		}
		if _, parseErr = ethrpc.ParseData(item.InitCalldata); parseErr != nil {
			return gen.DiamondCutHistory{}, errors.New("DiamondCut init calldata is invalid")
		}
		cuts := make([]gen.DiamondFacetCut, len(item.Cuts))
		for cutIndex, cut := range item.Cuts {
			action := gen.DiamondFacetCutAction(cut.Action)
			if !action.Valid() || cut.CutIndex != cutIndex {
				return gen.DiamondCutHistory{}, errors.New("DiamondCut entry is invalid")
			}
			facetAddress, addressErr := proxyAddress(cut.FacetAddress)
			if addressErr != nil {
				return gen.DiamondCutHistory{}, addressErr
			}
			selectors := make([]gen.FunctionSelector, len(cut.Selectors))
			for selectorIndex, selector := range cut.Selectors {
				if !validFunctionSelector(selector) {
					return gen.DiamondCutHistory{}, errors.New("DiamondCut selector is invalid")
				}
				selectors[selectorIndex] = gen.FunctionSelector(selector)
			}
			cuts[cutIndex] = gen.DiamondFacetCut{
				CutIndex: cut.CutIndex, Action: action,
				FacetAddress: facetAddress, Selectors: selectors,
			}
		}
		model.Items[index] = gen.DiamondCut{
			BlockNumber: blockNumber, BlockHash: blockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: transactionHash,
			TransactionIndex: transactionIndex, LogIndex: logIndex,
			InitAddress: initAddress, InitCalldata: item.InitCalldata, Cuts: cuts,
		}
	}
	return model, nil
}

func (adapter proxyReaderAdapter) proxySnapshot(snapshot query.ProxySnapshot) (gen.CatalogSnapshot, error) {
	number, err := proxyQuantity(snapshot.Number)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy snapshot number: %w", err)
	}
	hash, err := proxyHash(snapshot.Hash)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy snapshot hash: %w", err)
	}
	chainID, err := proxyQuantity(adapter.chainID)
	if err != nil {
		return gen.CatalogSnapshot{}, fmt.Errorf("invalid proxy chain identity: %w", err)
	}
	return gen.CatalogSnapshot{ChainId: chainID, BlockNumber: number, BlockHash: hash}, nil
}

func proxyCurrentIdentity(identity *query.ProxyIdentity) (*gen.ProxyContractIdentity, error) {
	if identity == nil {
		return nil, nil
	}
	address, err := proxyAddress(identity.Address)
	if err != nil {
		return nil, err
	}
	codeHash, err := proxyHash(identity.CodeHash)
	if err != nil {
		return nil, err
	}
	state := gen.ProxyVerificationStateUnverified
	if identity.Verified {
		state = gen.ProxyVerificationStateVerified
	}
	model := &gen.ProxyContractIdentity{
		Address:           address,
		CodeHash:          codeHash,
		VerificationState: state,
	}
	if identity.ArtifactResolution != "" {
		resolution := gen.ContractArtifactResolution(identity.ArtifactResolution)
		if !resolution.Valid() ||
			(identity.Verified && resolution != gen.ContractArtifactResolutionExactAddress) ||
			(!identity.Verified && resolution != gen.ContractArtifactResolutionCodeHash) {
			return nil, errors.New("proxy artifact resolution disagrees with exact verification")
		}
		model.ArtifactResolution = &resolution
	} else if identity.Verified {
		return nil, errors.New("verified proxy identity lacks exact artifact resolution")
	}
	if identity.ArtifactKind != "" {
		value := gen.ProxyArtifactKind(identity.ArtifactKind)
		if !value.Valid() {
			return nil, fmt.Errorf("invalid proxy artifact kind %q", identity.ArtifactKind)
		}
		model.ArtifactKind = &value
	}
	if identity.StandardVersion != "" {
		value := gen.ProxyContractIdentityStandardVersion(identity.StandardVersion)
		if !value.Valid() {
			return nil, fmt.Errorf("invalid proxy identity standard version %q", identity.StandardVersion)
		}
		model.StandardVersion = &value
	}
	return model, nil
}

func proxyHistoricalIdentity(identity query.ProxyIdentity) (gen.ProxyHistoricalIdentity, error) {
	address, err := proxyAddress(identity.Address)
	if err != nil {
		return gen.ProxyHistoricalIdentity{}, err
	}
	model := gen.ProxyHistoricalIdentity{Address: address}
	if identity.CodeHash != "" {
		codeHash, parseErr := proxyHash(identity.CodeHash)
		if parseErr != nil {
			return gen.ProxyHistoricalIdentity{}, parseErr
		}
		model.CodeHash = &codeHash
	}
	state := gen.ProxyVerificationStateUnverified
	if identity.Verified {
		state = gen.ProxyVerificationStateVerified
	}
	model.VerificationState = &state
	return model, nil
}

func proxyEvidence(evidence query.ProxyEvidence) (gen.ProxyRecognitionEvidence, error) {
	result := gen.ProxyEvidenceResult(evidence.Result)
	if !result.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid result %q", evidence.Result)
	}
	source := gen.ProxyEvidenceSource(evidence.Source)
	if !source.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid source %q", evidence.Source)
	}
	subject := gen.ProxyEvidenceSubject(evidence.Subject)
	if !subject.Valid() {
		return gen.ProxyRecognitionEvidence{}, fmt.Errorf("invalid subject %q", evidence.Subject)
	}
	model := gen.ProxyRecognitionEvidence{Result: result, Source: source, Subject: subject}
	if evidence.Address != "" {
		value, err := proxyAddress(evidence.Address)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.Address = &value
	}
	if evidence.CodeHash != "" {
		value, err := proxyHash(evidence.CodeHash)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.CodeHash = &value
	}
	if evidence.BlockNumber != "" {
		value, err := proxyQuantity(evidence.BlockNumber)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.BlockNumber = &value
	}
	if evidence.BlockHash != "" {
		value, err := proxyHash(evidence.BlockHash)
		if err != nil {
			return gen.ProxyRecognitionEvidence{}, err
		}
		model.BlockHash = &value
	}
	return model, nil
}

func proxyManagement(management *query.ProxyManagement) (*gen.ProxyManagement, error) {
	if management == nil {
		return nil, nil
	}
	kind := gen.ProxyManagementKind(management.Kind)
	if !kind.Valid() {
		return nil, fmt.Errorf("invalid proxy management kind %q", management.Kind)
	}
	target, err := proxyCurrentIdentity(&management.Target)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy management target: %w", err)
	}
	model := &gen.ProxyManagement{Kind: kind, Target: *target}
	if management.AffectedProxyCount != "" {
		count, parseErr := proxyQuantity(management.AffectedProxyCount)
		if parseErr != nil {
			return nil, parseErr
		}
		model.AffectedProxyCount = &count
	}
	return model, nil
}

func proxyCoverage(coverage query.ProxyHistoryCoverage, snapshotNumber string) (gen.ProxyHistoryCoverage, error) {
	state := gen.ProxyHistoryCoverageState(coverage.State)
	if !state.Valid() {
		return gen.ProxyHistoryCoverage{}, fmt.Errorf("invalid proxy history coverage %q", coverage.State)
	}
	model := gen.ProxyHistoryCoverage{State: state}
	if coverage.FromBlock != "" {
		value, err := proxyQuantity(coverage.FromBlock)
		if err != nil {
			return gen.ProxyHistoryCoverage{}, err
		}
		model.FromBlock = &value
	}
	if coverage.ToBlock != "" {
		value, err := proxyQuantity(coverage.ToBlock)
		if err != nil {
			return gen.ProxyHistoryCoverage{}, err
		}
		model.ToBlock = &value
	}
	if coverage.FromBlock != "" && coverage.ToBlock != "" &&
		greaterProxyQuantity(coverage.FromBlock, coverage.ToBlock) {
		return gen.ProxyHistoryCoverage{}, errors.New("proxy history coverage range is inverted")
	}
	if coverage.ToBlock != "" && greaterProxyQuantity(coverage.ToBlock, snapshotNumber) {
		return gen.ProxyHistoryCoverage{}, errors.New("proxy history coverage exceeds its snapshot")
	}
	return model, nil
}

func proxyUpgrade(upgrade query.ProxyUpgrade) (gen.ProxyUpgrade, error) {
	changeType := gen.ProxyUpgradeChangeType(upgrade.ChangeType)
	if !changeType.Valid() {
		return gen.ProxyUpgrade{}, fmt.Errorf("invalid upgrade change type %q", upgrade.ChangeType)
	}
	evidenceType := gen.ProxyUpgradeEvidenceType(upgrade.EvidenceType)
	if !evidenceType.Valid() {
		return gen.ProxyUpgrade{}, fmt.Errorf("invalid upgrade evidence type %q", upgrade.EvidenceType)
	}
	blockNumber, err := proxyQuantity(upgrade.BlockNumber)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	blockHash, err := proxyHash(upgrade.BlockHash)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	newImplementation, err := proxyHistoricalIdentity(upgrade.NewImplementation)
	if err != nil {
		return gen.ProxyUpgrade{}, err
	}
	model := gen.ProxyUpgrade{
		ChangeType:        changeType,
		EvidenceType:      evidenceType,
		BlockNumber:       blockNumber,
		BlockHash:         blockHash,
		BlockTimestamp:    upgrade.BlockTimestamp.UTC(),
		NewImplementation: newImplementation,
	}
	if upgrade.OldImplementation != nil {
		value, parseErr := proxyHistoricalIdentity(*upgrade.OldImplementation)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.OldImplementation = &value
	}
	if upgrade.TransactionHash != "" {
		value, parseErr := proxyHash(upgrade.TransactionHash)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.TransactionHash = &value
	}
	if upgrade.LogIndex != "" {
		value, parseErr := proxyQuantity(upgrade.LogIndex)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.LogIndex = &value
	}
	if upgrade.EmitterAddress != "" {
		value, parseErr := proxyAddress(upgrade.EmitterAddress)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.EmitterAddress = &value
	}
	if upgrade.Beacon != nil {
		value, parseErr := proxyHistoricalIdentity(*upgrade.Beacon)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.Beacon = &value
	}
	if upgrade.Management != nil {
		kind := gen.ProxyManagementKind(upgrade.Management.Kind)
		if !kind.Valid() {
			return gen.ProxyUpgrade{}, fmt.Errorf("invalid historical management kind %q", upgrade.Management.Kind)
		}
		target, parseErr := proxyHistoricalIdentity(upgrade.Management.Target)
		if parseErr != nil {
			return gen.ProxyUpgrade{}, parseErr
		}
		model.Management = &gen.ProxyHistoricalManagement{Kind: kind, Target: target}
	}
	return model, nil
}

func proxyAddress(value string) (gen.Address, error) {
	address, err := query.ChecksumAddress(value)
	return gen.Address(address), err
}

func proxyHash(value string) (gen.Hash, error) {
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return "", err
	}
	return gen.Hash(hash.Hex()), nil
}

func proxyQuantity(value string) (gen.Quantity, error) {
	if _, err := proxyQuantityInteger(value); err != nil {
		return "", err
	}
	return gen.Quantity(value), nil
}

func proxyQuantityInteger(value string) (*big.Int, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return nil, errors.New("quantity is not a canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return nil, errors.New("quantity is not a canonical decimal")
		}
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 || integer.BitLen() > 256 {
		return nil, errors.New("quantity exceeds uint256")
	}
	return integer, nil
}

func greaterProxyQuantity(left, right string) bool {
	leftInteger, leftOK := new(big.Int).SetString(left, 10)
	rightInteger, rightOK := new(big.Int).SetString(right, 10)
	return leftOK && rightOK && leftInteger.Cmp(rightInteger) > 0
}
