package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
)

type delegationCursor struct {
	Version            int    `json:"v"`
	Kind               string `json:"kind"`
	ChainID            string `json:"chain_id"`
	Address            string `json:"address"`
	SnapshotNumber     string `json:"snapshot_number"`
	SnapshotHash       string `json:"snapshot_hash"`
	BlockNumber        string `json:"block_number"`
	TransactionIndex   string `json:"transaction_index"`
	AuthorizationIndex string `json:"authorization_index"`
}

func (catalog *Postgres) TransactionAuthorizations(
	ctx context.Context, request TransactionResourceRequest,
) (TransactionAuthorizationPage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "authorizations", StageStateDiff)
	if err != nil {
		return TransactionAuthorizationPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	page := TransactionAuthorizationPage{Identity: resolution.identity, Items: []EIP7702Authorization{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := tx.QueryContext(ctx, transactionAuthorizationsSQL,
			request.ChainID, resolution.blockHash, resolution.txHash,
			resolution.limit+1, resolution.offset,
		)
		if queryErr != nil {
			return TransactionAuthorizationPage{}, fmt.Errorf("list transaction authorizations: %w", queryErr)
		}
		defer rows.Close() //nolint:errcheck
		for rows.Next() {
			var item EIP7702Authorization
			var index int64
			var delegate, authority, r, s []byte
			var skipReason sql.NullString
			if err := rows.Scan(
				&index, &item.ChainID, &item.Nonce, &delegate, &item.YParity,
				&r, &s, &authority, &item.SignatureStatus,
				&item.ApplicationStatus, &skipReason,
			); err != nil {
				return TransactionAuthorizationPage{}, fmt.Errorf("scan transaction authorization: %w", err)
			}
			if index < 0 || len(delegate) != common.AddressLength || len(r) != common.HashLength ||
				len(s) != common.HashLength || (len(authority) != 0 && len(authority) != common.AddressLength) ||
				!canonicalUint256(item.ChainID) || !canonicalUint256(item.Nonce) ||
				(item.YParity != 0 && item.YParity != 1) {
				return TransactionAuthorizationPage{}, ErrCorruptData
			}
			item.Index = strconv.FormatInt(index, 10)
			item.Delegate = common.BytesToAddress(delegate).Hex()
			item.R = "0x" + hex.EncodeToString(r)
			item.S = "0x" + hex.EncodeToString(s)
			if len(authority) != 0 {
				value := common.BytesToAddress(authority).Hex()
				item.Authority = &value
			}
			if skipReason.Valid {
				item.SkipReason = &skipReason.String
			}
			page.Items = append(page.Items, item)
		}
		if err := rows.Err(); err != nil {
			return TransactionAuthorizationPage{}, fmt.Errorf("iterate transaction authorizations: %w", err)
		}
		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("authorizations", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionAuthorizationPage{}, err
			}
		}
	}
	if err := commitRead(tx); err != nil {
		return TransactionAuthorizationPage{}, err
	}
	return page, nil
}

