//go:build integration

package billing

import "context"

// ReserveLegacyForIntegrationTest seeds superseded request-payment history for
// migration and operator-audit tests. It is absent from production builds.
func (ledger *PostgresLedger) ReserveLegacyForIntegrationTest(
	ctx context.Context,
	input ReserveInput,
) (Reservation, error) {
	return ledger.reservePayment(ctx, input)
}
