package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestParseOTPCode(t *testing.T) {
	t.Parallel()

	valid := []struct{ in, want string }{
		{"482193", "482193"},
		{" 000001 ", "000001"}, // pasted with spaces
		{"٤٨٢١٩٣", "482193"},   // Arabic-Indic digits (Arabic keyboard)
		{"۴۸۲۱۹۳", "482193"},   // Eastern Arabic-Indic digits (Persian/Urdu keyboard)
	}
	for _, tt := range valid {
		code, err := domain.ParseOTPCode(tt.in)
		if err != nil || code.Digits() != tt.want {
			t.Errorf("ParseOTPCode(%q) = %q, %v; want %q", tt.in, code.Digits(), err, tt.want)
		}
	}
	for _, in := range []string{"", "12345", "1234567", "12 345", "12a456", "１２３４５６"} {
		if _, err := domain.ParseOTPCode(in); !errors.Is(err, domain.ErrInvalidOTPCode) {
			t.Errorf("ParseOTPCode(%q) error = %v, want ErrInvalidOTPCode", in, err)
		}
	}
}

var (
	t0     = time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	right  = []byte("hash-of-right-code")
	wrong  = []byte("hash-of-wrong-code")
	policy = domain.DefaultOTPPolicy()
)

func newChallenge(t *testing.T) *domain.OTPChallenge {
	t.Helper()
	phone, err := shared.NewPhoneNumber("0551234567")
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), phone, right, t0, policy.TTL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		guesses      [][]byte // earlier guesses, before the one under test
		guess        []byte
		at           time.Time
		wantErr      error
		wantAttempts int
		wantConsumed bool
	}{
		{name: "right code", guess: right, at: t0.Add(time.Minute), wantAttempts: 1, wantConsumed: true},
		{name: "wrong code", guess: wrong, at: t0.Add(time.Minute), wantErr: domain.ErrOTPInvalid, wantAttempts: 1},
		{name: "right code after a typo", guesses: [][]byte{wrong}, guess: right, at: t0.Add(time.Minute), wantAttempts: 2, wantConsumed: true},
		{name: "expired", guess: right, at: t0.Add(policy.TTL), wantErr: domain.ErrOTPExpired},
		{name: "last wrong guess locks the code", guesses: [][]byte{wrong, wrong, wrong, wrong}, guess: wrong, at: t0.Add(time.Minute), wantErr: domain.ErrOTPTooManyAttempts, wantAttempts: 5},
		{name: "locked even for the right code", guesses: [][]byte{wrong, wrong, wrong, wrong, wrong}, guess: right, at: t0.Add(time.Minute), wantErr: domain.ErrOTPTooManyAttempts, wantAttempts: 5},
		{name: "used once only", guesses: [][]byte{right}, guess: right, at: t0.Add(time.Minute), wantErr: domain.ErrOTPInvalid, wantAttempts: 1, wantConsumed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newChallenge(t)
			for _, g := range tt.guesses {
				_ = c.Verify(g, t0.Add(30*time.Second), policy.MaxAttempts)
			}

			err := c.Verify(tt.guess, tt.at, policy.MaxAttempts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
			if c.Attempts() != tt.wantAttempts {
				t.Errorf("Attempts() = %d, want %d", c.Attempts(), tt.wantAttempts)
			}
			if (c.ConsumedAt() != nil) != tt.wantConsumed {
				t.Errorf("consumed = %v, want %v", c.ConsumedAt() != nil, tt.wantConsumed)
			}
		})
	}
}

func TestNewOTPChallengeValidates(t *testing.T) {
	t.Parallel()

	phone, _ := shared.NewPhoneNumber("0551234567")
	id := shared.NewID[domain.OTPChallengeTag]()
	cases := map[string]func() (*domain.OTPChallenge, error){
		"zero id": func() (*domain.OTPChallenge, error) {
			return domain.NewOTPChallenge(domain.OTPChallengeID{}, phone, right, t0, time.Minute)
		},
		"zero phone": func() (*domain.OTPChallenge, error) {
			return domain.NewOTPChallenge(id, shared.PhoneNumber{}, right, t0, time.Minute)
		},
		"no hash": func() (*domain.OTPChallenge, error) {
			return domain.NewOTPChallenge(id, phone, nil, t0, time.Minute)
		},
		"zero ttl": func() (*domain.OTPChallenge, error) {
			return domain.NewOTPChallenge(id, phone, right, t0, 0)
		},
	}
	for name, create := range cases {
		if _, err := create(); err == nil {
			t.Errorf("%s: NewOTPChallenge() error = nil, want error", name)
		}
	}
}
