package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
)

const maxReadTimeLogABICandidates = 16

type persistedLogDecoding struct {
	status         sql.NullString
	signature      sql.NullString
	source         sql.NullString
	confidence     sql.NullString
	arguments      []byte
	candidates     []byte
	warning        sql.NullString
	targetAddress  []byte
	targetCodeHash []byte
	sourceAddress  []byte
	sourceCodeHash []byte
}

type logABICandidate struct {
	abi           []byte
	source        enrich.ABISource
	sourceKind    string
	address       common.Address
	codeHash      common.Hash
	selectorScope common.Hash
	validFrom     uint64
	validTo       sql.NullString
}

func resolveTransactionLogDecoding(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	topics []common.Hash,
	data []byte,
	persisted persistedLogDecoding,
) (TransactionLogDecoding, error) {
	stored, hasStored, err := publicPersistedLogDecoding(persisted)
	if err != nil {
		return TransactionLogDecoding{}, err
	}
	if hasStored && !bytes.Equal(persisted.targetAddress, address[:]) {
		hasStored = false
	}
	if hasStored && (stored.Confidence == "verified" || stored.Confidence == "high") {
		return stored, nil
	}

	identity, candidates, err := loadLogABICandidates(ctx, tx, chainID, blockNumber, blockHash, address)
	if err != nil {
		return TransactionLogDecoding{}, err
	}
	if len(candidates) == 0 {
		if hasStored {
			return stored, nil
		}
		return emptyLogDecoding("unavailable", "no ABI is available for the log address at this block"), nil
	}
	if len(candidates) > maxReadTimeLogABICandidates {
		return emptyLogDecoding("malformed", "ABI candidate limit exceeded"), nil
	}

	registry := enrich.NewABIRegistry()
	provenance := make(map[enrich.ABISource]ABISource, len(candidates))
	for _, candidate := range candidates {
		var validTo *uint64
		if candidate.validTo.Valid {
			value, err := strconv.ParseUint(candidate.validTo.String, 10, 64)
			if err != nil {
				return TransactionLogDecoding{}, ErrCorruptData
			}
			validTo = &value
		}
		binding := enrich.ABIBinding{
			Identity: identity, Source: candidate.source,
			SourceAddress: candidate.address, SourceCodeHash: candidate.codeHash,
			SelectorScope:  candidate.selectorScope,
			ValidFromBlock: candidate.validFrom, ValidToBlock: validTo,
		}
		if err := registry.RegisterJSON(binding, candidate.abi); err != nil {
			return emptyLogDecoding("malformed", "stored ABI document is invalid or exceeds decoding limits"), nil
		}
		provenance[candidate.source] = ABISource{
			Kind: candidate.sourceKind, Address: candidate.address.Hex(), CodeHash: candidate.codeHash.Hex(),
		}
	}
	result := registry.DecodeLog(identity, topics, data)
	if result.Status == enrich.DecodeUnknown && hasStored {
		return stored, nil
	}
	return publicDecodeResult(result, provenance), nil
}

func loadLogABICandidates(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
) (enrich.ABIIdentity, []logABICandidate, error) {
	return loadLogABICandidatesForCodeHash(
		ctx, tx, chainID, blockNumber, blockHash, address, nil,
	)
}

