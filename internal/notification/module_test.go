package notification_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// A misconfigured push provider stops the server at startup, without
// quoting the key.
func TestNewChecksPushProvider(t *testing.T) {
	t.Parallel()
	deps := func(p notification.Push) notification.Deps {
		return notification.Deps{Clock: clock.System{}, Logger: slog.New(slog.DiscardHandler), Push: p}
	}
	for _, p := range []notification.Push{{}, {Provider: "console"}} {
		if _, err := notification.New(deps(p)); err != nil {
			t.Errorf("provider %q: %v", p.Provider, err)
		}
	}
	secret := `{"type":"service_account","project_id":"p","client_email":"e","token_uri":"https://oauth2.googleapis.com/token","private_key":"-----BEGIN PRIVATE KEY-----\nc2VjcmV0LWtleS1tYXRlcmlhbA==\n-----END PRIVATE KEY-----\n"}`
	for name, p := range map[string]notification.Push{
		"unknown provider":   {Provider: "pigeon"},
		"fcm with no key":    {Provider: "fcm", Timeout: time.Second},
		"fcm with a bad key": {Provider: "fcm", Credentials: []byte(secret), Timeout: time.Second},
	} {
		_, err := notification.New(deps(p))
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "c2VjcmV0") {
			t.Errorf("%s: the key is quoted: %v", name, err)
		}
	}
}
