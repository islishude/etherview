package dbaccess

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNumericTextPreservesPrecisionAndNull(t *testing.T) {
	t.Parallel()
	maximum := "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	for _, raw := range []string{maximum, "9007199254740993", "-42", "0.0001", "100.0000"} {
		var value pgtype.Numeric
		if err := value.Scan(raw); err != nil {
			t.Fatal(err)
		}
		text, err := NumericText(value)
		if err != nil || !text.Valid || text.String != raw {
			t.Fatalf("numeric %s became %+v, %v", raw, text, err)
		}
	}
	text, err := NumericText(pgtype.Numeric{})
	if err != nil || text.Valid {
		t.Fatalf("NULL became %+v, %v", text, err)
	}
	for _, value := range []pgtype.Numeric{{Valid: true}, {Valid: true, NaN: true}, {Valid: true, Int: big.NewInt(1), InfinityModifier: pgtype.Infinity}} {
		if _, err := NumericText(value); err == nil {
			t.Fatalf("accepted nonfinite numeric %+v", value)
		}
	}
}
