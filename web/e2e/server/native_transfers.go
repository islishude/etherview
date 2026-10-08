package main

import "net/http"

func registerNativeTransferHandlers(mux *http.ServeMux) {
	for _, pattern := range []string{"GET /api/v1/transactions/{hash}/native-transfers", "GET /api/v1/addresses/{address}/native-transfers"} {
		mux.HandleFunc(pattern, func(response http.ResponseWriter, _ *http.Request) {
			writeJSON(response, map[string]any{"applicable": true, "data": []any{map[string]any{"block_number": "2", "block_hash": secondHash, "transaction_hash": testTransactionHash, "transaction_index": "0", "log_index": "0", "from": testAddress, "to": testEOA, "amount": "9007199254740993", "timestamp": "2026-01-01T00:00:00Z"}}, "meta": map[string]any{"chain_id": "1", "request_id": "native-e2e", "coverage_start": "2", "coverage_end": "2"}})
		})
	}
}
