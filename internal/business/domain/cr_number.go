package domain

import (
	"strings"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// CRNumber is a Saudi Commercial Registration number: exactly 10 digits.
// Owners type it from a paper certificate, often on an Arabic keyboard, so
// Arabic-Indic digits and spaces are accepted and normalised away.
type CRNumber struct {
	digits string
}

// NewCRNumber parses and normalises a CR number.
func NewCRNumber(s string) (CRNumber, error) {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '-' {
			continue
		}
		d, ok := shared.WesternDigit(r)
		if !ok {
			return CRNumber{}, ErrInvalidCRNumber
		}
		b.WriteRune(d)
	}
	if b.Len() != 10 {
		return CRNumber{}, ErrInvalidCRNumber
	}
	return CRNumber{digits: b.String()}, nil
}

// String returns the 10 Western digits.
func (c CRNumber) String() string { return c.digits }
