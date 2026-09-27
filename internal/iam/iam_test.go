package iam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// End-to-end: real router, request validation, handlers, use cases and
// PostgreSQL — only the SMS sender and the clock are swapped for test doubles.

type inbox struct {
	mu    sync.Mutex
	codes map[string]string // phone → last code
}

func (i *inbox) SendOTP(_ context.Context, to shared.PhoneNumber, code domain.OTPCode) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.codes[to.String()] = code.Digits()
	return nil
}

func (i *inbox) last(phone string) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.codes[phone]
}

type apiServer struct{ *httpapi.Handlers }

type env struct {
	server *httptest.Server
	inbox  *inbox
	clock  *clock.Fake
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	logger := slog.New(slog.DiscardHandler)
	if err := database.Migrate(t.Context(), pool, logger); err != nil {
		t.Fatal(err)
	}
	e := &env{inbox: &inbox{codes: map[string]string{}}, clock: clock.NewFake(time.Now())}
	mod, err := iam.New(iam.Deps{
		Pool: pool, Clock: e.clock, Logger: logger, OTPSender: e.inbox,
		OTPSecret:   []byte("test-only-secret-0123456789abcdef-xyz"),
		TokenSecret: []byte("test-only-token-secret-0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	router := httpx.NewRouter(logger, httpx.NewHealth(pool, logger))
	if err := httpx.MountAPI(router, apiServer{mod.HTTP()}, logger, mod.Authenticate); err != nil {
		t.Fatal(err)
	}
	e.server = httptest.NewServer(router)
	t.Cleanup(e.server.Close)
	return e
}

type response struct {
	status  int
	headers http.Header
	body    map[string]any
}

func (e *env) post(t *testing.T, path, body string, headers ...string) response {
	t.Helper()
	return e.do(t, http.MethodPost, path, body, headers...)
}

func (e *env) get(t *testing.T, path string, headers ...string) response {
	t.Helper()
	return e.do(t, http.MethodGet, path, "", headers...)
}

func (e *env) do(t *testing.T, method, path, body string, headers ...string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, e.server.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("%s %s: response is not JSON: %s", method, path, raw)
		}
	}
	return response{status: resp.StatusCode, headers: resp.Header, body: parsed}
}

func TestLoginFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// 1. Request a code.
	r := e.post(t, "/v1/auth/otp/request", `{"phone":"055 123 4567"}`)
	if r.status != http.StatusAccepted || r.body["expires_in_seconds"] != float64(300) || r.body["resend_after_seconds"] != float64(60) {
		t.Fatalf("request: %d %v", r.status, r.body)
	}
	code := e.inbox.last("+966551234567")
	if len(code) != 6 {
		t.Fatalf("no code delivered: %q", code)
	}

	// 2. Asking again right away hits the cooldown.
	r = e.post(t, "/v1/auth/otp/request", `{"phone":"0551234567"}`)
	if r.status != http.StatusTooManyRequests || r.body["code"] != "otp_cooldown" || r.headers.Get("Retry-After") != "60" {
		t.Fatalf("cooldown: %d %v Retry-After=%q", r.status, r.body, r.headers.Get("Retry-After"))
	}

	// 3. A wrong code is rejected.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	r = e.post(t, "/v1/auth/otp/verify", `{"phone":"0551234567","code":"`+wrong+`"}`)
	if r.status != http.StatusUnauthorized || r.body["code"] != "otp_invalid" {
		t.Fatalf("wrong code: %d %v", r.status, r.body)
	}

	// 4. The right code signs in and registers the number.
	r = e.post(t, "/v1/auth/otp/verify", `{"phone":"+966551234567","code":"`+code+`"}`, "Accept-Language", "en-US")
	if r.status != http.StatusOK || r.body["is_new_user"] != true {
		t.Fatalf("verify: %d %v", r.status, r.body)
	}
	user, _ := r.body["user"].(map[string]any)
	if user["phone"] != "+966551234567" || user["locale"] != "en" || user["id"] == "" {
		t.Errorf("user = %v", user)
	}

	// 5. A used code doesn't work twice.
	r = e.post(t, "/v1/auth/otp/verify", `{"phone":"0551234567","code":"`+code+`"}`)
	if r.status != http.StatusUnauthorized || r.body["code"] != "otp_invalid" {
		t.Fatalf("reused code: %d %v", r.status, r.body)
	}

	// 6. Next login: same user, not new.
	e.clock.Advance(2 * time.Minute)
	if r = e.post(t, "/v1/auth/otp/request", `{"phone":"0551234567"}`); r.status != http.StatusAccepted {
		t.Fatalf("second request: %d %v", r.status, r.body)
	}
	r = e.post(t, "/v1/auth/otp/verify", `{"phone":"0551234567","code":"`+e.inbox.last("+966551234567")+`"}`)
	again, _ := r.body["user"].(map[string]any)
	if r.status != http.StatusOK || r.body["is_new_user"] != false || again["id"] != user["id"] {
		t.Fatalf("second login: %d %v", r.status, r.body)
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	tests := []struct {
		name, path, body string
		wantStatus       int
		wantDetail       string
	}{
		{"missing phone", "/v1/auth/otp/request", `{}`, 400, "API specification"},
		{"unknown field", "/v1/auth/otp/request", `{"phone":"0551234567","admin":true}`, 400, "API specification"},
		{"phone too long", "/v1/auth/otp/request", `{"phone":"` + strings.Repeat("5", 65) + `"}`, 400, "API specification"},
		{"not JSON", "/v1/auth/otp/request", `phone=055`, 400, ""},
		{"landline", "/v1/auth/otp/request", `{"phone":"0112345678"}`, 422, "phone"},
		{"code with letters", "/v1/auth/otp/verify", `{"phone":"0551234567","code":"12a456"}`, 422, "code"},
		{"code too short", "/v1/auth/otp/verify", `{"phone":"0551234567","code":"123"}`, 400, "API specification"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := e.post(t, tt.path, tt.body)
			if r.status != tt.wantStatus || r.body["code"] != "validation_failed" {
				t.Fatalf("got %d %v, want %d validation_failed", r.status, r.body, tt.wantStatus)
			}
			detail, _ := r.body["detail"].(string)
			if !strings.Contains(detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", detail, tt.wantDetail)
			}
			if strings.Contains(detail, "0112345678") || strings.Contains(detail, "0551234567") {
				t.Errorf("detail echoes the phone number: %q", detail)
			}
		})
	}
}

