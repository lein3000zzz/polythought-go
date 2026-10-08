package polymarket

import "github.com/shopspring/decimal"

// OptDecimal is a decimal the API may leave empty, as "" or null. Valid is false when absent.
// It converts to decimal.NullDecimal directly: decimal.NullDecimal(v).
type OptDecimal decimal.NullDecimal

func (o *OptDecimal) UnmarshalJSON(b []byte) error {
	if s := string(b); s == "null" || s == `""` {
		*o = OptDecimal{}
		return nil
	}

	if err := o.Decimal.UnmarshalJSON(b); err != nil {
		return err
	}

	o.Valid = true

	return nil
}
