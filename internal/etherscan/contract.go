package etherscan

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/contractartifact"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

type verifiedContractRecord struct {
	CodeHash        []byte
	SourceAddress   []byte
	Similar         bool
	ABI             []byte
	Sources         []byte
	Settings        []byte
	Language        string
	CompilerVersion string
	MatchKind       string
	ContractName    string
	FileName        string
}

func (b *PostgresBackend) verifiedContract(ctx context.Context, values url.Values) (verifiedContractRecord, error) {
	_, addressBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return verifiedContractRecord{}, err
	}
	resolved, found, err := b.artifacts.ResolveCurrent(ctx, b.chain, addressBytes)
	if err != nil {
		return verifiedContractRecord{}, fmt.Errorf("resolve verified contract: %w", err)
	}
	if len(resolved.Target.CodeHash) == 0 {
		return verifiedContractRecord{}, ErrStateUnavailable
	}
	if !found {
		return verifiedContractRecord{}, ErrContractUnverified
	}
	record := verifiedContractRecord{
		CodeHash: resolved.Target.CodeHash, SourceAddress: resolved.Source.Address,
		Similar: resolved.Resolution == contractartifact.ResolutionCodeHash,
		ABI:     resolved.Source.ABI, Sources: resolved.Source.Sources,
		Settings: resolved.Source.Settings, Language: resolved.Source.Language,
		CompilerVersion: resolved.Source.CompilerVersion,
		MatchKind:       resolved.Source.MatchType, ContractName: resolved.Source.ContractName,
		FileName: resolved.Source.FileName,
	}
	if len(record.CodeHash) != 32 {
		return verifiedContractRecord{}, errors.New("stored canonical contract code hash is invalid")
	}
	if len(resolved.Source.CodeHash) != 32 || !bytes.Equal(record.CodeHash, resolved.Source.CodeHash) {
		return verifiedContractRecord{}, errors.New("stored verified contract code hash does not match canonical code")
	}
	if len(record.SourceAddress) != common.AddressLength {
		return verifiedContractRecord{}, errors.New("stored verified contract identity is incomplete")
	}
	if record.Language != "solidity" && record.Language != "yul" && record.Language != "geas" {
		return verifiedContractRecord{}, fmt.Errorf("stored verified contract has unsupported language %q", record.Language)
	}
	if record.MatchKind != "full" && record.MatchKind != "partial" {
		return verifiedContractRecord{}, fmt.Errorf("stored verified contract has unsupported match kind %q", record.MatchKind)
	}
	if strings.TrimSpace(record.CompilerVersion) == "" || strings.TrimSpace(record.ContractName) == "" {
		return verifiedContractRecord{}, errors.New("stored verified contract identity is incomplete")
	}
	if _, err := compactJSON(record.Sources); err != nil {
		return verifiedContractRecord{}, fmt.Errorf("decode verified contract sources: %w", err)
	}
	if _, err := compactJSON(record.Settings); err != nil {
		return verifiedContractRecord{}, fmt.Errorf("decode verified contract settings: %w", err)
	}
	return record, nil
}

func (b *PostgresBackend) contractABI(ctx context.Context, values url.Values) (string, error) {
	record, err := b.verifiedContract(ctx, values)
	if err != nil {
		return "", err
	}
	if len(record.ABI) == 0 || string(record.ABI) == "null" {
		return "", ErrContractUnverified
	}
	abi, err := compactJSON(record.ABI)
	if err != nil {
		return "", fmt.Errorf("decode verified contract ABI: %w", err)
	}
	return abi, nil
}

func (b *PostgresBackend) contractSource(ctx context.Context, values url.Values) ([]sourceCodeResult, error) {
	record, err := b.verifiedContract(ctx, values)
	if err != nil {
		return nil, err
	}
	sources, err := compactJSON(record.Sources)
	if err != nil {
		return nil, fmt.Errorf("decode verified contract sources: %w", err)
	}
	abi := ""
	if len(record.ABI) != 0 && string(record.ABI) != "null" {
		abi, err = compactJSON(record.ABI)
		if err != nil {
			return nil, fmt.Errorf("decode verified contract ABI: %w", err)
		}
	}
	settings, err := sourceSettings(record.Settings)
	if err != nil {
		return nil, err
	}
	proxy, implementation, err := b.currentVerifiedProxy(ctx, values.Get("address"), record.CodeHash)
	if err != nil {
		return nil, err
	}
	similarMatch := ""
	if record.Similar {
		similarMatch = common.BytesToAddress(record.SourceAddress).Hex()
		settings.constructorArguments = ""
	}
	compilerType := "solc"
	contractFileName := ""
	if record.Language == "geas" {
		compilerType = "geas"
		contractFileName = record.FileName
	}
	return []sourceCodeResult{{
		SourceCode: sources, ABI: abi, ContractName: record.ContractName,
		CompilerVersion: record.CompilerVersion, CompilerType: compilerType,
		OptimizationUsed: settings.optimized,
		Runs:             settings.runs, ConstructorArguments: settings.constructorArguments,
		EVMVersion: settings.evmVersion, Library: settings.libraries,
		ContractFileName: contractFileName, LicenseType: settings.licenseType,
		Proxy: proxy, Implementation: implementation,
		SwarmSource: "", SimilarMatch: similarMatch, MatchKind: record.MatchKind,
	}}, nil
}

