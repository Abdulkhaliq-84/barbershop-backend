package shared_test

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestNewPhoneNumberAcceptsCommonFormats(t *testing.T) {
	t.Parallel()

	const want = "+966551234567"
	inputs := []string{
		"+966551234567",
		"+966 55 123 4567",
		"+966-55-123-4567",
		"+966 (55) 123.4567",
		"00966551234567",
		"966551234567",
		"0551234567",
		"055 123 4567",
		"551234567",
		"  0551234567  ",
		"+966 0551234567",        // trunk 0 after the country code — a common slip
		"٠٥٥١٢٣٤٥٦٧",             // Arabic-Indic digits
		"+٩٦٦ ٥٥ ١٢٣ ٤٥٦٧",       // Arabic-Indic with country code
		"۰۵۵۱۲۳۴۵۶۷",             // Eastern Arabic-Indic digits
		"055\u00a0123\u00a04567", // no-break spaces from copy-paste
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			p, err := shared.NewPhoneNumber(in)
			if err != nil {
				t.Fatalf("NewPhoneNumber(%q) error = %v", in, err)
			}
			if p.String() != want {
				t.Errorf("NewPhoneNumber(%q) = %s, want %s", in, p, want)
			}
		})
	}
}

func TestNewPhoneNumberRejects(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in string }{
		{"empty", ""},
		{"landline", "0112345678"},
		{"landline with country code", "+966112345678"},
		{"too short", "05512345"},
		{"too long", "05512345678"},
		{"other country", "+971501234567"},
		{"letters", "05512345ab"},
		{"plus in the middle", "0551+234567"},
		{"double plus", "++966551234567"},
		{"huge input", strings.Repeat("5", 1000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := shared.NewPhoneNumber(tt.in)
			if !errors.Is(err, shared.ErrInvalidPhoneNumber) {
				t.Errorf("NewPhoneNumber(%q) error = %v, want ErrInvalidPhoneNumber", tt.in, err)
			}
			if err != nil && strings.Contains(err.Error(), "5512") {
				t.Errorf("error %q echoes the input", err)
			}
		})
	}
}

func TestPhoneNumberNeverLogsFullNumber(t *testing.T) {
	t.Parallel()

	p, err := shared.NewPhoneNumber("0551234567")
	if err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("otp requested", "phone", p)

	if strings.Contains(logs.String(), p.String()) {
		t.Errorf("log contains the full number: %s", logs.String())
	}
	if !strings.Contains(logs.String(), p.Masked()) {
		t.Errorf("log should contain the masked number %q: %s", p.Masked(), logs.String())
	}
	if p.Masked() != "+9665•••••567" {
		t.Errorf("Masked() = %q", p.Masked())
	}
}

var e164 = regexp.MustCompile(`^\+9665\d{8}$`)

// FuzzNewPhoneNumber feeds random input to the parser. Go's fuzzer mutates
// the seed corpus looking for panics or broken properties.
// Run it for a while with: go test ./internal/shared -fuzz=FuzzNewPhoneNumber -fuzztime=30s
func FuzzNewPhoneNumber(f *testing.F) {
	for _, seed := range []string{"0551234567", "+966551234567", "٠٥٥١٢٣٤٥٦٧", "", "+", "00966", "+966 0551234567"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		p, err := shared.NewPhoneNumber(in)
		if err != nil {
			return // rejecting is fine; panicking is not
		}
		if !e164.MatchString(p.String()) {
			t.Fatalf("NewPhoneNumber(%q) = %q, not E.164 Saudi mobile", in, p)
		}
		again, err := shared.NewPhoneNumber(p.String())
		if err != nil || again != p {
			t.Fatalf("round trip of %q failed: %v, %v", p, again, err)
		}
	})
}
