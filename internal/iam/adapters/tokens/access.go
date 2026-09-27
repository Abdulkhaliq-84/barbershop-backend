// Package tokens issues and checks the tokens a signed-in client holds:
// short-lived access tokens (JWT, Ed25519) and opaque refresh tokens.
package tokens

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MinSecretLen is the shortest accepted signing secret, in bytes.
const MinSecretLen = 32

const (
	issuer   = "barbershop-backend"
	audience = "barbershop-api"
	leeway   = 30 * time.Second // tolerated clock skew between servers
)

// ErrInvalidToken reports an access token that is malformed, forged,
// expired or not meant for this API. The reason is deliberately not told
// apart: clients react the same way (refresh, or sign in again).
var ErrInvalidToken = errors.New("invalid access token")

// claims is the JSON payload: registered claims plus our two.
type claims struct {
	jwt.RegisteredClaims
	SessionID    string `json:"sid"`
	PlatformRole string `json:"platform_role"`
}

// Signer issues and verifies EdDSA (Ed25519) access tokens.
type Signer struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	keyID   string
	clock   clock.Clock
}

// NewSigner derives the signing key from secret (at least MinSecretLen
// bytes): the Ed25519 seed is SHA-256(secret), so the secret can be a random
// string like OTP_SECRET. The key ID ("kid") comes from the public key, so a
// token signed by an old key is recognised after a key change.
func NewSigner(secret []byte, clk clock.Clock) (*Signer, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("token signing secret must be at least %d bytes", MinSecretLen)
	}
	seed := sha256.Sum256(secret)
	private := ed25519.NewKeyFromSeed(seed[:])
	public, _ := private.Public().(ed25519.PublicKey)
	fingerprint := sha256.Sum256(public)
	return &Signer{
		private: private,
		public:  public,
		keyID:   base64.RawURLEncoding.EncodeToString(fingerprint[:8]),
		clock:   clk,
	}, nil
}

// Issue signs an access token for c.
func (s *Signer) Issue(c app.AccessClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   c.UserID.String(),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(c.IssuedAt),
			NotBefore: jwt.NewNumericDate(c.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(c.ExpiresAt),
			ID:        uuid.NewString(),
		},
		SessionID:    c.SessionID.String(),
		PlatformRole: string(c.PlatformRole),
	})
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.private)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// Verify checks the signature, algorithm, key ID, issuer, audience and
// lifetime of an access token and returns what it says.
func (s *Signer) Verify(raw string) (app.AccessClaims, error) {
	var c claims
	_, err := jwt.ParseWithClaims(raw, &c,
		func(t *jwt.Token) (any, error) {
			if kid, _ := t.Header["kid"].(string); kid != s.keyID {
				return nil, errors.New("unknown key id")
			}
			return s.public, nil
		},
		// Pinning the algorithm stops "alg: none" and HMAC-with-the-public-key tricks.
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(s.clock.Now),
	)
	if err != nil {
		return app.AccessClaims{}, ErrInvalidToken
	}

	userID, err := shared.ParseID[shared.UserTag](c.Subject)
	if err != nil {
		return app.AccessClaims{}, ErrInvalidToken
	}
	sessionID, err := shared.ParseID[domain.SessionTag](c.SessionID)
	if err != nil {
		return app.AccessClaims{}, ErrInvalidToken
	}
	return app.AccessClaims{
		UserID:       userID,
		SessionID:    sessionID,
		PlatformRole: domain.PlatformRole(c.PlatformRole),
		IssuedAt:     c.IssuedAt.Time,
		ExpiresAt:    c.ExpiresAt.Time,
	}, nil
}
