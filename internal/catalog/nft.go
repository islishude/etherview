package catalog

import (
	"context"
	"encoding/hex"
	"fmt"
)

func (catalog *Postgres) NFTOwner(ctx context.Context, chainID, tokenAddressText, tokenID string) (NFTOwnership, error) {
	if err := validateChainID(chainID); err != nil || !canonicalUint256(tokenID) {
		return NFTOwnership{}, ErrInvalidInput
	}
	tokenAddress, checksummedToken, err := checksumInputAddress(tokenAddressText)
	if err != nil {
		return NFTOwnership{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return NFTOwnership{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readCanonicalSnapshot(ctx, tx, chainID)
	if err != nil {
		return NFTOwnership{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return NFTOwnership{}, err
	}
	contract, err := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, tokenAddress)
	if err != nil {
		return NFTOwnership{}, err
	}
	if contract.Standard != "erc721" {
		return NFTOwnership{}, fmt.Errorf("%w: owner lookup requires an ERC-721 contract", ErrInvalidInput)
	}
	if catalog.nftState == nil {
		return NFTOwnership{}, nftStateUnavailable(snapshot)
	}
	// Do not hold a repeatable-read PostgreSQL snapshot or pool connection while
	// waiting for an external state RPC. The reconciler binds the call to the
	// immutable block hash and rechecks canonicality before returning.
	if err := commitRead(tx); err != nil {
		return NFTOwnership{}, err
	}
	observation, err := catalog.nftState.Owner(ctx, snapshot, checksummedToken, tokenID)
	if err != nil {
		return NFTOwnership{}, nftStateUnavailable(snapshot)
	}
	if !observation.Exists {
		return NFTOwnership{}, ErrNotFound
	}
	if observation.Confidence != NFTStateConfidenceRPCExact {
		return NFTOwnership{}, ErrCorruptData
	}
	_, owner, err := checksumInputAddress(observation.Owner)
	if err != nil {
		return NFTOwnership{}, ErrCorruptData
	}
	result := NFTOwnership{
		ChainID: chainID, TokenAddress: checksummedToken, TokenID: tokenID,
		Owner: owner, Balance: "1", Confidence: observation.Confidence, Snapshot: snapshot,
	}
	return result, nil
}

func (catalog *Postgres) NFTBalances(ctx context.Context, request NFTBalanceRequest) (NFTBalancePage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return NFTBalancePage{}, err
	}
	ownerAddress, checksummedOwner, err := checksumInputAddress(request.Owner)
	if err != nil {
		return NFTBalancePage{}, err
	}
	normalizedOwner := "0x" + hex.EncodeToString(ownerAddress)
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return NFTBalancePage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return NFTBalancePage{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var snapshot Snapshot
	hasBoundary := false
	boundaryAddress := make([]byte, 20)
	boundaryTokenID := "0"
	if request.Cursor == "" {
		snapshot, err = readCanonicalSnapshot(ctx, tx, request.ChainID)
	} else {
		var cursor nftBalanceCursor
		if decodeErr := decodeCursor(request.Cursor, &cursor); decodeErr != nil || cursor.Version != cursorVersion ||
			cursor.ChainID != request.ChainID || cursor.Owner != normalizedOwner || !canonicalUint256(cursor.TokenID) {
			return NFTBalancePage{}, ErrInvalidCursor
		}
		snapshot = Snapshot{ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash}
		boundaryAddress, err = decodeFixedHex(cursor.TokenAddress, 20)
		if err != nil || cursor.TokenAddress != "0x"+hex.EncodeToString(boundaryAddress) {
			return NFTBalancePage{}, ErrInvalidCursor
		}
		boundaryTokenID = cursor.TokenID
		hasBoundary = true
		if err = validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
			return NFTBalancePage{}, err
		}
	}
	if err != nil {
		return NFTBalancePage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return NFTBalancePage{}, err
	}
	rows, err := tx.QueryContext(ctx, nftBalanceCandidatesSQL,
		request.ChainID, snapshot.BlockNumber, ownerAddress, hasBoundary,
		boundaryAddress, boundaryTokenID, limit+1,
	)
	if err != nil {
		return NFTBalancePage{}, fmt.Errorf("query NFT balances: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	type candidateRow struct {
		address []byte
		tokenID string
	}
	candidateRows := make([]candidateRow, 0, limit+1)
	for rows.Next() {
		var candidate candidateRow
		if err := rows.Scan(&candidate.address, &candidate.tokenID); err != nil {
			return NFTBalancePage{}, fmt.Errorf("scan NFT balance: %w", err)
		}
		if len(candidate.address) != 20 || !canonicalUint256(candidate.tokenID) {
			return NFTBalancePage{}, ErrCorruptData
		}
		candidateRows = append(candidateRows, candidate)
	}
	if err := rows.Err(); err != nil {
		return NFTBalancePage{}, fmt.Errorf("iterate NFT balances: %w", err)
	}
	if err := rows.Close(); err != nil {
		return NFTBalancePage{}, fmt.Errorf("close NFT balance candidates: %w", err)
	}
	hasMore := len(candidateRows) > limit
	if hasMore {
		candidateRows = candidateRows[:limit]
	}
	candidates := make([]NFTBalanceCandidate, 0, len(candidateRows))
	for _, candidate := range candidateRows {
		contract, lookupErr := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, candidate.address)
		if lookupErr != nil {
			if lookupErr == ErrNotFound {
				continue
			}
			return NFTBalancePage{}, lookupErr
		}
		if contract.Standard != "erc721" && contract.Standard != "erc1155" {
			continue
		}
		checksummedToken, checksumErr := checksumAddressBytes(candidate.address)
		if checksumErr != nil {
			return NFTBalancePage{}, checksumErr
		}
		candidates = append(candidates, NFTBalanceCandidate{
			Standard: contract.Standard, TokenAddress: checksummedToken, TokenID: candidate.tokenID,
		})
	}
	if len(candidates) > 0 && catalog.nftState == nil {
		return NFTBalancePage{}, nftStateUnavailable(snapshot)
	}
	// Candidate discovery is complete and copied into local bounded values.
	// Release the database snapshot before potentially slow state RPC calls;
	// NFTStateReconciler owns exact-block and post-call canonicality checks.
	if err := commitRead(tx); err != nil {
		return NFTBalancePage{}, err
	}
	observations := make([]NFTBalanceObservation, len(candidates))
	if len(candidates) > 0 {
		observations, err = catalog.nftState.Balances(ctx, snapshot, checksummedOwner, candidates)
		if err != nil {
			return NFTBalancePage{}, nftStateUnavailable(snapshot)
		}
		if len(observations) != len(candidates) {
			return NFTBalancePage{}, ErrCorruptData
		}
	}
	items := make([]NFTBalance, 0, len(candidates))
	for index, candidate := range candidates {
		observation := observations[index]
		if !canonicalUint256(observation.Balance) || observation.Confidence != NFTStateConfidenceRPCExact {
			return NFTBalancePage{}, ErrCorruptData
		}
		if observation.Balance == "0" {
			continue
		}
		items = append(items, NFTBalance{
			ChainID: request.ChainID, Owner: checksummedOwner, TokenAddress: candidate.TokenAddress,
			TokenID: candidate.TokenID, Balance: observation.Balance, Confidence: observation.Confidence,
		})
	}
	next := ""
	if hasMore && len(candidateRows) > 0 {
		last := candidateRows[len(candidateRows)-1]
		next, err = encodeCursor(nftBalanceCursor{
			Version: cursorVersion, ChainID: request.ChainID, Owner: normalizedOwner,
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			TokenAddress: "0x" + hex.EncodeToString(last.address), TokenID: last.tokenID,
		})
		if err != nil {
			return NFTBalancePage{}, err
		}
	}
	return NFTBalancePage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func nftStateUnavailable(snapshot Snapshot) error {
	return StageUnavailableError{
		Stage: StageToken, State: StageUnavailable,
		BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
	}
}

const nftBalanceCandidatesSQL = `
SELECT d.token_address, d.token_id::text
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = $1::numeric
  AND d.block_number <= $2::numeric
  AND d.owner_address = $3
  AND d.token_id IS NOT NULL
  AND d.canonical = true
  AND (
      $4::boolean = false OR
      (d.token_address, d.token_id) > ($5, $6::numeric)
  )
GROUP BY d.token_address, d.token_id
ORDER BY d.token_address, d.token_id
LIMIT $7`
