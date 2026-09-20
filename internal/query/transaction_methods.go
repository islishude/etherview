package query

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/abicalldata"
	"github.com/islishude/etherview/internal/abicontract"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

const maxSelectorCandidatesPerTransaction = 32

type transactionSelectorRequest struct {
	Ordinal          int    `json:"ordinal"`
	BlockNumber      string `json:"block_number"`
	BlockHash        string `json:"block_hash"`
	TransactionIndex int64  `json:"transaction_index"`
	Address          string `json:"address"`
	CodeHash         string `json:"code_hash"`
	Selector         string `json:"selector"`
	SelectorScope    string `json:"selector_scope"`
	ExactAddressOnly bool   `json:"exact_address_only"`
}

type transactionSelectorCandidate struct {
	sourceCodeHash common.Hash
	abiEntry       []byte
	signature      string
	priority       int
}

func (r *PostgresReader) projectTransactionMethods(
	ctx context.Context,
	tx pgx.Tx,
	records []transactionRecord,
) error {
	requests := make([]transactionSelectorRequest, 0, len(records))
	requestRecords := make(map[int]int, len(records))
	for index := range records {
		record := &records[index]
		if record.Model.To == nil || strongPublishedMethod(record.method) {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
			continue
		}
		input, err := hexutil.Decode(record.Model.Input)
		if err != nil || len(input) < 4 {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
			continue
		}
		address := record.method.executionAddress
		codeHash := record.method.executionCodeHash
		exactAddressOnly := false
		if len(address) != common.AddressLength || len(codeHash) != common.HashLength {
			if record.method.executionResolution.Valid ||
				!record.method.stateDiffComplete || record.Model.To == nil ||
				!common.IsHexAddress(string(*record.Model.To)) {
				projectTransactionMethod(
					&record.Model, record.method.executionResolution, record.method.decodedSignature,
				)
				continue
			}
			address = common.HexToAddress(string(*record.Model.To)).Bytes()
			codeHash = nil
			exactAddressOnly = true
		}
		ordinal := len(requests)
		requests = append(requests, transactionSelectorRequest{
			Ordinal: ordinal, BlockNumber: strconv.FormatUint(record.BlockNumber, 10),
			BlockHash: hex.EncodeToString(record.BlockHash[:]), TransactionIndex: int64(record.Index),
			Address:          hex.EncodeToString(address),
			CodeHash:         hex.EncodeToString(codeHash),
			Selector:         hex.EncodeToString(input[:4]),
			SelectorScope:    hex.EncodeToString(crypto.Keccak256(input[:4])),
			ExactAddressOnly: exactAddressOnly,
		})
		requestRecords[ordinal] = index
	}
	if len(requests) == 0 {
		return nil
	}

	encoded, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("encode transaction selector requests: %w", err)
	}
	rows, err := func() ([]dbgen.QueryTransactionSelectorCandidatesRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return nil, err
		}
		return dbgen.New(tx).QueryTransactionSelectorCandidates(ctx, int64(maxSelectorCandidatesPerTransaction+1), encoded, queryValue0)
	}()
	if err != nil {
		return fmt.Errorf("query verified transaction selector candidates: %w", err)
	}
	candidates := make(map[int][]transactionSelectorCandidate, len(requests))
	overflowPriority := make(map[int]int)
	for _, storedRow := range rows {
		var ordinal int
		var source string
		var sourceAddress, sourceCodeHash, abiEntry []byte
		var validFromText string
		var validToText pgtype.Text
		var selectorScoped bool
		var signature string
		{
			ordinal = int(storedRow.Ordinal)
			source = storedRow.Source
			sourceAddress = storedRow.SourceAddress
			sourceCodeHash = storedRow.SourceCodeHash
			abiEntry = storedRow.AbiEntry
			validFromText = storedRow.RankedValidFromBlock
			var decodeErr error
			validToText, decodeErr = dbaccess.NumericText(storedRow.RankedValidToBlock)
			if decodeErr != nil {
				return decodeErr
			}
			selectorScoped = storedRow.SelectorScoped
			signature = storedRow.Signature
		}
		if len(candidates[ordinal]) >= maxSelectorCandidatesPerTransaction {
			priority := transactionSelectorSourcePriority(abicontract.Source(source))
			if current := overflowPriority[ordinal]; current == 0 || priority < current {
				overflowPriority[ordinal] = priority
			}
			continue
		}
		candidate, ok := parseTransactionSelectorCandidate(
			source, sourceAddress, sourceCodeHash, abiEntry, validFromText, validToText, selectorScoped,
			signature,
		)
		if ok {
			candidates[ordinal] = append(candidates[ordinal], candidate)
		}
	}

	for ordinal, recordIndex := range requestRecords {
		record := &records[recordIndex]
		indexedSignature, ambiguous := decodeTransactionSelector(
			record, candidates[ordinal], overflowPriority[ordinal],
		)
		if indexedSignature.Valid {
			projectTransactionMethod(&record.Model, record.method.executionResolution, indexedSignature)
		} else if ambiguous {
			projectTransactionMethod(&record.Model, record.method.executionResolution, pgtype.Text{})
		} else {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
		}
	}
	return nil
}

