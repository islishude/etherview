package facilitator

import (
	"bytes"
	"context"
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Rebroadcast resubmits only the exact journaled bytes. It never signs or allocates a nonce.
// The normal service must be stopped so this process can acquire the singleton lock.
func (s *Service) Rebroadcast(ctx context.Context, id string) error {
	row, err := s.queries.FacilitatorGet(ctx, id)
	if err != nil || row.State != "signed" || len(row.RawTransaction) == 0 {
		return errors.New("recovery_unavailable")
	}
	ch := s.chains[row.Network]
	if ch == nil {
		return errors.New("recovery_network_unavailable")
	}
	select {
	case ch.gate <- struct{}{}:
		defer func() { <-ch.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var tx types.Transaction
	if tx.UnmarshalBinary(row.RawTransaction) != nil || !bytes.Equal(tx.Hash().Bytes(), row.TransactionHash) {
		return errors.New("invalid_journal")
	}
	chainID, ok := networkID(row.Network)
	if !ok || tx.ChainId().Cmp(chainID) != 0 {
		return errors.New("invalid_journal")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(chainID), &tx)
	if err != nil || sender != common.BytesToAddress(row.SignerAddress) || sender != ch.adapter.signer.Address() {
		return errors.New("invalid_journal")
	}
	if err = s.guard(ctx); err != nil {
		return err
	}
	if err = ch.adapter.client.SendTransaction(ctx, &tx); err != nil {
		return errors.New("recovery_pending")
	}
	_, err = s.queries.FacilitatorConfirmBroadcast(ctx, id, row.TransactionHash)
	if err != nil {
		return errors.New("recovery_pending")
	}
	return nil
}
