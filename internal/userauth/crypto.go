package userauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const nonceAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var (
	sessionDigestDomain = []byte("etherview:user-session-token:v1\x00")
	csrfValueDomain     = []byte("etherview:user-session-csrf:v1\x00")
	csrfDigestDomain    = []byte("etherview:user-session-csrf-digest:v1\x00")
)

func randomOpaqueValue(random io.Reader) ([opaqueValueBytes]byte, string, error) {
	var value [opaqueValueBytes]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return value, "", fmt.Errorf("generate opaque authentication value: %w", err)
	}
	return value, base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func decodeOpaqueValue(encoded string) ([opaqueValueBytes]byte, error) {
	var value [opaqueValueBytes]byte
	if len(encoded) != opaqueValueChars {
		return value, ErrInvalidInput
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != len(value) ||
		base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return value, ErrInvalidInput
	}
	copy(value[:], decoded)
	return value, nil
}

func generateNonce(random io.Reader, length int) (string, error) {
	if length < 8 || length > 64 {
		return "", errors.New("SIWE nonce length must be between 8 and 64")
	}
	nonce := make([]byte, length)
	buffer := make([]byte, length)
	filled := 0
	// Rejection sampling avoids the modulo bias introduced by byte % 62.
	const acceptanceLimit = byte(248)
	for filled < len(nonce) {
		if _, err := io.ReadFull(random, buffer); err != nil {
			return "", fmt.Errorf("generate SIWE nonce: %w", err)
		}
		for _, value := range buffer {
			if value >= acceptanceLimit {
				continue
			}
			nonce[filled] = nonceAlphabet[int(value)%len(nonceAlphabet)]
			filled++
			if filled == len(nonce) {
				break
			}
		}
	}
	return string(nonce), nil
}

func keyedDigest(pepper, domain, value []byte) [sha256.Size]byte {
	hasher := hmac.New(sha256.New, pepper)
	_, _ = hasher.Write(domain)
	_, _ = hasher.Write(value)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func sessionDigest(pepper []byte, token [opaqueValueBytes]byte) [sha256.Size]byte {
	return keyedDigest(pepper, sessionDigestDomain, token[:])
}

func deriveCSRFValue(pepper []byte, token [opaqueValueBytes]byte) [sha256.Size]byte {
	return keyedDigest(pepper, csrfValueDomain, token[:])
}

func csrfDigest(pepper []byte, csrf [opaqueValueBytes]byte) [sha256.Size]byte {
	return keyedDigest(pepper, csrfDigestDomain, csrf[:])
}

func constantTimeEqual(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}
