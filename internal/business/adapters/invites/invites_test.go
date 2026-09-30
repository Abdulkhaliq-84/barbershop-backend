package invites_test

import (
	"bytes"
	"crypto/sha256"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/invites"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestTokens(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		token, hash, err := invites.Tokens{}.New()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(token))
		if !strings.HasPrefix(token, "inv_") || len(token) != 4+43 || !bytes.Equal(hash, sum[:]) || !bytes.Equal(invites.Tokens{}.Hash(token), hash) {
			t.Fatalf("token %q, hash %x", token, hash)
		}
		if seen[token] {
			t.Fatal("repeated token")
		}
		seen[token] = true
	}
}

func TestConsoleMasksThePhone(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := invites.NewConsole(slog.New(slog.NewJSONHandler(&buf, nil)))
	phone, _ := shared.NewPhoneNumber("0551234567")
	name, _ := shared.NewLocalizedText("صالون", "")
	if err := c.SendInvitation(t.Context(), phone, name, "inv_abc"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "551234567") || !strings.Contains(out, url.QueryEscape("inv_abc")) {
		t.Errorf("log = %s", out)
	}
}
