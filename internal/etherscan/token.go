package etherscan

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

func (b *PostgresBackend) accountTokenTransfers(ctx context.Context, action string, values url.Values) ([]tokenTransfer, error) {
	_, addressBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	standard := map[string]string{
		"tokentx": "erc20", "tokennfttx": "erc721", "token1155tx": "erc1155",
	}[action]
	if standard == "" {
		return nil, invalidParameter("unsupported token transfer action %q", action)
	}
	var contractArgument any
	if raw := strings.TrimSpace(values.Get("contractaddress")); raw != "" {
		_, contractBytes, parseErr := parseAddressParameter(raw, "contractaddress")
		if parseErr != nil {
			return nil, parseErr
		}
		contractArgument = contractBytes
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	start, end, err := decimalRange(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	tip, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, start, end, ErrTokenUnavailable)
	if err != nil {
		return nil, err
	}
	var endArgument any
	if end != nil {
		endArgument = *end
	}
	query := fmt.Sprintf(tokenTransfersSQL,
		page.direction, page.direction, page.direction, page.direction, page.direction,
	)
	rows, err := tx.QueryContext(ctx, query,
		b.chain, addressBytes, standard, start, endArgument, contractArgument, page.limit, page.offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query %s token transfers: %w", standard, err)
	}
	defer rows.Close() //nolint:errcheck
	result := make([]tokenTransfer, 0, page.limit)
	for rows.Next() {
		item, scanErr := scanTokenTransfer(rows, standard, tip)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s token transfers: %w", standard, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close %s token transfers: %w", standard, err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit %s token transfer snapshot: %w", standard, err)
	}
	return result, nil
}

func scanTokenTransfer(scanner rowScanner, expectedStandard, tipText string) (tokenTransfer, error) {
	var (
		blockNumberText, standard, eventKind    string
		blockHashBytes, transactionHashBytes    []byte
		tokenAddressBytes, fromBytes, toBytes   []byte
		transactionJSON, receiptJSON, blockJSON []byte
		logIndex, subIndex, transactionIndex    int64
		tokenID, amount, name, symbol           sql.NullString
		decimals                                sql.NullInt64
	)
	if err := scanner.Scan(
		&blockNumberText, &blockHashBytes, &logIndex, &subIndex,
		&transactionHashBytes, &tokenAddressBytes, &standard, &eventKind,
		&fromBytes, &toBytes, &tokenID, &amount,
		&transactionJSON, &receiptJSON, &blockJSON, &transactionIndex,
		&name, &symbol, &decimals,
	); err != nil {
		return tokenTransfer{}, fmt.Errorf("scan token transfer: %w", err)
	}
	if standard != expectedStandard {
		return tokenTransfer{}, errors.New("stored token transfer standard does not match query")
	}
	if eventKind != "transfer" && eventKind != "mint" && eventKind != "burn" {
		return tokenTransfer{}, errors.New("stored token transfer event kind is invalid")
	}
	if logIndex < 0 || subIndex < 0 || transactionIndex < 0 {
		return tokenTransfer{}, errors.New("stored token transfer index is negative")
	}
	blockNumber, err := storedUint256(blockNumberText, "token transfer block number")
	if err != nil {
		return tokenTransfer{}, err
	}
	tip, err := storedUint256(tipText, "canonical tip")
	if err != nil || tip.Cmp(blockNumber) < 0 {
		return tokenTransfer{}, errors.New("stored token transfer canonical tip is invalid")
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return tokenTransfer{}, err
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return tokenTransfer{}, err
	}
	tokenAddress, err := addressFromBytes(tokenAddressBytes)
	if err != nil {
		return tokenTransfer{}, err
	}

	transaction, _, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token transaction raw JSON: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return tokenTransfer{}, errors.New("stored token transaction raw identity does not match event")
	}

	receipt, err := decodeStoredReceiptWithBlockContext(
		receiptJSON,
		blockJSON,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
	)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token receipt raw JSON: %w", err)
	}

	block, err := decodeStoredBlockProjection(blockJSON, blockHash, blockNumber)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token block raw JSON: %w", err)
	}

	item := tokenTransfer{
		BlockNumber: blockNumber.String(), Hash: strings.ToLower(transactionHash.Hex()),
		BlockHash: strings.ToLower(blockHash.Hex()), TransactionIndex: strconv.FormatInt(transactionIndex, 10),
		Input: "deprecated", FunctionName: "",
	}
	item.ContractAddress, err = checksumAddress(tokenAddress)
	if err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token contract: %w", err)
	}
	if name.Valid {
		if len(name.String) > 1<<20 {
			return tokenTransfer{}, errors.New("stored token name is too large")
		}
		item.TokenName = name.String
	}
	if symbol.Valid {
		if len(symbol.String) > 1<<20 {
			return tokenTransfer{}, errors.New("stored token symbol is too large")
		}
		item.TokenSymbol = symbol.String
	}
	if decimals.Valid {
		if decimals.Int64 < 0 || decimals.Int64 > 255 {
			return tokenTransfer{}, errors.New("stored token decimals are invalid")
		}
		item.TokenDecimal = strconv.FormatInt(decimals.Int64, 10)
	}
	if standard == "erc721" && !decimals.Valid {
		item.TokenDecimal = "0"
	}
	if item.From, err = optionalChecksumAddress(fromBytes); err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token transfer sender: %w", err)
	}
	if item.To, err = optionalChecksumAddress(toBytes); err != nil {
		return tokenTransfer{}, fmt.Errorf("checksum token transfer recipient: %w", err)
	}
	item.TimeStamp = decimalUint64(uint64(*block.Timestamp))
	item.Nonce = decimalUint64(transaction.Nonce())
	item.Gas = decimalUint64(transaction.Gas())
	gasPrice, err := effectiveGasPrice(transaction, receipt)
	if err != nil {
		return tokenTransfer{}, err
	}
	if item.GasPrice, err = decimalBig(gasPrice); err != nil {
		return tokenTransfer{}, fmt.Errorf("decode token transaction gas price: %w", err)
	}
	item.CumulativeGasUsed = decimalUint64(receipt.CumulativeGasUsed)
	item.GasUsed = decimalUint64(receipt.GasUsed)
	input := hexutil.Encode(transaction.Data())
	if len(input) >= 10 {
		item.MethodID = strings.ToLower(input[:10])
	}
	confirmations := new(big.Int).Sub(tip, blockNumber)
	confirmations.Add(confirmations, big.NewInt(1))
	item.Confirmations = confirmations.String()

	if tokenID.Valid {
		parsed, parseErr := storedUint256(tokenID.String, "token ID")
		if parseErr != nil {
			return tokenTransfer{}, parseErr
		}
		item.TokenID = parsed.String()
	}
	if amount.Valid {
		parsed, parseErr := storedUint256(amount.String, "token transfer amount")
		if parseErr != nil {
			return tokenTransfer{}, parseErr
		}
		switch standard {
		case "erc20":
			item.Value = parsed.String()
		case "erc1155":
			item.TokenValue = parsed.String()
		}
	}
	switch standard {
	case "erc20":
		if item.Value == "" || item.TokenID != "" {
			return tokenTransfer{}, errors.New("stored ERC-20 transfer shape is invalid")
		}
	case "erc721":
		if item.TokenID == "" {
			return tokenTransfer{}, errors.New("stored ERC-721 transfer has no token ID")
		}
	case "erc1155":
		if item.TokenID == "" || item.TokenValue == "" {
			return tokenTransfer{}, errors.New("stored ERC-1155 transfer shape is invalid")
		}
	}
	return item, nil
}

