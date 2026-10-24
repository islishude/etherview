// Command pushreceiver is a test-only encrypted Web Push receiver. It runs on
// an isolated Docker network; production never disables its public-IP policy.
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	configBytes, e := os.ReadFile("/fixture/browser.json")
	if e != nil {
		log.Fatal("missing fixture")
	}
	var config struct{ Private, Auth string }
	if json.Unmarshal(configBytes, &config) != nil {
		log.Fatal("invalid fixture")
	}
	raw, e := base64.RawURLEncoding.DecodeString(config.Private)
	if e != nil {
		log.Fatal("invalid key")
	}
	private, e := ecdh.P256().NewPrivateKey(raw)
	if e != nil {
		log.Fatal("invalid key")
	}
	auth, e := base64.RawURLEncoding.DecodeString(config.Auth)
	if e != nil {
		log.Fatal("invalid auth")
	}
	var mu sync.Mutex
	messages := []json.RawMessage{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messages)
	})
	mux.HandleFunc("POST /push", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("TTL") != "0" || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Authorization") == "" {
			http.Error(w, "invalid protocol", 400)
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, 8193))
		if e != nil || len(body) < 86 || len(body) > 8192 || int(body[20]) != 65 {
			http.Error(w, "invalid body", 400)
			return
		}
		peer, e := ecdh.P256().NewPublicKey(body[21:86])
		if e != nil {
			http.Error(w, "invalid key", 400)
			return
		}
		shared, e := private.ECDH(peer)
		if e != nil {
			http.Error(w, "invalid key", 400)
			return
		}
		info := append([]byte("WebPush: info\x00"), private.PublicKey().Bytes()...)
		info = append(info, peer.Bytes()...)
		ikm, _ := hkdf.Key(sha256.New, shared, auth, string(info), 32)
		key, _ := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: aes128gcm\x00", 16)
		nonce, _ := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: nonce\x00", 12)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plaintext, e := gcm.Open(nil, nonce, body[86:], nil)
		if e != nil {
			http.Error(w, "invalid ciphertext", 400)
			return
		}
		plaintext = bytes.TrimRight(plaintext, "\x00")
		if len(plaintext) == 0 || plaintext[len(plaintext)-1] != 2 {
			http.Error(w, "invalid record", 400)
			return
		}
		plaintext = plaintext[:len(plaintext)-1]
		if !json.Valid(plaintext) {
			http.Error(w, "invalid JSON", 400)
			return
		}
		mu.Lock()
		if len(messages) < 1000 {
			messages = append(messages, append(json.RawMessage(nil), plaintext...))
		}
		mu.Unlock()
		w.WriteHeader(201)
	})
	server := &http.Server{Addr: ":443", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(server.ListenAndServeTLS("/fixture/tls.crt", "/fixture/tls.key"))
}
