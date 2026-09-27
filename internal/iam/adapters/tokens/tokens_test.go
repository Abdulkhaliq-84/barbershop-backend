package tokens_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/tokens"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

const secret = "test-only-token-secret-0123456789abcdef"

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newSigner(t *testing.T, s string) (*tokens.Signer, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	signer, err := tokens.NewSigner([]byte(s), clk)
	if err != nil {
		t.Fatal(err)
	}
	return signer, clk
}

func claims() app.AccessClaims {
	return app.AccessClaims{
		UserID:       shared.NewID[shared.UserTag](),
		SessionID:    shared.NewID[domain.SessionTag](),
		PlatformRole: domain.PlatformRoleAdmin,
		IssuedAt:     t0,
		ExpiresAt:    t0.Add(15 * time.Minute),
	}
}

func TestSignerRoundTrip(t *testing.T) {
	t.Parallel()
	signer, clk := newSigner(t, secret)
	want := claims()
	token, err := signer.Issue(want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != want.UserID || got.SessionID != want.SessionID || got.PlatformRole != want.PlatformRole || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("claims = %+v, want %+v", got, want)
	}

	// Valid until expiry (plus 30 s leeway for clock skew), then not.
	clk.Advance(15*time.Minute + 20*time.Second)
	if _, err := signer.Verify(token); err != nil {
		t.Errorf("inside leeway: %v", err)
	}
	clk.Advance(time.Minute)
	if _, err := signer.Verify(token); !errors.Is(err, tokens.ErrInvalidToken) {
		t.Errorf("expired: error = %v, want ErrInvalidToken", err)
	}
}

func TestSignerRejectsShortSecret(t *testing.T) {
	t.Parallel()
	if _, err := tokens.NewSigner([]byte("too-short"), clock.NewFake(t0)); err == nil {
		t.Fatal("a 9-byte secret was accepted")
	}
}

// forge signs arbitrary claims with the key the Signer derives from secret,
// so each test changes exactly one thing.
func forge(t *testing.T, method jwt.SigningMethod, key any, mutate func(jwt.MapClaims, map[string]any)) string {
	t.Helper()
	c := claims()
	mc := jwt.MapClaims{
		"iss": "barbershop-backend", "aud": []string{"barbershop-api"},
		"sub": c.UserID.String(), "sid": c.SessionID.String(), "platform_role": "admin",
		"iat": t0.Unix(), "nbf": t0.Unix(), "exp": t0.Add(15 * time.Minute).Unix(), "jti": "x",
	}
	token := jwt.NewWithClaims(method, mc)
	seed := sha256.Sum256([]byte(secret))
	public, _ := ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey)
	fp := sha256.Sum256(public)
	token.Header["kid"] = base64.RawURLEncoding.EncodeToString(fp[:8])
	if mutate != nil {
		mutate(mc, token.Header)
	}
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignerRejectsForgedTokens(t *testing.T) {
	t.Parallel()
	signer, _ := newSigner(t, secret)
	seed := sha256.Sum256([]byte(secret))
	private := ed25519.NewKeyFromSeed(seed[:])
	public, _ := private.Public().(ed25519.PublicKey)

	if _, err := signer.Verify(forge(t, jwt.SigningMethodEdDSA, private, nil)); err != nil {
		t.Fatalf("the control token must verify: %v", err)
	}

	tests := map[string]string{
		"alg none":          forge(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, nil),
		"HS256 public key":  forge(t, jwt.SigningMethodHS256, []byte(public), nil), // the classic key-confusion attack
		"other signing key": forge(t, jwt.SigningMethodEdDSA, ed25519.NewKeyFromSeed(make([]byte, 32)), nil),
		"wrong issuer":      forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { c["iss"] = "evil" }),
		"wrong audience":    forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { c["aud"] = "other-api" }),
		"no expiry":         forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { delete(c, "exp") }),
		"issued in future":  forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { c["iat"] = t0.Add(time.Hour).Unix() }),
		"unknown key id":    forge(t, jwt.SigningMethodEdDSA, private, func(_ jwt.MapClaims, h map[string]any) { h["kid"] = "old-key" }),
		"no key id":         forge(t, jwt.SigningMethodEdDSA, private, func(_ jwt.MapClaims, h map[string]any) { delete(h, "kid") }),
		"bad subject":       forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { c["sub"] = "admin" }),
		"bad session id":    forge(t, jwt.SigningMethodEdDSA, private, func(c jwt.MapClaims, _ map[string]any) { c["sid"] = "" }),
		"garbage":           "not.a.jwt",
	}
	for name, token := range tests {
		if _, err := signer.Verify(token); !errors.Is(err, tokens.ErrInvalidToken) {
			t.Errorf("%s: error = %v, want ErrInvalidToken", name, err)
		}
	}

	// Promoting yourself to admin in a genuine token breaks the signature.
	c := claims()
	c.PlatformRole = domain.PlatformRoleNone
	genuine, _ := signer.Issue(c)
	parts := strings.Split(genuine, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !strings.Contains(string(payload), `"platform_role":"none"`) {
		t.Fatalf("unexpected payload %s", payload)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(payload), `"none"`, `"admin"`, 1)))
	if _, err := signer.Verify(strings.Join(parts, ".")); !errors.Is(err, tokens.ErrInvalidToken) {
		t.Errorf("tampered payload: error = %v, want ErrInvalidToken", err)
	}
}

func TestSignersWithDifferentSecretsDontTrustEachOther(t *testing.T) {
	t.Parallel()
	a, _ := newSigner(t, secret)
	b, _ := newSigner(t, secret+"-other")
	token, _ := a.Issue(claims())
	if _, err := b.Verify(token); !errors.Is(err, tokens.ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestRefreshSecrets(t *testing.T) {
	t.Parallel()
	var s tokens.RefreshSecrets
	seen := map[string]bool{}
	for range 100 {
		token, hash, err := s.New()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(token, "rt_") || len(token) != 3+43 || len(hash) != 32 || seen[token] {
			t.Fatalf("token %q, hash %d bytes, seen before: %v", token, len(hash), seen[token])
		}
		seen[token] = true
		again, err := s.Hash(token)
		if err != nil || string(again) != string(hash) {
			t.Fatalf("Hash(New()) = %x, %v; want %x", again, err, hash)
		}
	}
	for _, bad := range []string{"", "rt_", "rt_short", "xx_" + strings.Repeat("A", 43), "rt_" + strings.Repeat("!", 43), "rt_" + strings.Repeat("A", 44)} {
		if _, err := s.Hash(bad); err == nil {
			t.Errorf("Hash(%q) accepted a malformed token", bad)
		}
	}
}
