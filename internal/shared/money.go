package shared

import (
	"errors"
	"fmt"
	"math"
)

// Money errors.
var (
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrCurrencyMismatch    = errors.New("currency mismatch")
	ErrMoneyOverflow       = errors.New("money overflow")
)

// Currency is an ISO 4217 code.
type Currency string

// SAR is the Saudi riyal: 1 riyal = 100 halalas.
const SAR Currency = "SAR"

// minorPerMajor lists supported currencies and their minor units per major
// unit. Adding a GCC currency later is one line (KWD would be 1000).
var minorPerMajor = map[Currency]int64{SAR: 100}

// Money is an amount in a currency's minor unit (halalas for SAR).
//
// Never use float64 for money: 0.1 + 0.2 != 0.3 in binary floating point,
// and rounding errors add up across thousands of bookings. Integers are exact.
type Money struct {
	amount   int64
	currency Currency
}

// NewMoney returns amount minor units of currency.
func NewMoney(amount int64, currency Currency) (Money, error) {
	if _, ok := minorPerMajor[currency]; !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnsupportedCurrency, currency)
	}
	return Money{amount: amount, currency: currency}, nil
}

// Halalas is shorthand for an amount in SAR: Halalas(6000) is 60.00 SAR.
func Halalas(amount int64) Money {
	return Money{amount: amount, currency: SAR}
}

// Amount returns the amount in minor units.
func (m Money) Amount() int64 { return m.amount }

// Currency returns the currency code.
func (m Money) Currency() Currency { return m.currency }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.amount == 0 }

// IsNegative reports whether the amount is below zero (refunds, corrections).
func (m Money) IsNegative() bool { return m.amount < 0 }

// Add returns m + o. Both must share a currency.
func (m Money) Add(o Money) (Money, error) {
	if m.currency != o.currency {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	if (o.amount > 0 && m.amount > math.MaxInt64-o.amount) ||
		(o.amount < 0 && m.amount < math.MinInt64-o.amount) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{amount: m.amount + o.amount, currency: m.currency}, nil
}

// Multiply returns m × n, e.g. a price times a quantity.
func (m Money) Multiply(n int64) (Money, error) {
	product := m.amount * n
	// Overflow check: dividing back must give n. The one case division can't
	// catch is -1 × MinInt64, whose true result doesn't fit in int64.
	if m.amount != 0 && (product/m.amount != n || (m.amount == -1 && n == math.MinInt64)) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{amount: product, currency: m.currency}, nil
}

// String formats the amount for logs and debugging: "60.00 SAR".
// User-facing formatting (Arabic digits, "ر.س") belongs to the app.
func (m Money) String() string {
	per, ok := minorPerMajor[m.currency]
	if !ok {
		return fmt.Sprintf("%d %s", m.amount, m.currency)
	}
	// Split first, then drop the sign: Go's / and % truncate toward zero, so
	// both parts share the amount's sign and negating them can't overflow.
	whole, frac, sign := m.amount/per, m.amount%per, ""
	if m.amount < 0 {
		whole, frac, sign = -whole, -frac, "-"
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, whole, frac, m.currency)
}
