// Package iam is the identity & access module: phone OTP login and users
// (docs/architecture/domain-model.md §3.1).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; the domain, app and adapters packages are the
// module's private implementation (enforced by lint rules).
package iam

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/otpcode"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/sms"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Logger    *slog.Logger
	OTPSecret []byte        // HMAC key for stored codes, ≥ 32 bytes
	OTPSender app.OTPSender // nil = development console sender
	OTPCodes  app.CodeGenerator
}

// Module is the wired iam module.
type Module struct {
	http *httpapi.Handlers
}

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

	policy := domain.DefaultOTPPolicy()
	challenges := postgres.NewOTPChallenges(d.Pool)
	users := postgres.NewUsers(d.Pool)

	return &Module{
		http: httpapi.NewHandlers(
			app.NewRequestOTPHandler(challenges, codes, hasher, sender, d.Clock, policy),
			app.NewVerifyOTPHandler(challenges, users, hasher, d.Clock, policy),
			d.Logger,
		),
	}, nil
}

// HTTP returns the handlers for the iam API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
