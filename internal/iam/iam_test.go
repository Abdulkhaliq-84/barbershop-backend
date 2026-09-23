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
		OTPSecret: []byte("test-only-secret-0123456789abcdef-xyz"),
	})
	if err != nil {
		t.Fatal(err)
	}
	router := httpx.NewRouter(logger, httpx.NewHealth(pool, logger))
	if err := httpx.MountAPI(router, apiServer{mod.HTTP()}, logger); err != nil {
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
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.server.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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
			t.Fatalf("POST %s: response is not JSON: %s", path, raw)
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
		{"missing phone", "/v1/auth/otp/request", `{}`, 400, `property "phone" is missing`},
		{"unknown field", "/v1/auth/otp/request", `{"phone":"0551234567","admin":true}`, 400, `"admin"`},
		{"phone too long", "/v1/auth/otp/request", `{"phone":"` + strings.Repeat("5", 65) + `"}`, 400, "phone"},
		{"not JSON", "/v1/auth/otp/request", `phone=055`, 400, ""},
		{"landline", "/v1/auth/otp/request", `{"phone":"0112345678"}`, 422, "phone"},
		{"code with letters", "/v1/auth/otp/verify", `{"phone":"0551234567","code":"12a456"}`, 422, "code"},
		{"code too short", "/v1/auth/otp/verify", `{"phone":"0551234567","code":"123"}`, 400, "code"},
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