func (b *PostgresBackend) currentVerifiedProxy(
	ctx context.Context,
	rawAddress string,
	codeHash []byte,
) (string, string, error) {
	_, address, err := parseAddressParameter(rawAddress, "address")
	if err != nil {
		return "", "", err
	}
	var implementation []byte
	err = b.db.QueryRowContext(ctx, dbgen.EtherscanVerifiedProxy, b.chain, address, codeHash).Scan(&implementation)
	if errors.Is(err, sql.ErrNoRows) {
		return "0", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("query verified proxy binding: %w", err)
	}
	if len(implementation) != common.AddressLength {
		return "", "", errors.New("stored verified proxy implementation is invalid")
	}
	return "1", common.BytesToAddress(implementation).Hex(), nil
}

type contractSettings struct {
	optimized            string
	runs                 string
	evmVersion           string
	libraries            string
	constructorArguments string
	licenseType          string
}

func sourceSettings(raw []byte) (contractSettings, error) {
	var object map[string]json.RawMessage
	if err := decodeRawObject(raw, &object); err != nil {
		return contractSettings{}, fmt.Errorf("decode verified contract settings: %w", err)
	}
	result := contractSettings{optimized: "0", runs: "0"}
	if optimizerRaw := object["optimizer"]; len(optimizerRaw) != 0 {
		var optimizer struct {
			Enabled bool            `json:"enabled"`
			Runs    json.RawMessage `json:"runs"`
		}
		if err := decodeRawObject(optimizerRaw, &optimizer); err != nil {
			return contractSettings{}, fmt.Errorf("decode optimizer settings: %w", err)
		}
		if optimizer.Enabled {
			result.optimized = "1"
		}
		if len(optimizer.Runs) != 0 {
			runs, err := jsonDecimal(optimizer.Runs)
			if err != nil {
				return contractSettings{}, fmt.Errorf("decode optimizer runs: %w", err)
			}
			result.runs = runs
		}
	}
	if value := object["evmVersion"]; len(value) != 0 {
		if err := json.Unmarshal(value, &result.evmVersion); err != nil {
			return contractSettings{}, fmt.Errorf("decode EVM version: %w", err)
		}
	}
	if value := object["libraries"]; len(value) != 0 {
		var err error
		result.libraries, err = compactJSON(value)
		if err != nil {
			return contractSettings{}, fmt.Errorf("decode libraries: %w", err)
		}
	}
	for key, destination := range map[string]*string{
		"constructorArguments": &result.constructorArguments,
		"licenseType":          &result.licenseType,
	} {
		if value := object[key]; len(value) != 0 {
			if err := json.Unmarshal(value, destination); err != nil {
				return contractSettings{}, fmt.Errorf("decode %s: %w", key, err)
			}
		}
	}
	return result, nil
}

func jsonDecimal(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
	}
	value, err := parseCanonicalDecimal(text)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func (b *PostgresBackend) contractCreation(ctx context.Context, values url.Values) ([]contractCreationResult, error) {
	rawAddresses := strings.Split(values.Get("contractaddresses"), ",")
	if len(rawAddresses) == 0 || len(rawAddresses) > 5 {
		return nil, invalidParameter("contractaddresses must contain between 1 and 5 addresses")
	}
	seen := make(map[string]struct{}, len(rawAddresses))
	addresses := make([]common.Address, 0, len(rawAddresses))
	for _, raw := range rawAddresses {
		address, _, err := parseAddressParameter(strings.TrimSpace(raw), "contractaddresses")
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address.Hex())
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidParameter("contractaddresses contains a duplicate address")
		}
		seen[key] = struct{}{}
		addresses = append(addresses, address)
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	result := make([]contractCreationResult, 0, len(addresses))
	for _, address := range addresses {
		item, err := b.oneContractCreation(ctx, tx, address)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit contract creation snapshot: %w", err)
	}
	return result, nil
}