func strongPublishedMethod(method transactionMethodContext) bool {
	if !method.decodedSignature.Valid || !method.decodedConfidence.Valid || !method.decodedSource.Valid {
		return false
	}
	return validTransactionMethodSignature(method.decodedSignature.String) &&
		(method.decodedConfidence.String == string(abicontract.ConfidenceVerified) ||
			method.decodedConfidence.String == string(abicontract.ConfidenceHigh))
}

func parseTransactionSelectorCandidate(
	source string,
	addressBytes, codeHashBytes, abiEntry []byte,
	validFromText string,
	validToText pgtype.Text,
	_ bool,
	signature string,
) (transactionSelectorCandidate, bool) {
	if len(addressBytes) != common.AddressLength || len(codeHashBytes) != common.HashLength {
		return transactionSelectorCandidate{}, false
	}
	if _, err := strconv.ParseUint(validFromText, 10, 64); err != nil {
		return transactionSelectorCandidate{}, false
	}
	if validToText.Valid {
		if _, err := strconv.ParseUint(validToText.String, 10, 64); err != nil {
			return transactionSelectorCandidate{}, false
		}
	}
	parsedSource := abicontract.Source(source)
	switch parsedSource {
	case abicontract.SourceVerified, abicontract.SourceCodeHash,
		abicontract.SourceProxyImplementation, abicontract.SourceDiamondFacet:
	default:
		return transactionSelectorCandidate{}, false
	}
	return transactionSelectorCandidate{
		sourceCodeHash: common.BytesToHash(codeHashBytes),
		abiEntry:       append([]byte(nil), abiEntry...),
		signature:      signature,
		priority:       transactionSelectorSourcePriority(parsedSource),
	}, true
}

func transactionSelectorSourcePriority(source abicontract.Source) int {
	switch source {
	case abicontract.SourceVerified:
		return 1
	case abicontract.SourceCodeHash:
		return 2
	case abicontract.SourceProxyImplementation, abicontract.SourceDiamondFacet:
		return 3
	default:
		return 4
	}
}

func decodeTransactionSelector(
	record *transactionRecord,
	candidates []transactionSelectorCandidate,
	overflowPriority int,
) (pgtype.Text, bool) {
	if record == nil || len(candidates) == 0 {
		return pgtype.Text{}, overflowPriority > 0
	}
	input, err := hexutil.Decode(record.Model.Input)
	if err != nil || len(input) < 4 {
		return pgtype.Text{}, false
	}
	for priority := 1; priority <= 3; priority++ {
		matches := make(map[string]string)
		for _, candidate := range candidates {
			if candidate.priority != priority {
				continue
			}
			signature, ok := abicalldata.DecodeVerifiedFunction(candidate.abiEntry, input)
			if ok && signature == candidate.signature && validTransactionMethodSignature(signature) {
				matches[signature+"\x00"+candidate.sourceCodeHash.Hex()] = signature
			}
		}
		if overflowPriority == priority {
			return pgtype.Text{}, true
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			return pgtype.Text{}, true
		}
		for _, signature := range matches {
			return pgtype.Text{String: signature, Valid: true}, false
		}
	}
	return pgtype.Text{}, false
}

// The input page is bounded to 101 rows. Candidate sets are joined by exact
// execution identity or a completed state-diff plus exact verified address
// range, and selector; no complete verified ABI document is read.
// Existing canonical route bindings preserve proxy/Diamond provenance, while
// published proxy observations make late implementation verification visible
// without replaying abi@4.