func loadLogABICandidatesForCodeHash(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	codeHash *common.Hash,
) (enrich.ABIIdentity, []logABICandidate, error) {
	identity := enrich.ABIIdentity{
		ChainID: chainID, Address: address, BlockNumber: blockNumber,
		BlockHash: common.BytesToHash(blockHash),
	}
	if codeHash != nil {
		identity.CodeHash = *codeHash
	}
	var expectedCodeHash any
	if codeHash != nil {
		expectedCodeHash = codeHash[:]
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogTransactionLogABICandidates, chainID, address[:], fmt.Sprint(blockNumber), maxReadTimeLogABICandidates+1,
		expectedCodeHash,
	)
	if err != nil {
		return identity, nil, fmt.Errorf("load log ABI candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var candidates []logABICandidate
	for rows.Next() {
		var candidate logABICandidate
		var source, sourceKind string
		var sourceAddress, sourceCodeHash, selectorScope, targetCodeHash []byte
		var validFrom string
		if err := rows.Scan(
			&targetCodeHash, &candidate.abi, &source, &sourceKind,
			&sourceAddress, &sourceCodeHash, &selectorScope, &validFrom, &candidate.validTo,
		); err != nil {
			return identity, nil, fmt.Errorf("scan log ABI candidate: %w", err)
		}
		if len(targetCodeHash) != common.HashLength || len(sourceAddress) != common.AddressLength ||
			len(sourceCodeHash) != common.HashLength || len(selectorScope) != common.HashLength {
			return identity, nil, ErrCorruptData
		}
		parsedValidFrom, parseErr := strconv.ParseUint(validFrom, 10, 64)
		if parseErr != nil {
			return identity, nil, ErrCorruptData
		}
		identity.CodeHash = common.BytesToHash(targetCodeHash)
		candidate.source = enrich.ABISource(source)
		candidate.sourceKind = sourceKind
		candidate.address = common.BytesToAddress(sourceAddress)
		candidate.codeHash = common.BytesToHash(sourceCodeHash)
		candidate.selectorScope = common.BytesToHash(selectorScope)
		candidate.validFrom = parsedValidFrom
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return identity, nil, fmt.Errorf("iterate log ABI candidates: %w", err)
	}
	return identity, candidates, nil
}

func publicPersistedLogDecoding(value persistedLogDecoding) (TransactionLogDecoding, bool, error) {
	if !value.status.Valid {
		return TransactionLogDecoding{}, false, nil
	}
	result := emptyLogDecoding(value.status.String, value.warning.String)
	result.Signature = value.signature.String
	result.Confidence = value.confidence.String
	if result.Signature != "" {
		if open := len(result.Signature) - 1; open > 0 {
			for open >= 0 && result.Signature[open] != '(' {
				open--
			}
			if open > 0 {
				result.EventName = result.Signature[:open]
			}
		}
	}
	if len(value.arguments) > 0 && !json.Valid(value.arguments) {
		return TransactionLogDecoding{}, false, ErrCorruptData
	}
	if len(value.candidates) > 0 && !json.Valid(value.candidates) {
		return TransactionLogDecoding{}, false, ErrCorruptData
	}
	if len(value.arguments) > 0 {
		if err := json.Unmarshal(value.arguments, &result.Arguments); err != nil {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
	}
	if len(value.candidates) > 0 {
		if err := json.Unmarshal(value.candidates, &result.Candidates); err != nil {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
	}
	if value.source.Valid {
		kind := mapStoredABISource(value.source.String)
		if kind == "" {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
		result.ABISource = &ABISource{Kind: kind}
		sourceAddress, sourceCodeHash := value.sourceAddress, value.sourceCodeHash
		if len(sourceAddress) == 0 && kind != "proxy_implementation" {
			sourceAddress, sourceCodeHash = value.targetAddress, value.targetCodeHash
		}
		if len(sourceAddress) != common.AddressLength || len(sourceCodeHash) != common.HashLength {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
		result.ABISource.Address = common.BytesToAddress(sourceAddress).Hex()
		result.ABISource.CodeHash = common.BytesToHash(sourceCodeHash).Hex()
	}
	if result.Status == "ambiguous" {
		result.EventName, result.Signature = "", ""
		result.Arguments = []TransactionLogArgument{}
		result.ABISource = nil
	}
	return result, true, nil
}

func publicDecodeResult(
	result enrich.DecodeResult,
	provenance map[enrich.ABISource]ABISource,
) TransactionLogDecoding {
	decoded := emptyLogDecoding(string(result.Status), result.Warning)
	decoded.EventName = result.Name
	decoded.Signature = result.Signature
	decoded.Confidence = string(result.Confidence)
	decoded.Candidates = append(decoded.Candidates, result.Candidates...)
	decoded.Arguments = make([]TransactionLogArgument, len(result.Arguments))
	for index, argument := range result.Arguments {
		decoded.Arguments[index] = TransactionLogArgument{
			Name: argument.Name, Type: argument.Type, Indexed: argument.Indexed,
			Hashed: argument.Hashed, Value: argument.Value,
		}
	}
	if source, ok := provenance[result.Source]; ok {
		copy := source
		decoded.ABISource = &copy
	}
	if result.SourceAddress != (common.Address{}) && result.SourceCodeHash != (common.Hash{}) {
		decoded.ABISource = &ABISource{
			Kind: mapDecodeABISource(result.Source), Address: result.SourceAddress.Hex(), CodeHash: result.SourceCodeHash.Hex(),
		}
	}
	if result.Status == enrich.DecodeAmbiguous {
		decoded.EventName, decoded.Signature = "", ""
		decoded.Arguments = []TransactionLogArgument{}
		decoded.ABISource = nil
	}
	return decoded
}

func mapDecodeABISource(source enrich.ABISource) string {
	switch source {
	case enrich.ABISourceVerified:
		return "exact_address"
	case enrich.ABISourceCodeHash:
		return "code_hash"
	case enrich.ABISourceProxyImplementation:
		return "proxy_implementation"
	case enrich.ABISourceDiamondFacet:
		return "diamond_facet"
	case enrich.ABISourceSignatureDatabase:
		return "signature_database"
	default:
		return ""
	}
}

func emptyLogDecoding(status, warning string) TransactionLogDecoding {
	return TransactionLogDecoding{
		Status: status, Arguments: []TransactionLogArgument{}, Candidates: []string{}, Warning: warning,
	}
}

func mapStoredABISource(source string) string {
	switch source {
	case "verified":
		return "exact_address"
	case "proxy_implementation":
		return "proxy_implementation"
	case "diamond_facet":
		return "diamond_facet"
	case "code_hash":
		return "code_hash"
	case "builtin":
		return "builtin"
	case "signature_database":
		return "signature_database"
	default:
		return ""
	}
}
