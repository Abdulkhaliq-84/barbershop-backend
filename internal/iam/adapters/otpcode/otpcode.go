// Package otpcode generates and hashes one-time codes.
package otpcode

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
)

// MinSecretLen is the shortest accepted HMAC secret, in bytes.
const MinSecretLen = 32

// RandomCodes generates codes with crypto/rand — unpredictable, unlike
// math/rand, whose output can be guessed from earlier values.
type RandomCodes struct{}

// NewCode returns a uniformly random code from 000000 to 999999.
func (RandomCodes) NewCode() (domain.OTPCode, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return domain.OTPCode{}, fmt.Errorf("random code: %w", err)
	}
	return domain.ParseOTPCode(fmt.Sprintf("%06d", n.Int64()))
}

// HMACHasher hashes codes with HMAC-SHA256 and a server secret. A plain
// SHA-256 of a 6-digit code could be reversed by trying all million codes;
// without the secret, a leaked hash is useless.
type HMACHasher struct {
	secret []byte
}

// NewHMACHasher returns a hasher; the secret must be at least MinSecretLen bytes.
func NewHMACHasher(secret []byte) (*HMACHasher, error) {
	if len(secret) < MinSecretLen {
		return nil, errors.New("otp secret must be at least 32 bytes")
	}
	// Keep a private copy: the caller's slice could be changed later.
	return &HMACHasher{secret: bytes.Clone(secret)}, nil
}

// Hash returns HMAC-SHA256(secret, code).
func (h *HMACHasher) Hash(code domain.OTPCode) []byte {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(code.Digits()))
	return mac.Sum(nil)
}
