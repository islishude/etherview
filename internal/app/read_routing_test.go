package app

import (
	"context"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/httpapi"
)

type transactionRoutingReader struct{ hash string }

func (reader transactionRoutingReader) Transaction(context.Context, string) (gen.Transaction, error) {
	return gen.Transaction{Hash: reader.hash}, nil
}

func TestTransactionDetailRoutingUsesWriterOnlyWithMempool(t *testing.T) {
	t.Parallel()
	ordinary := transactionRoutingReader{hash: "ordinary"}
	authoritative := transactionRoutingReader{hash: "writer"}
	for _, test := range []struct {
		enabled bool
		want    string
	}{{enabled: false, want: "ordinary"}, {enabled: true, want: "writer"}} {
		transaction, err := selectTransactionDetailReader(test.enabled, ordinary, authoritative).Transaction(t.Context(), "ignored")
		if err != nil || transaction.Hash != test.want {
			t.Fatalf("enabled=%v transaction=%+v error=%v", test.enabled, transaction, err)
		}
	}
}

type routingTestReader struct {
	httpapi.Reader
	statusCalls int
	searchCalls int
}

func (reader *routingTestReader) Status(context.Context) (httpapi.StatusSnapshot, error) {
	reader.statusCalls++
	return httpapi.StatusSnapshot{}, nil
}

func (reader *routingTestReader) Search(
	context.Context,
	string,
	string,
	int,
) ([]gen.SearchResult, string, error) {
	reader.searchCalls++
	return nil, "", nil
}

func TestSearchRoutingReaderKeepsNameSearchOnWriter(t *testing.T) {
	t.Parallel()
	replica := &routingTestReader{}
	writer := &routingTestReader{}
	reader := searchRoutingReader{Reader: replica, search: writer}
	if _, err := reader.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Search(context.Background(), "alice.eth", "", 10); err != nil {
		t.Fatal(err)
	}
	if replica.statusCalls != 1 || replica.searchCalls != 0 || writer.searchCalls != 1 {
		t.Fatalf(
			"calls replica status=%d search=%d writer search=%d",
			replica.statusCalls, replica.searchCalls, writer.searchCalls,
		)
	}
}

type routingTestBackend struct {
	requests []etherscan.Request
}

func (backend *routingTestBackend) Execute(
	_ context.Context,
	request etherscan.Request,
) (any, error) {
	backend.requests = append(backend.requests, request)
	return "ok", nil
}

func TestReplicaAwareEtherscanBackendRoutesAuthoritativeActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		request     etherscan.Request
		writerCalls int
		readerCalls int
	}{
		{
			name:        "ordinary compatibility read",
			request:     etherscan.Request{Module: "account", Action: "txlist"},
			readerCalls: 1,
		},
		{
			name:        "contract source read",
			request:     etherscan.Request{Module: "contract", Action: "getsourcecode"},
			readerCalls: 1,
		},
		{
			name:        "verification submission",
			request:     etherscan.Request{Module: "contract", Action: "verifysourcecode"},
			writerCalls: 1,
		},
		{
			name:        "price observation remains authoritative",
			request:     etherscan.Request{Module: "stats", Action: "ethprice"},
			writerCalls: 1,
		},
		{
			name:        "verification status remains authoritative",
			request:     etherscan.Request{Module: "contract", Action: "checkverifystatus"},
			writerCalls: 1,
		},
		{
			name:        "proxy verification submission",
			request:     etherscan.Request{Module: "contract", Action: "verifyproxycontract"},
			writerCalls: 1,
		},
		{
			name:        "proxy verification status remains authoritative",
			request:     etherscan.Request{Module: "contract", Action: "checkproxyverification"},
			writerCalls: 1,
		},
		{
			name:        "future contract action fails closed to writer",
			request:     etherscan.Request{Module: "contract", Action: "future-action"},
			writerCalls: 1,
		},
		{
			name:        "future non-contract action also fails closed to writer",
			request:     etherscan.Request{Module: "future", Action: "mutation"},
			writerCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := &routingTestBackend{}
			writer := &routingTestBackend{}
			backend := replicaAwareEtherscanBackend{reader: reader, authoritative: writer}
			if _, err := backend.Execute(context.Background(), test.request); err != nil {
				t.Fatal(err)
			}
			if len(reader.requests) != test.readerCalls || len(writer.requests) != test.writerCalls {
				t.Fatalf("reader calls=%d writer calls=%d", len(reader.requests), len(writer.requests))
			}
		})
	}
}
