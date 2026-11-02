//go:build integration

package facilitator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func TestPendingCapacityRefusalAndExistingReservation(t *testing.T) {
	pool, url := journalDB(t)
	lock, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close(context.Background()) }()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	ch := &chainRuntime{adapter: &chainClient{signer: &localSigner{key: key}}, gate: make(chan struct{}, 1)}
	ch.healthy.Store(true)
	s := &Service{queries: q, lock: lock}
	s.active.Store(true)
	request := testRequest()
	body, _ := json.Marshal(request)
	digest := sha256.Sum256(body)
	args := dbgen.FacilitatorReserveParams{AuthorizationID: "existing", RequestDigest: digest[:], RequestBody: body, Network: request.PaymentRequirements.Network, SignerAddress: ch.adapter.signer.Address().Bytes()}
	if _, err = q.FacilitatorReserve(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	result, err := s.executePayment(t.Context(), request, ch, "new", "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", true)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(x402.SettleResponse)
	if !ok || response.Success || response.Transaction != "" || response.ErrorReason != "chain_pending" {
		t.Fatalf("unexpected pending capacity result %+v", result)
	}
	row, err := q.FacilitatorGet(t.Context(), "new")
	if err != nil || row.State != "failed" || len(row.TransactionHash) != 0 {
		t.Fatal("refusal not durable", err)
	}
	// An existing unsigned owner may still sign. Refusal must not terminalize it.
	if _, err = s.rejectUnsubmitted(t.Context(), ch, request, "existing", "", "chain_busy"); err == nil {
		t.Fatal("rejected an existing unsigned reservation")
	}
	row, err = q.FacilitatorGet(t.Context(), "existing")
	if err != nil || row.State != "reserved" || len(row.Response) != 0 {
		t.Fatal("modified the owner's reservation", err)
	}
	changed := request
	changed.PaymentRequirements.Amount = "101"
	if _, err = s.rejectUnsubmitted(t.Context(), ch, changed, "new", "", "chain_busy"); err == nil || err.Error() != "authorization_conflict" {
		t.Fatal("digest conflict lost", err)
	}
}

func TestAdmissionRefusalRacesReservation(t *testing.T) {
	pool, _ := journalDB(t)
	q := dbgen.New(pool)
	digest := sha256.Sum256([]byte("request"))
	for i := range 30 {
		id := fmt.Sprint("race-", i)
		reserve := dbgen.FacilitatorReserveParams{AuthorizationID: id, RequestDigest: digest[:], RequestBody: []byte(`{}`), Network: "eip155:31337", SignerAddress: make([]byte, 20)}
		reject := dbgen.FacilitatorRejectUnsubmittedParams{AuthorizationID: id, RequestDigest: digest[:], RequestBody: []byte(`{}`), Network: reserve.Network, SignerAddress: reserve.SignerAddress, Response: []byte(`{"success":false,"network":"eip155:31337","transaction":"","errorReason":"chain_busy"}`)}
		var reserved, refused int64
		var reserveErr, rejectErr error
		var wg sync.WaitGroup
		wg.Go(func() { reserved, reserveErr = q.FacilitatorReserve(t.Context(), reserve) })
		wg.Go(func() { refused, rejectErr = q.FacilitatorRejectUnsubmitted(t.Context(), reject) })
		wg.Wait()
		if reserveErr != nil || rejectErr != nil || reserved+refused != 1 {
			t.Fatalf("nonexclusive admission: %d %d %v %v", reserved, refused, reserveErr, rejectErr)
		}
		row, err := q.FacilitatorGet(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if refused == 1 {

			var response x402.SettleResponse
			if row.State != "failed" || json.Unmarshal(row.Response, &response) != nil || response.Success || response.Transaction != "" || response.ErrorReason != "chain_busy" {
				t.Fatal("wrong winner state")
			}

			n, e := q.FacilitatorSaveSigned(t.Context(), dbgen.FacilitatorSaveSignedParams{AuthorizationID: id, Nonce: pgtype.Numeric{}, RawTransaction: []byte{1}, TransactionHash: make([]byte, 32)})
			if e != nil || n != 0 {
				t.Fatal("refused authorization could acquire signed bytes", e)
			}
		} else if row.State != "reserved" || len(row.Response) != 0 {
			t.Fatal("refusal overwrote reservation")
		}
	}
}
