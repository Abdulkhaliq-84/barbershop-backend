// Package push delivers notifications to devices. Only a development sender
// exists so far; FCM arrives in M7.2.
package push

import (
	"context"
	"log/slog"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
)

// Console "sends" pushes by writing them to the log, so you can see them
// locally. The device's token is never logged (domain.Token redacts itself).
//
// DEVELOPMENT ONLY: main uses it only outside production.
type Console struct {
	logger *slog.Logger
}

// NewConsole returns a sender that logs to logger.
func NewConsole(logger *slog.Logger) *Console { return &Console{logger: logger} }

// Send logs the push.
func (c *Console) Send(ctx context.Context, p app.Push) error {
	c.logger.InfoContext(ctx, "DEVELOPMENT PUSH (never in production)",
		slog.String("device_id", p.Device.ID.String()),
		slog.String("platform", string(p.Device.Platform)),
		slog.String("locale", string(p.Device.Locale)),
		slog.String("kind", p.Data["kind"]),
		slog.String("title", p.Title),
		slog.String("body", p.Body),
	)
	return nil
}
