package etherscan

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/publicquery"
)

type blobReaderStub struct {
	publicquery.ConsensusReader
	data  []byte
	err   error
	calls int
	hash  string
	index uint64
}

func (r *blobReaderStub) BlobContent(_ context.Context, hash string, index uint64) ([]byte, error) {
	r.calls++
	r.hash, r.index = hash, index
	return r.data, r.err
}
func TestBlobBackendExactContent(t *testing.T) {
	r := &blobReaderStub{data: bytes.Repeat([]byte{7}, 131072)}
	b := &PostgresBackend{blobReader: r}
	values := url.Values{"blockhash": {testHash(7)}, "index": {"42"}}
	got, err := b.blobContent(t.Context(), values)
	if err != nil || got != "0x"+strings.Repeat("07", 131072) || r.hash != testHash(7) || r.index != 42 {
		t.Fatalf("incorrect blob result: %v", err)
	}
	for _, index := range []string{"", "01", "-1", "18446744073709551616"} {
		values.Set("index", index)
		if _, err := b.blobContent(t.Context(), values); err == nil {
			t.Fatal("accepted invalid index")
		}
	}
	if r.calls != 1 {
		t.Fatal("invalid requests read archive")
	}
	values.Set("index", "0")
	values.Set("blockhash", "latest")
	if _, err := b.blobContent(t.Context(), values); err == nil {
		t.Fatal("accepted latest")
	}
	values.Set("blockhash", testHash(7))
	r.data = r.data[:100]
	if _, err := b.blobContent(t.Context(), values); err != ErrBlobUnavailable {
		t.Fatal("accepted partial blob")
	}
}
func TestBlobBillingAndAuthentication(t *testing.T) {
	for _, mode := range []string{"success", "failure", "insufficient", "operator", "disabled", "cookie", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: bytes.Repeat([]byte{5}, 32)}
			key, err := manager.CreateForUser(t.Context(), "00000000-0000-4000-8000-000000000001", "blob", []auth.Scope{auth.ScopeRead}, 100, 100, 5)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "operator" {
				key, err = manager.CreateScoped(t.Context(), "operator", 100, 100, []auth.Scope{auth.ScopeRead})
				if err != nil {
					t.Fatal(err)
				}
			}
			ledger := &blobUsageLedger{}
			if mode == "insufficient" {
				ledger.fail = true
			}
			usage, err := billing.NewUsageDispatcher(billing.UsageDispatcherOptions{Ledger: ledger, Prices: map[string]string{"etherview.blob.getblob": "5"}, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			backend := &fakeBackend{result: "0x" + strings.Repeat("00", 131072)}
			if mode == "failure" {
				backend.err = ErrBlobUnavailable
			}
			if mode == "disabled" {
				usage = nil
			}
			h := manager.Middleware(false, Handler{ChainID: 1, Backend: backend, Usage: usage, PublicOrigin: "https://explorer.example"})
			index := "0"
			if mode == "invalid" {
				index = "01"
			}
			req := httptest.NewRequest("GET", "/v2/api?chainid=1&module=blob&action=getblob&blockhash="+testHash(7)+"&index="+index, nil)
			if mode == "cookie" {
				req.AddCookie(&http.Cookie{Name: "etherview_session", Value: "session"})
			} else {
				req.Header.Set("X-API-Key", key.Token)
			}
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			switch mode {
			case "success":
				if len(ledger.commits) != 1 || len(ledger.reserves) != 1 || ledger.reserves[0].AmountAtomic != "5" || !strings.Contains(out.Body.String(), `"status":"1"`) {
					t.Fatal("successful content was not charged")
				}
			case "failure":
				if len(ledger.commits) != 0 || len(ledger.releases) != 1 {
					t.Fatal("failed read was charged")
				}
			case "operator", "disabled":
				if len(ledger.reserves) != 0 || backend.calls != 1 {
					t.Fatal("free exception failed")
				}
			case "cookie", "invalid", "insufficient":
				if backend.calls != 0 || len(ledger.commits) != 0 {
					t.Fatal("rejected request accessed archive")
				}
			}
		})
	}
}

type blobUsageLedger struct {
	fakeUsageLedger
	fail bool
}

func (l *blobUsageLedger) ReserveUsage(ctx context.Context, in billing.ReserveUsageInput) (billing.UsageReservation, error) {
	if l.fail {
		return billing.UsageReservation{}, billing.ErrInsufficientCredit
	}
	return l.fakeUsageLedger.ReserveUsage(ctx, in)
}
