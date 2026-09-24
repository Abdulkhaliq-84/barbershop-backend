package otpcode_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/otpcode"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
)

func mustCode(t *testing.T, s string) domain.OTPCode {
	t.Helper()
	c, err := domain.ParseOTPCode(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHMACHasher(t *testing.T) {
	t.Parallel()
	secret := []byte("0123456789abcdef0123456789abcdef")

	if _, err := otpcode.NewHMACHasher(secret[:otpcode.MinSecretLen-1]); err == nil {
		t.Fatal("a 31-byte secret was accepted")
	}

	h, err := otpcode.NewHMACHasher(secret)
	if err != nil {
		t.Fatal(err)
	}
	// Known answer, computed independently:
	//   python3 -c "import hmac,hashlib;print(hmac.new(b'0123456789abcdef0123456789abcdef',b'482193',hashlib.sha256).hexdigest())"
	// It fails if someone swaps HMAC-SHA256 for something else.
	got := hex.EncodeToString(h.Hash(mustCode(t, "482193")))
	if want := "1c819da9ff3325171245691f92e5f6b36a50890cf236e93efaa0c09f71c58190"; got != want {
		t.Errorf("Hash = %s, want %s", got, want)
	}

	// The hasher keeps its own copy of the secret.
	secret[0] = 'X'
	if again := hex.EncodeToString(h.Hash(mustCode(t, "482193"))); again != got {
		t.Error("changing the caller's secret slice changed the hash")
	}

	other, _ := otpcode.NewHMACHasher([]byte("another-secret-another-secret-xyz"))
	if bytes.Equal(other.Hash(mustCode(t, "482193")), h.Hash(mustCode(t, "482193"))) {
		t.Error("different secrets produced the same hash")
	}
}

func TestRandomCodes(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for range 1000 {
		c, err := otpcode.RandomCodes{}.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Digits()) != 6 {
			t.Fatalf("code %q is not 6 digits", c.Digits())
		}
		seen[c.Digits()] = true
	}
	// 1000 draws from a million: expect ~0.5 repeats. Many more means a broken generator.
	if len(seen) < 990 {
		t.Errorf("only %d distinct codes in 1000 draws", len(seen))
	}
}
