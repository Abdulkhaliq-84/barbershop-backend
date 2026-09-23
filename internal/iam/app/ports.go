// Package app holds the iam use cases (commands). Each one loads what it
// needs through small interfaces (ports), applies domain rules and saves the
// result. It knows nothing about HTTP, SQL or SMS providers.
package app

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// CodeGenerator creates one-time codes. Production uses crypto/rand; tests
// use a fixed code so they know what to type.
type CodeGenerator interface {
	NewCode() (domain.OTPCode, error)
}

// CodeHasher turns a code into the value stored in the database. It holds a
// server secret, so the stored hash is useless without it.
type CodeHasher interface {
	Hash(code domain.OTPCode) []byte
}

// OTPSender delivers a code to a phone (SMS in production, the console in
// development).
type OTPSender interface {
	SendOTP(ctx context.Context, to shared.PhoneNumber, code domain.OTPCode) error
}
