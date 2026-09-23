// Package sms delivers one-time codes. Only a development sender exists so
// far; a real provider (Unifonic / Authentica / Taqnyat) arrives in M7.
package sms

import (
	"context"
	"log/slog"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Console "sends" codes by writing them to the log so you can log in locally.
//
// DEVELOPMENT ONLY: this is the one place a code is ever logged, and the
// config refuses SMS_PROVIDER=console when APP_ENV=production.
type Console struct {
	logger *slog.Logger
}

// NewConsole returns a sender that logs to logger.
func NewConsole(logger *slog.Logger) *Console {
	return &Console{logger: logger}
}

// SendOTP logs the code (the number stays masked).
func (c *Console) SendOTP(ctx context.Context, to shared.PhoneNumber, code domain.OTPCode) error {
	c.logger.WarnContext(ctx, "DEVELOPMENT SMS (never in production)",
		slog.Any("to", to), // masked by PhoneNumber.LogValue
		slog.String("code", code.Digits()),
	)
	return nil
}
