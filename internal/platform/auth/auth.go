// Package auth carries the authenticated caller through a request.
//
// The iam module verifies access tokens; the HTTP layer stores the result
// here; every module reads it with PrincipalFrom. Keeping the type in the
// platform means no module ever imports iam's internals to learn "who is
// calling" (ADR-0014).
package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Principal is the verified identity behind a request.
type Principal struct {
	UserID       shared.UserID
	SessionID    uuid.UUID // the sign-in session the access token belongs to
	PlatformRole string    // "none" or "admin"; business roles are checked per request
}

// IsPlatformAdmin reports whether the caller may use platform admin operations.
func (p Principal) IsPlatformAdmin() bool { return p.PlatformRole == "admin" }

// Authenticator verifies a bearer token and says who presented it.
type Authenticator func(ctx context.Context, token string) (Principal, error)

type principalKey struct{}

// WithPrincipal returns a context that carries p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller, if the request carried a valid token.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
