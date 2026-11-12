package facilitator

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"
)

func (s *Service) guard(ctx context.Context) error {
	if !s.active.Load() {
		return errors.New("facilitator_inactive")
	}
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ping, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.lock.Ping(ping); err != nil {
		s.loseLock()
		return errors.New("facilitator_lock_lost")
	}
	return nil
}
func (s *Service) monitor(ctx context.Context, ch *chainRuntime) {
	defer s.workers.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, time.Second)
			id, err := ch.adapter.GetChainID(check)
			expected, _ := networkID(ch.adapter.config.Network)
			healthy := err == nil && id != nil && id.Cmp(expected) == 0 && s.guard(check) == nil
			ch.healthy.Store(healthy)
			if healthy {
				balance, e := ch.adapter.client.BalanceAt(check, ch.adapter.signer.Address(), nil)
				if e == nil {
					fee, _ := new(big.Int).SetString(ch.adapter.config.MaxFeePerGas, 10)
					ch.insufficientFunds.Store(balance.Cmp(fee.Mul(fee, new(big.Int).SetUint64(ch.adapter.config.MaxGas))) < 0)
				}
			}
			cancel()
			if !healthy {
				continue
			}
			select {
			case ch.gate <- struct{}{}:
				work, done := context.WithTimeout(ctx, 10*time.Second)
				rows, e := s.queries.FacilitatorPending(work, ch.adapter.config.Network)
				if e == nil {
					for _, row := range rows {
						// Rotate each attempted row before external work. A slow or
						// permanently unresolved authorization must not pin the first
						// page forever or starve rows beyond this round's time budget.
						count, err := s.queries.FacilitatorTouchRecovery(work, row.AuthorizationID)
						if err != nil {
							break
						}
						if count == 0 {
							// Re-read after a conditional miss; never reconcile an old
							// snapshot that has already left the pending set.
							if _, err := s.queries.FacilitatorGet(work, row.AuthorizationID); err != nil {
								break
							}
							continue
						}
						var request paymentRequest
						if json.Unmarshal(row.RequestBody, &request) == nil {
							_, _ = s.reconcile(work, ch, row, request)
						}
					}
				}
				done()
				<-ch.gate
			default:
			}
		}
	}
}