func TestHTTPPhoneLockout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// Unknown phones also count failed verification, without revealing registration.
	for i := range 10 {
		r := e.post(t, "/v1/auth/otp/verify", `{"phone":"0551234567","code":"000000"}`)
		if i < 9 && r.status != 401 {
			t.Fatalf("failure %d: %v", i, r)
		}
		if i == 9 && (r.status != 429 || r.body["code"] != "otp_locked" || r.headers.Get("Retry-After") != "900") {
			t.Fatalf("lockout: %v", r)
		}
	}
	r := e.post(t, "/v1/auth/otp/request", `{"phone":"+966551234567"}`)
	if r.status != 429 || r.body["code"] != "otp_locked" {
		t.Fatalf("locked resend: %v", r)
	}
	e.clock.Advance(15 * time.Minute)
	r = e.post(t, "/v1/auth/otp/request", `{"phone":"0551234567"}`)
	if r.status != 202 {
		t.Fatalf("after expiry: %v", r)
	}
}

func TestValidationNeverEchoesUnknownKeys(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, body := range []string{
		`{"phone":"0551234567","+966551234567-private":true}`,
		`{"phone":{"+966551234567-private":true}}`,
	} {
		r := e.post(t, "/v1/auth/otp/request", body)
		detail, _ := r.body["detail"].(string)
		if r.status != 400 || strings.Contains(detail, "551234567") || strings.Contains(detail, "private") {
			t.Fatalf("unsafe validation response: %v", r)
		}
	}
}

// signIn logs a number in and returns the access and refresh tokens.
func (e *env) signIn(t *testing.T, phone string) (access, refresh string) {
	t.Helper()
	if r := e.post(t, "/v1/auth/otp/request", `{"phone":"`+phone+`"}`); r.status != http.StatusAccepted {
		t.Fatalf("request code: %d %v", r.status, r.body)
	}
	e.clock.Advance(2 * time.Minute) // the next sign-in of this number is past the resend cooldown
	p, err := shared.NewPhoneNumber(phone)
	if err != nil {
		t.Fatal(err)
	}
	r := e.post(t, "/v1/auth/otp/verify", `{"phone":"`+phone+`","code":"`+e.inbox.last(p.String())+`"}`)
	tokens, _ := r.body["tokens"].(map[string]any)
	access, _ = tokens["access_token"].(string)
	refresh, _ = tokens["refresh_token"].(string)
	if r.status != http.StatusOK || access == "" || refresh == "" || tokens["token_type"] != "Bearer" ||
		tokens["expires_in"] != float64(900) || tokens["refresh_expires_in"] != float64(30*24*3600) {
		t.Fatalf("verify: %d %v", r.status, r.body)
	}
	return access, refresh
}