type storedTokenContract struct {
	address, codeHash, observedHash []byte
	standard, confidence            string
	name, symbol, totalSupply       sql.NullString
	decimals                        sql.NullInt64
	metadataState, observedBlock    string
}

func (b *PostgresBackend) canonicalTokenContract(
	ctx context.Context,
	queryer enrichmentQueryer,
	addressBytes []byte,
) (storedTokenContract, error) {
	var token storedTokenContract
	err := queryer.QueryRowContext(ctx, canonicalTokenContractSQL, b.chain, addressBytes).Scan(
		&token.address, &token.codeHash, &token.standard, &token.confidence,
		&token.name, &token.symbol, &token.decimals, &token.totalSupply,
		&token.metadataState, &token.observedBlock, &token.observedHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storedTokenContract{}, ErrNotFound
	}
	if err != nil {
		return storedTokenContract{}, fmt.Errorf("query canonical token contract: %w", err)
	}
	if !bytes.Equal(token.address, addressBytes) || len(token.codeHash) != 32 || len(token.observedHash) != 32 {
		return storedTokenContract{}, errors.New("stored token contract identity is invalid")
	}
	if _, err := storedUint256(token.observedBlock, "token observation block"); err != nil {
		return storedTokenContract{}, err
	}
	switch token.standard {
	case "erc20", "erc721", "erc1155", "unknown":
	default:
		return storedTokenContract{}, errors.New("stored token standard is invalid")
	}
	switch token.confidence {
	case "verified", "high", "inferred", "guess":
	default:
		return storedTokenContract{}, errors.New("stored token confidence is invalid")
	}
	switch token.metadataState {
	case "pending", "complete", "unavailable", "failed":
	default:
		return storedTokenContract{}, errors.New("stored token metadata state is invalid")
	}
	if token.name.Valid && len(token.name.String) > 1<<20 || token.symbol.Valid && len(token.symbol.String) > 1<<20 {
		return storedTokenContract{}, errors.New("stored token metadata is too large")
	}
	if token.decimals.Valid && (token.decimals.Int64 < 0 || token.decimals.Int64 > 255) {
		return storedTokenContract{}, errors.New("stored token decimals are invalid")
	}
	if token.totalSupply.Valid {
		if _, err := storedUint256(token.totalSupply.String, "token total supply"); err != nil {
			return storedTokenContract{}, err
		}
	}
	return token, nil
}