func (b *PostgresBackend) oneContractCreation(
	ctx context.Context,
	queryer enrichmentQueryer,
	requested common.Address,
) (contractCreationResult, error) {
	requestedBytes := requested.Bytes()
	var sourceKind string
	var receiptJSON, transactionJSON, factoryBytes, traceInput []byte
	var transactionHashBytes, blockHashBytes []byte
	var blockNumberText, timestampText string
	var transactionIndex int64
	var tracePath, callType sql.NullString
	var traceDepth sql.NullInt64
	err := queryer.QueryRowContext(ctx, dbgen.EtherscanContractCreation, b.chain, requestedBytes).Scan(
		&sourceKind, &receiptJSON, &transactionJSON, &transactionHashBytes, &blockHashBytes,
		&blockNumberText, &timestampText, &transactionIndex,
		&tracePath, &traceDepth, &callType, &factoryBytes, &traceInput,
	)
	if err == sql.ErrNoRows {
		return contractCreationResult{}, b.contractCreationAbsence(ctx, queryer)
	}
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("query contract creation: %w", err)
	}
	if transactionIndex < 0 {
		return contractCreationResult{}, errors.New("stored contract creation index is negative")
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return contractCreationResult{}, err
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return contractCreationResult{}, err
	}
	blockNumber, err := storedUint256(blockNumberText, "contract creation block number")
	if err != nil {
		return contractCreationResult{}, err
	}
	if _, err := storedUint256(timestampText, "contract creation timestamp"); err != nil {
		return contractCreationResult{}, err
	}
	transaction, sender, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("decode contract creation transaction: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return contractCreationResult{}, errors.New("stored contract creation transaction identity is invalid")
	}
	creationBytecode := hexutil.Encode(transaction.Data())
	contractFactory := ""
	switch sourceKind {
	case "top_level":
		if transaction.To() != nil || tracePath.Valid || traceDepth.Valid || callType.Valid || factoryBytes != nil || traceInput != nil {
			return contractCreationResult{}, errors.New("stored top-level contract creation has trace-only fields")
		}
		receipt, err := decodeStoredReceipt(receiptJSON, transaction, blockHash, blockNumber, transactionIndex)
		if err != nil {
			return contractCreationResult{}, fmt.Errorf("decode contract creation receipt: %w", err)
		}
		if receipt.ContractAddress != requested {
			return contractCreationResult{}, errors.New("stored contract creation receipt identity does not match indexed row")
		}
	case "trace":
		if len(receiptJSON) != 0 || !tracePath.Valid || !traceDepth.Valid || !callType.Valid ||
			(callType.String != "CREATE" && callType.String != "CREATE2") || len(factoryBytes) != 20 {
			return contractCreationResult{}, errors.New("stored factory contract creation identity is invalid")
		}
		depth, pathErr := validateTracePath(tracePath.String)
		if pathErr != nil || depth == 0 || int64(depth) != traceDepth.Int64 {
			return contractCreationResult{}, errors.New("stored factory contract creation trace path is invalid")
		}
		contractFactory, err = optionalChecksumAddress(factoryBytes)
		if err != nil {
			return contractCreationResult{}, fmt.Errorf("checksum contract factory: %w", err)
		}
		creationBytecode = hexutil.Encode(traceInput)
	default:
		return contractCreationResult{}, errors.New("stored contract creation source kind is invalid")
	}
	if _, err := ethrpc.ParseData(creationBytecode); err != nil {
		return contractCreationResult{}, errors.New("stored contract creation bytecode is invalid")
	}
	if len(creationBytecode) > b.maxVerificationInputBytes*2+2 {
		return contractCreationResult{}, errors.New("stored contract creation bytecode exceeds the response limit")
	}
	creator, err := checksumAddress(sender)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum contract creator: %w", err)
	}
	contract, err := checksumAddress(requested)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum created contract: %w", err)
	}
	return contractCreationResult{
		ContractAddress: contract, ContractCreator: creator,
		TxHash: strings.ToLower(transactionHash.Hex()), BlockNumber: blockNumberText,
		Timestamp: timestampText, ContractFactory: contractFactory,
		CreationBytecode: strings.ToLower(creationBytecode),
	}, nil
}

// contractCreationAbsence returns ErrNotFound only when genesis-to-tip core
// coverage and the trace stage are both complete. Without that proof, a
// missing row could be an unindexed factory CREATE/CREATE2 and must be exposed
// as an unavailable capability rather than a misleading empty result.
func (b *PostgresBackend) contractCreationAbsence(ctx context.Context, queryer enrichmentQueryer) error {
	if _, err := b.requireCanonicalStageRange(ctx, queryer, traceStage, "0", nil, ErrTraceUnavailable); err != nil {
		return err
	}
	return ErrNotFound
}