func bearer(token string) []string { return []string{"Authorization", "Bearer " + token} }

func TestSessionFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	access, refresh := e.signIn(t, "0551234567")

	// 1. The access token opens /v1/me.
	r := e.get(t, "/v1/me", bearer(access)...)
	if r.status != http.StatusOK || r.body["phone"] != "+966551234567" {
		t.Fatalf("GET /v1/me: %d %v", r.status, r.body)
	}

	// 2. Refresh: a new pair, and the old refresh token is spent.
	r = e.post(t, "/v1/auth/refresh", `{"refresh_token":"`+refresh+`"}`)
	access2, _ := r.body["access_token"].(string)
	refresh2, _ := r.body["refresh_token"].(string)
	if r.status != http.StatusOK || access2 == "" || refresh2 == "" || refresh2 == refresh {
		t.Fatalf("refresh: %d %v", r.status, r.body)
	}
	if r = e.get(t, "/v1/me", bearer(access2)...); r.status != http.StatusOK {
		t.Fatalf("GET /v1/me with the refreshed token: %d %v", r.status, r.body)
	}

	// 3. The spent token comes back: it was copied. The session ends…
	r = e.post(t, "/v1/auth/refresh", `{"refresh_token":"`+refresh+`"}`)
	if r.status != http.StatusUnauthorized || r.body["code"] != "refresh_token_reused" {
		t.Fatalf("reuse: %d %v", r.status, r.body)
	}
	// …so the newest refresh token is dead as well.
	r = e.post(t, "/v1/auth/refresh", `{"refresh_token":"`+refresh2+`"}`)
	if r.status != http.StatusUnauthorized || r.body["code"] != "refresh_token_invalid" {
		t.Fatalf("after reuse: %d %v", r.status, r.body)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	access, refresh := e.signIn(t, "0551234567")

	for range 2 { // signing out twice is fine
		if r := e.post(t, "/v1/auth/logout", "", bearer(access)...); r.status != http.StatusNoContent {
			t.Fatalf("logout: %d %v", r.status, r.body)
		}
	}
	r := e.post(t, "/v1/auth/refresh", `{"refresh_token":"`+refresh+`"}`)
	if r.status != http.StatusUnauthorized || r.body["code"] != "refresh_token_invalid" {
		t.Fatalf("refresh after logout: %d %v", r.status, r.body)
	}
	// Another sign-in on the same number is a separate session.
	_, other := e.signIn(t, "0551234567")
	if r := e.post(t, "/v1/auth/refresh", `{"refresh_token":"`+other+`"}`); r.status != http.StatusOK {
		t.Fatalf("second session: %d %v", r.status, r.body)
	}
}

func TestProtectedOperationsNeedAValidToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	access, _ := e.signIn(t, "0551234567")

	tests := map[string][]string{
		"no header":      nil,
		"garbage token":  bearer("not-a-token"),
		"wrong scheme":   {"Authorization", "Basic " + access},
		"refresh token":  bearer("rt_" + strings.Repeat("A", 43)),
		"empty bearer":   {"Authorization", "Bearer "},
		"tampered token": bearer(access[:len(access)-2] + "xx"),
	}
	for name, headers := range tests {
		r := e.get(t, "/v1/me", headers...)
		if r.status != http.StatusUnauthorized || r.body["code"] != "unauthorized" || !strings.HasPrefix(r.headers.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: %d %v WWW-Authenticate=%q", name, r.status, r.body, r.headers.Get("WWW-Authenticate"))
		}
	}
	if r := e.post(t, "/v1/auth/logout", ""); r.status != http.StatusUnauthorized {
		t.Errorf("logout without token: %d %v", r.status, r.body)
	}

	// The scheme is case-insensitive.
	if r := e.get(t, "/v1/me", "Authorization", "bearer "+access); r.status != http.StatusOK {
		t.Errorf("lower-case scheme: %d %v", r.status, r.body)
	}

	// Access tokens expire after 15 minutes (plus 30 s allowed clock skew).
	e.clock.Advance(16 * time.Minute)
	if r := e.get(t, "/v1/me", bearer(access)...); r.status != http.StatusUnauthorized {
		t.Errorf("expired token: %d %v", r.status, r.body)
	}

	// Public operations ignore a bad token instead of failing.
	if r := e.post(t, "/v1/auth/otp/request", `{"phone":"0559998888"}`, bearer("garbage")...); r.status != http.StatusAccepted {
		t.Errorf("public operation with a bad token: %d %v", r.status, r.body)
	}
}
