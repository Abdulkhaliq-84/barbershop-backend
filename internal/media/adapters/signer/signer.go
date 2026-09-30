// Package signer signs media download links with HMAC-SHA256.
package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MinSecretLen is the shortest accepted secret.
const MinSecretLen = 32

// HMAC signs "which file, until when" with a server secret. Changing either
// part — another file ID, a later expiry — breaks the signature, and only
// someone with the secret can make a new one.
type HMAC struct {
	key []byte
}

// New returns a signer. The secret is copied.
func New(secret []byte) (*HMAC, error) {
	if len(secret) < MinSecretLen {
		return nil, errors.New("media signer: secret must be at least 32 bytes")
	}
	return &HMAC{key: append([]byte(nil), secret...)}, nil
}

// Sign returns the link signature for id valid until expires (Unix seconds).
func (h *HMAC) Sign(id shared.MediaID, expires int64) string {
	return base64.RawURLEncoding.EncodeToString(h.mac(id, expires))
}

// Verify checks a signature in constant time, so the response time leaks
// nothing about how many bytes of a guess were right.
func (h *HMAC) Verify(id shared.MediaID, expires int64, signature string) bool {
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(got, h.mac(id, expires))
}

func (h *HMAC) mac(id shared.MediaID, expires int64) []byte {
	m := hmac.New(sha256.New, h.key)
	// A purpose label and fixed separators: the same secret signing some
	// other message format could never produce a valid link by accident.
	m.Write([]byte("media-download\x00" + id.String() + "\x00" + strconv.FormatInt(expires, 10)))
	return m.Sum(nil)
}
