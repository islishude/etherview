//go:build integration

package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkVerificationCompactAdmissionAndStatus(b *testing.B) {
	for _, size := range []int{1024, 3 << 19, 3 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			db := bytecodeDBPostgres(b)
			cfg := db.Config()
			name := fmt.Sprintf("verification-capacity-%d", size)
			cfg.ConnConfig.RuntimeParams["application_name"] = name
			workload, err := pgxpool.NewWithConfig(b.Context(), cfg)
			if err != nil {
				b.Fatal(err)
			}
			defer workload.Close()
			var peakWait atomic.Int64
			stop, stopped := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(stopped)
				ticker := time.NewTicker(2 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						var micros int64
						if err := db.QueryRow(b.Context(), `SELECT COALESCE(max(extract(epoch FROM (clock_timestamp()-l.waitstart))*1000000),0)::bigint FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.application_name=$1 AND NOT l.granted`, name).Scan(&micros); err == nil {
							for previous := peakWait.Load(); micros > previous; previous = peakWait.Load() {
								if peakWait.CompareAndSwap(previous, micros) {
									break
								}
							}
						}
					}
				}
			}()
			defer func() { close(stop); <-stopped }()
			repo, _ := NewPostgresRepository(workload, RepositoryOptions{})
			service, _ := NewService(repo, 5<<20)
			durations := make(chan time.Duration, 25)
			failures := make(chan error, 25)
			var wg sync.WaitGroup
			for replica := range 5 {
				wg.Go(func() {
					for index := range 5 {
						request := capacitySubmission(replica*5 + index)
						request.StandardJSON, _ = json.Marshal(map[string]any{"language": "Solidity", "sources": map[string]any{"A.sol": map[string]string{"content": fmt.Sprintf("contract A {} /* %d %s */", replica*5+index, strings.Repeat("a", size))}}, "settings": map[string]any{}})
						started := time.Now()
						_, _, err := service.SubmitV2(b.Context(), request)
						durations <- time.Since(started)
						failures <- err
					}
				})
			}
			wg.Wait()
			close(durations)
			close(failures)
			for err := range failures {
				if err != nil {
					b.Fatal(err)
				}
			}
			samples := make([]time.Duration, 0, 25)
			for duration := range durations {
				samples = append(samples, duration)
			}
			slices.Sort(samples)
			renewals := make([]time.Duration, 0, 25)
			var id string
			for range 25 {
				lease, found, err := repo.Claim(b.Context(), "capacity-benchmark", time.Minute)
				if err != nil || !found {
					b.Fatalf("claim %v %v", found, err)
				}
				id = lease.Job.ID
				started := time.Now()
				if err = repo.Renew(b.Context(), lease, time.Minute); err != nil {
					b.Fatal(err)
				}
				renewals = append(renewals, time.Since(started))
			}
			slices.Sort(renewals)
			var queuedBytes int64
			if err := db.QueryRow(b.Context(), `SELECT bytes FROM verification_budget`).Scan(&queuedBytes); err != nil {
				b.Fatal(err)
			}
			var plan json.RawMessage
			if err := db.QueryRow(b.Context(), `EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) SELECT id,kind,status,summary_outcome,error_code,created_at,updated_at,summary_chain_id,summary_address FROM verification_jobs WHERE id=$1::uuid`, id).Scan(&plan); err != nil {
				b.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, plan); err != nil {
				b.Fatal(err)
			}
			b.Logf("source-bytes=%d status EXPLAIN=%s", size, compact.String())
			b.ResetTimer()
			for b.Loop() {
				if _, found, err := repo.JobSummary(b.Context(), id); err != nil || !found {
					b.Fatalf("summary %v %v", found, err)
				}
			}
			b.ReportMetric(float64(samples[23].Microseconds()), "admit-p95-us")
			b.ReportMetric(float64(renewals[23].Microseconds()), "renew-p95-us")
			b.ReportMetric(float64(queuedBytes), "queued-bytes")
			b.ReportMetric(float64(peakWait.Load()), "sampled-lockwait-max-us")

		})
	}
}
