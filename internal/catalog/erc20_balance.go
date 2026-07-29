package catalog

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
)

func (catalog *Postgres) ERC20Balances(
	ctx context.Context,
	request ERC20BalanceRequest,
) (ERC20BalancePage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return ERC20BalancePage{}, err
	}
	ownerAddress, checksummedOwner, err := checksumInputAddress(request.Owner)
	if err != nil {
		return ERC20BalancePage{}, err
	}
	normalizedOwner := "0x" + hex.EncodeToString(ownerAddress)
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return ERC20BalancePage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return ERC20BalancePage{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var snapshot Snapshot
	hasBoundary := false
	boundaryAddress := make([]byte, 20)
	if request.Cursor == "" {
		snapshot, err = readCanonicalSnapshot(ctx, tx, request.ChainID)
	} else {
		var cursor erc20BalanceCursor
		if decodeErr := decodeCursor(request.Cursor, &cursor); decodeErr != nil ||
			cursor.Version != cursorVersion || cursor.ChainID != request.ChainID ||
			cursor.Owner != normalizedOwner {
			return ERC20BalancePage{}, ErrInvalidCursor
		}
		snapshot = Snapshot{
			ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber,
			BlockHash: cursor.SnapshotHash,
		}
		boundaryAddress, err = decodeFixedHex(cursor.TokenAddress, 20)
		if err != nil || cursor.TokenAddress != "0x"+hex.EncodeToString(boundaryAddress) {
			return ERC20BalancePage{}, ErrInvalidCursor
		}
		hasBoundary = true
		if err = validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
			return ERC20BalancePage{}, err
		}
	}
	if err != nil {
		return ERC20BalancePage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return ERC20BalancePage{}, err
	}
	rows, err := tx.QueryContext(ctx, erc20BalanceCandidatesSQL,
		request.ChainID, snapshot.BlockNumber, ownerAddress,
		hasBoundary, boundaryAddress, limit+1,
	)
	if err != nil {
		return ERC20BalancePage{}, fmt.Errorf("query ERC-20 balances: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	candidateRows := make([][]byte, 0, limit+1)
	for rows.Next() {
		var address []byte
		if err := rows.Scan(&address); err != nil {
			return ERC20BalancePage{}, fmt.Errorf("scan ERC-20 balance candidate: %w", err)
		}
		if len(address) != 20 {
			return ERC20BalancePage{}, ErrCorruptData
		}
		candidateRows = append(candidateRows, address)
	}
	if err := rows.Err(); err != nil {
		return ERC20BalancePage{}, fmt.Errorf("iterate ERC-20 balances: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ERC20BalancePage{}, fmt.Errorf("close ERC-20 balance candidates: %w", err)
	}
	hasMore := len(candidateRows) > limit
	if hasMore {
		candidateRows = candidateRows[:limit]
	}

	type candidate struct {
		address  []byte
		contract TokenContract
	}
	candidates := make([]candidate, 0, len(candidateRows))
	stateCandidates := make([]ERC20BalanceCandidate, 0, len(candidateRows))
	for _, address := range candidateRows {
		contract, lookupErr := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, address)
		if lookupErr != nil {
			if lookupErr == ErrNotFound {
				continue
			}
			return ERC20BalancePage{}, lookupErr
		}
		if contract.Standard != "erc20" {
			continue
		}
		checksummed, checksumErr := checksumAddressBytes(address)
		if checksumErr != nil {
			return ERC20BalancePage{}, checksumErr
		}
		candidates = append(candidates, candidate{address: address, contract: contract})
		stateCandidates = append(stateCandidates, ERC20BalanceCandidate{TokenAddress: checksummed})
	}
	if len(stateCandidates) > 0 && catalog.erc20State == nil {
		return ERC20BalancePage{}, erc20StateUnavailable(snapshot)
	}
	if err := commitRead(tx); err != nil {
		return ERC20BalancePage{}, err
	}

	observations := make([]ERC20BalanceObservation, len(stateCandidates))
	if len(stateCandidates) > 0 {
		observations, err = catalog.erc20State.ERC20Balances(ctx, snapshot, checksummedOwner, stateCandidates)
		if err != nil {
			return ERC20BalancePage{}, erc20StateUnavailable(snapshot)
		}
	}
	if len(observations) != len(candidates) {
		return ERC20BalancePage{}, ErrCorruptData
	}
	items := make([]ERC20Balance, 0, len(candidates))
	for index, observation := range observations {
		balance, ok := new(big.Int).SetString(observation.Balance, 10)
		if !ok || balance.Sign() < 0 || balance.BitLen() > 256 ||
			observation.Confidence != NFTStateConfidenceRPCExact {
			return ERC20BalancePage{}, ErrCorruptData
		}
		if balance.Sign() == 0 {
			continue
		}
		contract := candidates[index].contract
		items = append(items, ERC20Balance{
			ChainID: request.ChainID, Owner: checksummedOwner,
			TokenAddress: stateCandidates[index].TokenAddress,
			Balance:      observation.Balance, Confidence: observation.Confidence,
			Name: contract.Name, Symbol: contract.Symbol, Decimals: contract.Decimals,
		})
	}

	next := ""
	if hasMore && len(candidateRows) > 0 {
		last := candidateRows[len(candidateRows)-1]
		next, err = encodeCursor(erc20BalanceCursor{
			Version: cursorVersion, ChainID: request.ChainID, Owner: normalizedOwner,
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			TokenAddress: "0x" + hex.EncodeToString(last),
		})
		if err != nil {
			return ERC20BalancePage{}, err
		}
	}
	return ERC20BalancePage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func erc20StateUnavailable(snapshot Snapshot) error {
	return StageUnavailableError{
		Stage: StageToken, State: StageUnavailable,
		BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
	}
}

const erc20BalanceCandidatesSQL = `
SELECT d.token_address
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = $1::numeric
  AND d.block_number <= $2::numeric
  AND d.owner_address = $3
  AND d.token_id IS NULL
  AND d.canonical = TRUE
  AND ($4::boolean = FALSE OR d.token_address > $5)
GROUP BY d.token_address
ORDER BY d.token_address
LIMIT $6`
