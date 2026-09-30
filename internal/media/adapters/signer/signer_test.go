package signer_test

import (
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/signer"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

const secret = "test-only-media-secret-0123456789abcdef"

func TestSignAndVerify(t *testing.T) {
	t.Parallel()
	s, err := signer.New([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	id, other := shared.NewID[shared.MediaTag](), shared.NewID[shared.MediaTag]()
	sig := s.Sign(id, 1_800_000_000)

	if !s.Verify(id, 1_800_000_000, sig) {
		t.Fatal("a genuine signature was refused")
	}
	otherKey, _ := signer.New([]byte(secret + "-rotated"))
	for name, ok := range map[string]bool{
		"another file":       s.Verify(other, 1_800_000_000, sig),
		"a later expiry":     s.Verify(id, 1_800_000_001, sig),
		"a changed char":     s.Verify(id, 1_800_000_000, flip(sig)),
		"not base64":         s.Verify(id, 1_800_000_000, "!!!"),
		"empty":              s.Verify(id, 1_800_000_000, ""),
		"another server key": otherKey.Verify(id, 1_800_000_000, sig),
	} {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := signer.New([]byte("short")); err == nil {
		t.Error("a short secret was accepted")
	}
}

func flip(s string) string {
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}