func (b *PostgresBackend) tokenInformation(ctx context.Context, values url.Values) ([]tokenInfo, error) {
	address, addressBytes, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, "0", nil, ErrTokenUnavailable); err != nil {
		return nil, err
	}
	token, err := b.canonicalTokenContract(ctx, tx, addressBytes)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit token information snapshot: %w", err)
	}
	contractAddress, err := checksumAddress(address)
	if err != nil {
		return nil, fmt.Errorf("checksum token contract: %w", err)
	}
	item := tokenInfo{ContractAddress: contractAddress, TokenType: strings.ToUpper(token.standard)}
	if token.name.Valid {
		item.TokenName = token.name.String
	}
	if token.symbol.Valid {
		item.Symbol = token.symbol.String
	}
	if token.decimals.Valid {
		item.Divisor = strconv.FormatInt(token.decimals.Int64, 10)
	} else if token.standard == "erc721" {
		item.Divisor = "0"
	}
	if token.standard == "erc20" && b.state != nil {
		if supply, stateErr := b.state.ERC20TotalSupply(ctx, contractAddress); stateErr == nil {
			if _, parseErr := parseCanonicalDecimal(supply); parseErr != nil {
				return nil, ErrStateUnavailable
			}
			item.TotalSupply = supply
		}
	}
	return []tokenInfo{item}, nil
}

