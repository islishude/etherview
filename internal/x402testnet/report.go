package x402testnet

import "time"

const SDKVersion = "x402/go/v2 v2.19.0"

// Report is the only successful command output. It deliberately omits every
// URL, credential, authorization header, facilitator body, and database error.
type Report struct {
	SDKVersion         string    `json:"sdk_version"`
	HarnessRevision    string    `json:"harness_revision"`
	Operation          string    `json:"operation"`
	Network            string    `json:"network"`
	Payer              string    `json:"payer"`
	Asset              string    `json:"asset"`
	Recipient          string    `json:"recipient"`
	AmountAtomic       string    `json:"amount_atomic"`
	PaymentID          string    `json:"payment_id"`
	TransactionHash    string    `json:"transaction_hash"`
	ReceiptBlock       string    `json:"receipt_block"`
	ReceiptBlockHash   string    `json:"receipt_block_hash"`
	ResponseBodyBytes  int64     `json:"response_body_bytes"`
	ResponseBodySHA256 string    `json:"response_body_sha256"`
	CompletedAt        time.Time `json:"completed_at"`
}
