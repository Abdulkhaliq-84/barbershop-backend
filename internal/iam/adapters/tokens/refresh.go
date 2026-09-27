package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// refreshPrefix makes refresh tokens recognisable, e.g. to secret scanners.
const refreshPrefix = "rt_"

// refreshBytes is the token's randomness: 256 bits, far beyond guessing.
const refreshBytes = 32

// errMalformed reports a presented refresh token that we could never have issued.
var errMalformed = errors.New("malformed refresh token")

// RefreshSecrets creates opaque refresh tokens: "rt_" + 43 base64url characters.
type RefreshSecrets struct{}

// New returns a random token and its SHA-256 hash (what is stored). A plain
// hash is enough here, unlike OTP codes: 256 random bits can't be brute-forced.
func (RefreshSecrets) New() (string, []byte, error) {
	b := make([]byte, refreshBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token := refreshPrefix + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

// Hash returns the stored form of a presented token.
func (RefreshSecrets) Hash(token string) ([]byte, error) {
	body, ok := strings.CutPrefix(token, refreshPrefix)
	if !ok {
		return nil, errMalformed
	}
	if b, err := base64.RawURLEncoding.DecodeString(body); err != nil || len(b) != refreshBytes {
		return nil, errMalformed
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
