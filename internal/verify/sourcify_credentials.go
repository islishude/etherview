package verify

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
)

const maxSourcifyAPIKeyBytes = 256

var errSourcifyCredential = errors.New("sourcify credential is unavailable or invalid")

// SourcifyAPIKey is a transient, redacted transport value. SubmissionV2 never
// serializes it; only authenticated ciphertext may cross the repository boundary.
type SourcifyAPIKey string

func (SourcifyAPIKey) String() string               { return "[REDACTED]" }
func (SourcifyAPIKey) GoString() string             { return "[REDACTED]" }
func (SourcifyAPIKey) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
func (SourcifyAPIKey) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// SourcifyCredentials derives independent encryption and deduplication keys
// from the API role's secret. No plaintext credential is kept by this codec.
type SourcifyCredentials struct {
	aead           cipher.AEAD
	fingerprintKey []byte
}

type sourcifyCredential struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Ciphertext  []byte `json:"ciphertext"`
}

type sourcifyEtherscanRequest struct {
	ChainID    string              `json:"chain_id"`
	Address    string              `json:"address"`
	Credential *sourcifyCredential `json:"credential,omitempty"`
}

func NewSourcifyCredentials(pepper []byte) (*SourcifyCredentials, error) {
	if len(pepper) < 32 {
		return nil, errSourcifyCredential
	}
	encryptionKey, err := hkdf.Key(sha256.New, pepper, nil, "etherview:sourcify:credential-encryption:v1", 32)
	if err != nil {
		return nil, errSourcifyCredential
	}
	defer clear(encryptionKey)
	fingerprintKey, err := hkdf.Key(sha256.New, pepper, nil, "etherview:sourcify:credential-fingerprint:v1", 32)
	if err != nil {
		return nil, errSourcifyCredential
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, errSourcifyCredential
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errSourcifyCredential
	}
	return &SourcifyCredentials{aead: aead, fingerprintKey: fingerprintKey}, nil
}

func ValidateSourcifyAPIKey(key string) error {
	if len(key) == 0 || len(key) > maxSourcifyAPIKeyBytes {
		return errSourcifyCredential
	}
	for _, char := range []byte(key) {
		if char < '!' || char > '~' {
			return errSourcifyCredential
		}
	}
	return nil
}

func decodeSourcifyEtherscanRequest(raw json.RawMessage) (sourcifyEtherscanRequest, error) {
	var request sourcifyEtherscanRequest
	if len(raw) > 4096 || validateUniqueJSON(raw) != nil {
		return request, errSourcifyCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !canonicalChainID(request.ChainID) || !fixedHex(request.Address, 20) {
		return sourcifyEtherscanRequest{}, errSourcifyCredential
	}
	if credential := request.Credential; credential != nil {
		fingerprint, err := hex.DecodeString(credential.Fingerprint)
		if credential.Version != 1 || err != nil || len(fingerprint) != sha256.Size ||
			len(credential.Ciphertext) < 29 || len(credential.Ciphertext) > maxSourcifyAPIKeyBytes+28 {
			return sourcifyEtherscanRequest{}, errSourcifyCredential
		}
	}
	return request, nil
}

func sourcifyCredentialContext(request sourcifyEtherscanRequest) []byte {
	return []byte("etherview:sourcify:etherscan:v1\x00" + request.ChainID + "\x00" + strings.ToLower(request.Address))
}

func (credentials *SourcifyCredentials) fingerprint(context []byte, key string) string {
	mac := hmac.New(sha256.New, credentials.fingerprintKey)
	_, _ = mac.Write(context)
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

func (credentials *SourcifyCredentials) seal(raw json.RawMessage, key SourcifyAPIKey) (json.RawMessage, error) {
	request, err := decodeSourcifyEtherscanRequest(raw)
	if err != nil || request.Credential != nil || credentials == nil || ValidateSourcifyAPIKey(string(key)) != nil {
		return nil, errSourcifyCredential
	}
	context := sourcifyCredentialContext(request)
	fingerprint := credentials.fingerprint(context, string(key))
	aad := append(context, []byte("\x00"+fingerprint)...)
	plaintext := []byte(key)
	defer clear(plaintext)
	request.Credential = &sourcifyCredential{Version: 1, Fingerprint: fingerprint, Ciphertext: credentials.aead.Seal(nil, nil, plaintext, aad)}
	return json.Marshal(request)
}

func (credentials *SourcifyCredentials) open(request sourcifyEtherscanRequest) (string, error) {
	if request.Credential == nil {
		return "", nil
	}
	if credentials == nil {
		return "", errSourcifyCredential
	}
	context := sourcifyCredentialContext(request)
	aad := append(context, []byte("\x00"+request.Credential.Fingerprint)...)
	plaintext, err := credentials.aead.Open(nil, nil, request.Credential.Ciphertext, aad)
	if err != nil {
		return "", errSourcifyCredential
	}
	defer clear(plaintext)
	key := string(plaintext)
	if ValidateSourcifyAPIKey(key) != nil || !hmac.Equal([]byte(credentials.fingerprint(context, key)), []byte(request.Credential.Fingerprint)) {
		return "", errSourcifyCredential
	}
	return key, nil
}

// The keyed fingerprint preserves active-job deduplication while a fresh random
// nonce protects every ciphertext. Plaintext or unkeyed credential hashes never
// enter a stored payload or a request digest.
func sourcifyDigestPayload(request SubmissionV2, encoded []byte) ([]byte, error) {
	if request.Kind != JobSourcifyFromEtherscan {
		return encoded, nil
	}
	etherscan, err := decodeSourcifyEtherscanRequest(request.SourcifyRequest)
	if err != nil {
		return nil, err
	}
	if etherscan.Credential == nil {
		return encoded, nil
	}
	etherscan.Credential.Ciphertext = nil
	request.SourcifyRequest, err = json.Marshal(etherscan)
	if err != nil {
		return nil, errSourcifyCredential
	}
	return json.Marshal(request)
}
