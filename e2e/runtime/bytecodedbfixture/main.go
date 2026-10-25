// Command bytecodedbfixture serves bounded search responses on a test-owned TLS network.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	raw, err := os.ReadFile("/fixture/sources.json")
	if err != nil {
		log.Fatal("missing sources")
	}
	var sources map[string]json.RawMessage
	if json.Unmarshal(raw, &sources) != nil {
		log.Fatal("invalid sources")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/bytecodes/sources:search", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Bytecode     string `json:"bytecode"`
			BytecodeType string `json:"bytecodeType"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&request) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if source, ok := sources[request.BytecodeType+":"+request.Bytecode]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"sources": []json.RawMessage{source}})
			return
		}
		_, _ = w.Write([]byte(`{"sources":[]}`))
	})
	server := http.Server{Addr: ":443", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServeTLS("/fixture/tls.crt", "/fixture/tls.key"))
}
