// Package iam is the identity & access module: phone OTP login and users
// (docs/architecture/domain-model.md §3.1).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; the domain, app and adapters packages are the
// module's private implementation (enforced by lint rules).
package iam

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/otpcode"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/sms"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/tokens"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool        *pgxpool.Pool
	Clock       clock.Clock
	Logger      *slog.Logger
	OTPSecret   []byte        // HMAC key for stored codes, ≥ 32 bytes
	TokenSecret []byte        // derives the access-token signing key, ≥ 32 bytes
	OTPSender   app.OTPSender // nil = development console sender
	OTPCodes    app.CodeGenerator
}

// Module is the wired iam module.
type Module struct {
	http   *httpapi.Handlers
	signer *tokens.Signer
	users  *postgres.Users
}

// ErrUnknownUser reports a user ID with no account behind it.
var ErrUnknownUser = errors.New("iam: unknown user")

// New wires repositories, use cases and HTTP handlers — manual dependency
// injection: every dependency is visible right here, no framework needed.
func New(d Deps) (*Module, error) {
	hasher, err := otpcode.NewHMACHasher(d.OTPSecret)
	if err != nil {
		return nil, err
	}
	sender := d.OTPSender
	if sender == nil {
		sender = sms.NewConsole(d.Logger)
	}
	var codes app.CodeGenerator = otpcode.RandomCodes{}
	if d.OTPCodes != nil {
		codes = d.OTPCodes
	}

	signer, err := tokens.NewSigner(d.TokenSecret, d.Clock)
	if err != nil {
		return nil, err
	}

	otpPolicy := domain.DefaultOTPPolicy()
	challenges := postgres.NewOTPChallenges(d.Pool)
	users := postgres.NewUsers(d.Pool)
	sessions := postgres.NewSessions(d.Pool)
	issuer := app.NewSessionIssuer(sessions, signer, tokens.RefreshSecrets{}, d.Clock, domain.DefaultTokenPolicy())

	return &Module{
		signer: signer,
		users:  users,
		http: httpapi.NewHandlers(httpapi.UseCases{
			RequestOTP: app.NewRequestOTPHandler(challenges, codes, hasher, sender, d.Clock, otpPolicy),
			VerifyOTP:  app.NewVerifyOTPHandler(challenges, users, hasher, issuer, d.Clock, otpPolicy),
			Refresh:    app.NewRefreshHandler(issuer, users),
			Logout:     app.NewLogoutHandler(sessions, d.Clock),
			GetMe:      app.NewGetMeHandler(users),
		}, d.Logger),
	}, nil
}

// PhoneOf returns the phone number a user signs in with. Other modules use
// it to tie something sent to a phone (a staff invitation) to the account
// that proved it owns that phone.
func (m *Module) PhoneOf(ctx context.Context, id shared.UserID) (shared.PhoneNumber, error) {
	u, err := m.users.ByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return shared.PhoneNumber{}, ErrUnknownUser
	}
	if err != nil {
		return shared.PhoneNumber{}, err
	}
	return u.Phone(), nil
}

// HTTP returns the handlers for the iam API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// Authenticate verifies an access token and says who presented it. It is
// the auth.Authenticator for the whole API: no database call, so it adds
// microseconds per request. (Logout and blocking take effect when the
// access token expires; ADR-0014.)
func (m *Module) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	claims, err := m.signer.Verify(token)
	if err != nil {
		return auth.Principal{}, err
	}
	return auth.Principal{
		UserID:       claims.UserID,
		SessionID:    claims.SessionID.UUID(),
		PlatformRole: string(claims.PlatformRole),
	}, nil
}
