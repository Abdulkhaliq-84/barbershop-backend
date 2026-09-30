// Package invites makes invitation tokens and delivers invitations. Only a
// development sender exists so far; real SMS arrives with notifications (M7).
package invites

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/url"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var (
	_ app.InvitationTokens = Tokens{}
	_ app.InvitationSender = (*Console)(nil)
)

// tokenPrefix makes a leaked token recognisable (to people and to secret
// scanners), like "rt_" on refresh tokens.
const tokenPrefix = "inv_"

// Tokens makes 256-bit random tokens and stores their SHA-256. A plain hash
// is enough: the token is random, so there is nothing to guess.
type Tokens struct{}

// New returns a fresh token and its hash.
func (Tokens) New() (string, []byte, error) {
	var b [32]byte
	rand.Read(b[:]) // never fails: crypto/rand crashes the program instead of returning an error
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, Tokens{}.Hash(token), nil
}

// Hash returns the SHA-256 of token.
func (Tokens) Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Link is the deep link the app opens to accept an invitation.
func Link(token string) string {
	return "barbershop://invitations/accept?token=" + url.QueryEscape(token)
}

// Console "sends" invitations by writing the link to the log, so you can
// accept one locally.
//
// DEVELOPMENT ONLY, like the iam console sender: it is used only with
// SMS_PROVIDER=console, which the config refuses when APP_ENV=production.
type Console struct {
	logger *slog.Logger
}

// NewConsole returns a sender that logs to logger.
func NewConsole(logger *slog.Logger) *Console { return &Console{logger: logger} }

// SendInvitation logs the link (the number stays masked).
func (c *Console) SendInvitation(ctx context.Context, to shared.PhoneNumber, business shared.LocalizedText, token string) error {
	c.logger.WarnContext(ctx, "DEVELOPMENT SMS (never in production)",
		slog.Any("to", to), // masked by PhoneNumber.LogValue
		slog.String("business", business.Ar()),
		slog.String("invitation_link", Link(token)),
	)
	return nil
}