func (b *PostgresBackend) tokenSupply(ctx context.Context, values url.Values) (string, error) {
	address, _, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return "", err
	}
	if b.state == nil {
		return "", ErrStateUnavailable
	}
	supply, err := b.state.ERC20TotalSupply(ctx, address.String())
	if err != nil {
		return "", ErrStateUnavailable
	}
	if _, err := parseCanonicalDecimal(supply); err != nil {
		return "", ErrStateUnavailable
	}
	return supply, nil
}

func (b *PostgresBackend) tokenBalance(ctx context.Context, values url.Values) (string, error) {
	contract, _, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return "", err
	}
	owner, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return "", err
	}
	if tag := strings.TrimSpace(values.Get("tag")); tag != "" && tag != "latest" {
		return "", invalidParameter("tag must be latest")
	}
	if b.state == nil {
		return "", ErrStateUnavailable
	}
	balance, err := b.state.ERC20Balance(ctx, contract.String(), owner.String())
	if err != nil {
		return "", ErrStateUnavailable
	}
	if _, err := parseCanonicalDecimal(balance); err != nil {
		return "", ErrStateUnavailable
	}
	return balance, nil
}

func (b *PostgresBackend) tokenHolders(_ context.Context, values url.Values) ([]tokenHolder, error) {
	_, _, err := parseAddressParameter(values.Get("contractaddress"), "contractaddress")
	if err != nil {
		return nil, err
	}
	if _, err := parsePagination(values); err != nil {
		return nil, err
	}
	// Enumerating all current ERC-20 holders cannot be proven from JSON-RPC.
	// The event ledger is intentionally not exposed as current state unless a
	// future reconciliation persists a fixed-canonical-block holder set.
	return nil, ErrStateUnavailable
}

const tokenTransfersSQL = `
SELECT event.block_number::text, event.block_hash, event.log_index, event.sub_index,
       event.transaction_hash, event.token_address, event.standard, event.event_kind,
       event.from_address, event.to_address, event.token_id::text, event.amount::text,
       inclusion.raw, receipt.raw, block.raw, inclusion.tx_index,
       metadata.name, metadata.symbol, metadata.decimals
FROM token_events AS event
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = event.chain_id
 AND inclusion.block_number = event.block_number
 AND inclusion.block_hash = event.block_hash
 AND inclusion.tx_hash = event.transaction_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
JOIN blocks AS block
  ON block.chain_id = event.chain_id
 AND block.number = event.block_number
 AND block.hash = event.block_hash
LEFT JOIN LATERAL (
    SELECT token.name, token.symbol, token.decimals
    FROM token_contracts AS token
    JOIN canonical_blocks AS observed
      ON observed.chain_id = token.chain_id
     AND observed.number = token.observed_block_number
     AND observed.block_hash = token.observed_block_hash
    WHERE token.chain_id = event.chain_id
      AND token.address = event.token_address
      AND token.observed_block_number <= event.block_number
    ORDER BY token.observed_block_number DESC, token.updated_at DESC, token.code_hash DESC
    LIMIT 1
) AS metadata ON true
WHERE event.chain_id = $1::numeric
  AND event.canonical = TRUE
  AND (event.from_address = $2 OR event.to_address = $2)
  AND event.standard = $3
  AND event.event_kind IN ('transfer', 'mint', 'burn')
  AND event.block_number >= $4::numeric
  AND ($5::numeric IS NULL OR event.block_number <= $5::numeric)
  AND ($6::bytea IS NULL OR event.token_address = $6)
ORDER BY event.block_number %s, inclusion.tx_index %s, event.log_index %s,
         event.sub_index %s, event.block_hash %s
LIMIT $7 OFFSET $8`

const canonicalTokenContractSQL = `
SELECT token.address, token.code_hash, token.standard, token.confidence,
       token.name, token.symbol, token.decimals, token.total_supply::text,
       token.metadata_state, token.observed_block_number::text, token.observed_block_hash
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = $1::numeric AND token.address = $2
ORDER BY token.observed_block_number DESC, token.updated_at DESC, token.code_hash DESC
LIMIT 1`
