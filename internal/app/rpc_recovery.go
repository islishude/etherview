package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func transientRPCProbe(err error) bool {
	return errors.Is(err, ethrpc.ErrTransport) || errors.Is(err, context.DeadlineExceeded)
}

type rpcRecovery struct {
	pool       *ethrpc.Pool
	pending    []ethrpc.Endpoint
	expected   ethrpc.ChainIdentity
	startBlock uint64
	timeout    time.Duration
	logger     *slog.Logger
}

func (*rpcRecovery) Name() string { return "rpc-recovery" }
func (r *rpcRecovery) Run(ctx context.Context) error {
	delay := 5 * time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err := r.runOnce(ctx); err != nil {
			return err
		}
		if len(r.pending) == 0 {
			<-ctx.Done()
			return ctx.Err()
		}
		delay = min(2*delay, time.Minute)
	}
}
func (r *rpcRecovery) runOnce(ctx context.Context) error {
	remaining := r.pending[:0]
	for _, endpoint := range r.pending {
		timeout := r.timeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		report, err := ethrpc.ProbeEndpoint(probeCtx, &endpoint, ethrpc.ProbeOptions{Expected: &r.expected, StartBlock: r.startBlock})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !transientRPCProbe(err) {
				return fmt.Errorf("revalidate RPC endpoint %q: %w", endpoint.Name, err)
			}
			remaining = append(remaining, endpoint)
			continue
		}
		endpoint.Capabilities = report
		if report.Status(ethrpc.CapabilityHistoricalData) == ethrpc.AvailabilityUnavailable {
			delete(endpoint.Purposes, ethrpc.PurposeHistory)
		}
		if report.Status(ethrpc.CapabilityTxPool) == ethrpc.AvailabilityUnavailable {
			delete(endpoint.Purposes, ethrpc.PurposeMempool)
		}
		if hasEnabledRPCPurpose(endpoint.Purposes) {
			if err := r.pool.AddVerified(endpoint); err != nil {
				return err
			}
		}
		r.logger.InfoContext(ctx, "RPC endpoint revalidated", "rpc", endpoint.Name)
	}
	r.pending = remaining
	return nil
}
