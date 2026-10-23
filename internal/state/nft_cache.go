package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/catalog"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5/pgtype"
)

type nftCandidateKey struct {
	standard catalogStandard
	address  common.Address
	tokenID  [32]byte
}

func (candidate parsedNFTCandidate) key() nftCandidateKey {
	key := nftCandidateKey{standard: candidate.standard, address: candidate.address}
	candidate.tokenID.FillBytes(key.tokenID[:])
	return key
}

type nftCacheRequest struct {
	addresses [][]byte
	tokenIDs  []pgtype.Numeric
}

// cachedNFTBalances performs at most one read per standard, closing both result
// sets before the caller acquires an RPC endpoint. Keys include the standard,
// contract and token ID; database row order never determines result identity.
func (reconciler *TokenReconciler) cachedNFTBalances(
	ctx context.Context, chainID string, owner common.Address,
	candidates []parsedNFTCandidate, reference CanonicalRef,
) (map[int]catalog.NFTBalanceObservation, error) {
	var chain, block pgtype.Numeric
	if err := chain.Scan(chainID); err != nil {
		return nil, err
	}
	if err := block.Scan(strconv.FormatUint(reference.Number, 10)); err != nil {
		return nil, err
	}
	requests := make(map[catalogStandard]*nftCacheRequest, 2)
	indices := make(map[nftCandidateKey]int, len(candidates))
	for index, candidate := range candidates {
		indices[candidate.key()] = index
		request := requests[candidate.standard]
		if request == nil {
			request = &nftCacheRequest{}
			requests[candidate.standard] = request
		}
		var id pgtype.Numeric
		if err := id.Scan(candidate.tokenID.String()); err != nil {
			return nil, err
		}
		request.addresses = append(request.addresses, candidate.address.Bytes())
		request.tokenIDs = append(request.tokenIDs, id)
	}
	results := make(map[int]catalog.NFTBalanceObservation, len(candidates))
	store := func(standard catalogStandard, address []byte, tokenID string, balance catalog.NFTBalanceObservation) error {
		id, err := parseUint256(tokenID)
		if err != nil || len(address) != common.AddressLength {
			return errors.New("invalid cached NFT identity")
		}
		candidate := parsedNFTCandidate{standard: standard, address: common.BytesToAddress(address), tokenID: id}
		index, found := indices[candidate.key()]
		if !found {
			return errors.New("unexpected cached NFT identity")
		}
		if _, duplicate := results[index]; duplicate {
			return errors.New("duplicate cached NFT identity")
		}
		results[index] = balance
		return nil
	}
	queries := dbgen.New(reconciler.db)
	if request := requests[standardERC721]; request != nil {
		rows, err := queries.StateERC721OwnerObservations(ctx, dbgen.StateERC721OwnerObservationsParams{
			ChainID: chain, BlockNumber: block, BlockHash: reference.Hash.Bytes(), TokenAddresses: request.addresses, TokenIds: request.tokenIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("read exact ERC-721 owner observations: %w", err)
		}
		for _, row := range rows {
			observation, err := decodeOwnerObservation(row.State, row.OwnerAddress, row.Confidence)
			if err != nil {
				return nil, err
			}
			balance := "0"
			if observation.Exists {
				observedOwner, err := ethrpc.ParseAddress(observation.Owner)
				if err != nil {
					return nil, errors.New("invalid cached ERC-721 owner")
				}
				if observedOwner == owner {
					balance = "1"
				}
			}
			if err := store(standardERC721, row.TokenAddress, row.ObservationTokenID, exactNFTBalance(balance)); err != nil {
				return nil, err
			}
		}
	}
	if request := requests[standardERC1155]; request != nil {
		rows, err := queries.StateERC1155BalanceObservations(ctx, dbgen.StateERC1155BalanceObservationsParams{
			ChainID: chain, OwnerAddress: owner.Bytes(), BlockNumber: block, BlockHash: reference.Hash.Bytes(), TokenAddresses: request.addresses, TokenIds: request.tokenIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("read exact ERC-1155 balance observations: %w", err)
		}
		for _, row := range rows {
			if _, err := parseUint256(row.ObservationBalance); err != nil || row.Confidence != catalog.NFTStateConfidenceRPCExact {
				return nil, errors.New("invalid ERC-1155 balance observation")
			}
			if err := store(standardERC1155, row.TokenAddress, row.ObservationTokenID, exactNFTBalance(row.ObservationBalance)); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}
