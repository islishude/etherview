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
		s.active.Store(false)
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
				work, done := context.WithTimeout(ctx, time.Second)
				rows, e := s.queries.FacilitatorPending(work, ch.adapter.config.Network)
				if e == nil {
					for _, row := range rows {
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
