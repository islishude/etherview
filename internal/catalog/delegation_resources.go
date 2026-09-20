package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	defer dbaccess.Rollback(ctx, tx)
	page := TransactionAuthorizationPage{Identity: resolution.identity, Items: []EIP7702Authorization{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := func() ([]dbgen.CatalogTransactionAuthorizationsRow, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return nil, err
			}
			if resolution.limit+1 < -2147483648 || resolution.limit+1 > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			if resolution.offset < -2147483648 || resolution.offset > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			return dbgen.New(tx).CatalogTransactionAuthorizations(ctx, dbgen.CatalogTransactionAuthorizationsParams{ChainID: queryValue0, BlockHash: resolution.blockHash, TransactionHash: resolution.txHash, Limit: int32(resolution.limit + 1), Offset: int32(resolution.offset)})
		}()
		if queryErr != nil {
			return TransactionAuthorizationPage{}, fmt.Errorf("list transaction authorizations: %w", queryErr)
		}

		for _, storedRow := range rows {
			var item EIP7702Authorization
			var index int64
			var delegate, authority, r, s []byte
			var skipReason pgtype.Text
			{
				index = storedRow.AuthorizationIndex
				item.ChainID = storedRow.AuthorizationChainID
				item.Nonce = storedRow.AuthorizationNonce
				delegate = storedRow.DelegateAddress
				item.YParity = int(storedRow.YParity)
				r = storedRow.R
				s = storedRow.S
				authority = storedRow.Authority
				item.SignatureStatus = storedRow.SignatureStatus
				item.ApplicationStatus = storedRow.ApplicationStatus
				var queryValue10 pgtype.Text
				if storedRow.SkipReason != nil {
					queryValue10 = pgtype.Text{String: *storedRow.SkipReason, Valid: true}
				}
				skipReason = queryValue10
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

		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("authorizations", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionAuthorizationPage{}, err
			}
		}
	}
	if err := commitRead(ctx, tx); err != nil {
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
	defer dbaccess.Rollback(ctx, tx)
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
	blockBoundary, transactionBoundary, authorizationBoundary :=
		boundary.BlockNumber, boundary.TransactionIndex, boundary.AuthorizationIndex
	if !hasBoundary {
		// PostgreSQL casts all tuple parameters even when the preceding NOT
		// $4 branch makes the comparison logically unnecessary.
		blockBoundary, transactionBoundary, authorizationBoundary = "0", "0", "0"
	}
	rows, err := func() ([]dbgen.CatalogAddressDelegationsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(blockBoundary); err != nil {
			return nil, err
		}
		var queryValue3 pgtype.Numeric
		if err := queryValue3.Scan(transactionBoundary); err != nil {
			return nil, err
		}
		var queryValue4 pgtype.Numeric
		if err := queryValue4.Scan(authorizationBoundary); err != nil {
			return nil, err
		}
		if limit+1 < -2147483648 || limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogAddressDelegations(ctx, dbgen.CatalogAddressDelegationsParams{ChainID: queryValue0, Authority: authority, MaxBlockNumber: queryValue1, HasCursor: hasBoundary, CursorBlockNumber: queryValue2, CursorTransactionIndex: queryValue3, CursorAuthorizationIndex: queryValue4, Limit: int32(limit + 1)})
	}()
	if err != nil {
		return DelegationHistoryPage{}, fmt.Errorf("list address delegations: %w", err)
	}

	page := DelegationHistoryPage{Items: []DelegationHistoryItem{}, Snapshot: snapshot}
	for _, storedRow := range rows {
		var item DelegationHistoryItem
		var blockHash, transactionHash, delegate, previous []byte
		{
			item.BlockNumber = storedRow.BlockNumber
			blockHash = storedRow.BlockHash
			transactionHash = storedRow.TransactionHash
			item.TransactionIndex = storedRow.TransactionIndex
			item.AuthorizationIndex = storedRow.AuthorizationIndex
			delegate = storedRow.DelegateAddress
			previous = storedRow.PreviousDelegate
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
	if err := commitRead(ctx, tx); err != nil {
		return DelegationHistoryPage{}, err
	}
	return page, nil
}
