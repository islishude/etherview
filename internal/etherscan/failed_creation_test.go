package etherscan

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func TestFailedCreationDoesNotExposeAttemptedAddressAsCreatedContract(t *testing.T) {
	t.Parallel()
	transaction := testSignedTransaction(7, "")
	address := crypto.CreateAddress(testTransactionSender(), transaction.Nonce()).Hex()
	for _, status := range []string{"0x0", "0x1"} {
		t.Run(status, func(t *testing.T) {
			item, err := scanAccountTransaction(dbgen.EtherscanAccountTransactionsRow{
				TransactionRaw: testTransactionJSON(7, ""), ReceiptRaw: testReceiptJSON(status, address),
				BlockTimestamp: "100", BlockBaseFee: new("0x3b9aca00"), BlockNumber: "10",
				BlockHash: testHashBytes(3), TransactionIndex: 1,
				TransactionHash: transaction.Hash().Bytes(), TipNumber: "12",
			})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if status == "0x1" {
				want = address
			}
			if item.ContractAddress != want {
				t.Fatalf("contract address = %q, want %q", item.ContractAddress, want)
			}
		})
	}
}