func (catalog *Postgres) AddressDelegations(
	ctx context.Context, request AddressDelegationRequest,
) (DelegationHistoryPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return DelegationHistoryPage{}, err
	}
	authority, normalized, err := checksumInputAddress(request.Address)
	if err != nil {
		return DelegationHistoryPage{}, err
	}
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return DelegationHistoryPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return DelegationHistoryPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
	if err != nil {
		return DelegationHistoryPage{}, err
	}
	boundary := delegationCursor{}
	hasBoundary := false
	if request.Cursor != "" {
		if decodeCursor(request.Cursor, &boundary) != nil || boundary.Version != cursorVersion ||
			boundary.Kind != "delegations" || boundary.ChainID != request.ChainID ||
			boundary.Address != normalized || boundary.SnapshotNumber != snapshot.BlockNumber ||
			boundary.SnapshotHash != snapshot.BlockHash || !canonicalUint256(boundary.BlockNumber) ||
			!canonicalUint256(boundary.TransactionIndex) || !canonicalUint256(boundary.AuthorizationIndex) {
			return DelegationHistoryPage{}, ErrInvalidCursor
		}
		hasBoundary = true
	}
	rows, err := tx.QueryContext(ctx, addressDelegationsSQL,
		request.ChainID, authority, snapshot.BlockNumber, hasBoundary,
		boundary.BlockNumber, boundary.TransactionIndex, boundary.AuthorizationIndex, limit+1,
	)
	if err != nil {
		return DelegationHistoryPage{}, fmt.Errorf("list address delegations: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	page := DelegationHistoryPage{Items: []DelegationHistoryItem{}, Snapshot: snapshot}
	for rows.Next() {
		var item DelegationHistoryItem
		var blockHash, transactionHash, delegate, previous []byte
		if err := rows.Scan(
			&item.BlockNumber, &blockHash, &transactionHash, &item.TransactionIndex,
			&item.AuthorizationIndex, &delegate, &previous,
		); err != nil {
			return DelegationHistoryPage{}, fmt.Errorf("scan address delegation: %w", err)
		}
		if !canonicalUint256(item.BlockNumber) || !canonicalUint256(item.TransactionIndex) ||
			!canonicalUint256(item.AuthorizationIndex) || len(blockHash) != common.HashLength ||
			len(transactionHash) != common.HashLength || len(delegate) != common.AddressLength ||
			(len(previous) != 0 && len(previous) != common.AddressLength) {
			return DelegationHistoryPage{}, ErrCorruptData
		}
		item.Authority = common.BytesToAddress(authority).Hex()
		item.Delegate = common.BytesToAddress(delegate).Hex()
		item.BlockHash = common.BytesToHash(blockHash).Hex()
		item.TransactionHash = common.BytesToHash(transactionHash).Hex()
		if common.BytesToAddress(delegate) == (common.Address{}) {
			item.Kind = "cleared"
		} else if len(previous) == 0 || common.BytesToAddress(previous) == (common.Address{}) {
			item.Kind = "delegated"
		} else {
			item.Kind = "redelegated"
			value := common.BytesToAddress(previous).Hex()
			item.PreviousDelegate = &value
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DelegationHistoryPage{}, fmt.Errorf("iterate address delegations: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeCursor(delegationCursor{
			Version: cursorVersion, Kind: "delegations", ChainID: request.ChainID,
			Address: normalized, SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			BlockNumber: last.BlockNumber, TransactionIndex: last.TransactionIndex,
			AuthorizationIndex: last.AuthorizationIndex,
		})
		if err != nil {
			return DelegationHistoryPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return DelegationHistoryPage{}, err
	}
	return page, nil
}

const transactionAuthorizationsSQL = `
SELECT authorization_index, authorization_chain_id::text,
       authorization_nonce::text, delegate_address, y_parity, r, s,
       authority, signature_status, application_status, skip_reason
FROM eip7702_authorizations
WHERE chain_id = $1::numeric AND block_hash = $2 AND transaction_hash = $3
  AND canonical
ORDER BY authorization_index
LIMIT $4 OFFSET $5`

const addressDelegationsSQL = `
WITH ordered AS (
    SELECT authorization.block_number, authorization.block_hash,
           authorization.transaction_hash, authorization.transaction_index,
           authorization.authorization_index, authorization.delegate_address,
           lag(authorization.delegate_address) OVER (
               ORDER BY authorization.block_number, authorization.transaction_index,
                        authorization.authorization_index
           ) AS previous_delegate
    FROM eip7702_authorizations AS authorization
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = authorization.chain_id
     AND canonical.number = authorization.block_number
     AND canonical.block_hash = authorization.block_hash
    WHERE authorization.chain_id = $1::numeric
      AND authorization.authority = $2
      AND authorization.application_status = 'applied'
      AND authorization.canonical
      AND authorization.block_number <= $3::numeric
)
SELECT block_number::text, block_hash, transaction_hash,
       transaction_index::text, authorization_index::text,
       delegate_address, previous_delegate
FROM ordered
WHERE NOT $4 OR (block_number, transaction_index, authorization_index)
    < ($5::numeric, $6::numeric, $7::numeric)
ORDER BY block_number DESC, transaction_index DESC, authorization_index DESC
LIMIT $8`
