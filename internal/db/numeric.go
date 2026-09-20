package dbaccess

import (
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
)

// NumericText preserves a PostgreSQL NUMERIC's exact decimal value and NULL
// state. Domain readers still validate signs, canonical form and numeric range.
func NumericText(value pgtype.Numeric) (pgtype.Text, error) {
	if !value.Valid {
		return pgtype.Text{}, nil
	}
	if value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil {
		return pgtype.Text{}, errors.New("stored numeric value is not finite")
	}
	codec := pgtype.NumericCodec{}
	encoded, err := codec.PlanEncode(nil, pgtype.NumericOID, pgtype.TextFormatCode, value).Encode(value, nil)
	if err != nil {
		return pgtype.Text{}, err
	}
	return pgtype.Text{String: string(encoded), Valid: true}, nil
}
