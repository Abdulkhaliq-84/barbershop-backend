package shared

import (
	"errors"
	"log/slog"
	"strings"
)

// ErrInvalidPhoneNumber reports input that is not a Saudi mobile number.
// It deliberately does not echo the input: errors end up in logs, and the
// input is (almost) a phone number.
var ErrInvalidPhoneNumber = errors.New("invalid Saudi mobile number")

// maxPhoneInput bounds the work done on untrusted input.
const maxPhoneInput = 64 // bytes; Arabic-Indic digits take 2 bytes each in UTF-8

// PhoneNumber is a Saudi mobile number in E.164 form: +9665XXXXXXXX.
//
// v1 serves Saudi Arabia only. Supporting other GCC countries later means
// changing this constructor (e.g. to libphonenumber), not its callers.
type PhoneNumber struct {
	e164 string
}

// NewPhoneNumber accepts the ways people actually type a Saudi mobile:
// "+966 55 123 4567", "00966551234567", "966551234567", "0551234567",
// "551234567" — with spaces, dashes, dots or parentheses, in Western (0-9),
// Arabic-Indic (٠-٩) or Eastern Arabic-Indic (۰-۹) digits.
func NewPhoneNumber(raw string) (PhoneNumber, error) {
	if len(raw) > maxPhoneInput {
		return PhoneNumber{}, ErrInvalidPhoneNumber
	}

	var digits strings.Builder
	plus := false
	for i, r := range strings.TrimSpace(raw) {
		if d, ok := WesternDigit(r); ok {
			digits.WriteRune(d)
			continue
		}
		switch {
		case r == '+' && i == 0:
			plus = true
		case strings.ContainsRune(" -.()\u00a0", r): // \u00a0 = no-break space
			// separators people type; ignore them
		default:
			return PhoneNumber{}, ErrInvalidPhoneNumber
		}
	}

	national, ok := saudiNational(digits.String(), plus)
	if !ok {
		return PhoneNumber{}, ErrInvalidPhoneNumber
	}
	return PhoneNumber{e164: "+966" + national}, nil
}

// saudiNational strips the country code or trunk prefix and returns the 9-digit
// mobile number (5XXXXXXXX).
func saudiNational(d string, plus bool) (string, bool) {
	switch {
	case plus:
		var ok bool
		if d, ok = strings.CutPrefix(d, "966"); !ok {
			return "", false
		}
	case strings.HasPrefix(d, "00966"):
		d = d[len("00966"):]
	case strings.HasPrefix(d, "966") && len(d) >= 12:
		d = d[len("966"):]
	}
	// "0" is the domestic trunk prefix (05X…); people also add it after +966.
	if len(d) == 10 && d[0] == '0' {
		d = d[1:]
	}
	if len(d) != 9 || d[0] != '5' {
		return "", false
	}
	return d, true
}

// String returns the E.164 form, for storage and SMS providers.
func (p PhoneNumber) String() string { return p.e164 }

// IsZero reports whether the number was never set.
func (p PhoneNumber) IsZero() bool { return p.e164 == "" }

// Masked hides the middle digits: +9665•••••678. Use it in anything a human
// might see besides the owner (support tools, admin screens).
func (p PhoneNumber) Masked() string {
	if p.IsZero() {
		return ""
	}
	return p.e164[:5] + "•••••" + p.e164[len(p.e164)-3:]
}

// LogValue makes slog print the masked form automatically, so a phone number
// can never leak into logs by accident (CLAUDE.md: never log full numbers).
func (p PhoneNumber) LogValue() slog.Value { return slog.StringValue(p.Masked()) }
