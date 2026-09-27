package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
)

// These tests drive the server exactly as main wires it — every module,
// real router, spec validation and PostgreSQL — through HTTP only. They
// check what no single module can: that the modules work together (an iam
// access token opens business-mode routes).

// logBuffer collects the JSON log lines; the development SMS sender writes
// login codes there.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lastCode returns the most recent development SMS code.
func (b *logBuffer) lastCode(t *testing.T) string {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	code := ""
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var line struct{ Code string }
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Code != "" {
			code = line.Code
		}
	}
	if code == "" {
		t.Fatal("no login code in the log")
	}
	return code
}

type api struct {
	url  string
	logs *logBuffer
}

func newAPI(t *testing.T) *api {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	logs := &logBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	if err := database.Migrate(t.Context(), pool, logger); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Auth: config.Auth{
		OTPSecret:   "test-only-otp-secret-0123456789abcdef",
		TokenSecret: "test-only-token-secret-0123456789abcdef",
	}}
	handler, err := newHandler(cfg, pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &api{url: srv.URL, logs: logs}
}

type response struct {
	status  int
	headers http.Header
	body    map[string]any
}

func (a *api) do(t *testing.T, method, path, token, body string, headers ...string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, a.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

// signIn logs phone in and returns an access token.
func (a *api) signIn(t *testing.T, phone string) string {
	t.Helper()
	if r := a.do(t, http.MethodPost, "/v1/auth/otp/request", "", `{"phone":"`+phone+`"}`); r.status != http.StatusAccepted {
		t.Fatalf("otp request: %d %v", r.status, r.body)
	}
	r := a.do(t, http.MethodPost, "/v1/auth/otp/verify", "", `{"phone":"`+phone+`","code":"`+a.logs.lastCode(t)+`"}`)
	if r.status != http.StatusOK {
		t.Fatalf("otp verify: %d %v", r.status, r.body)
	}
	tokens, _ := r.body["tokens"].(map[string]any)
	token, _ := tokens["access_token"].(string)
	return token
}

func memberships(t *testing.T, r response) []any {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("memberships: %d %v", r.status, r.body)
	}
	data, ok := r.body["data"].([]any)
	if !ok {
		t.Fatalf("memberships: no data array in %v", r.body)
	}
	return data
}

const registration = `{"display_name":{"ar":"صالون الأناقة","en":"Elegance Barbers"},"legal_name":"مؤسسة الأناقة للحلاقة","cr_number":"١٠١٠ ١٢٣ ٤٥٦"}`

func TestBusinessOnboardingAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner := a.signIn(t, "0551234567")

	// A new user is only a customer.
	if data := memberships(t, a.do(t, http.MethodGet, "/v1/me/memberships", owner, "")); len(data) != 0 {
		t.Fatalf("new user's memberships = %v", data)
	}

	// Register: a draft owned by the caller, CR number normalised.
	r := a.do(t, http.MethodPost, "/v1/businesses", owner, registration)
	if r.status != http.StatusCreated || r.body["status"] != "draft" || r.body["cr_number"] != "1010123456" ||
		r.body["version"] != float64(1) || r.body["legal_name"] != "مؤسسة الأناقة للحلاقة" {
		t.Fatalf("register: %d %v", r.status, r.body)
	}
	id, _ := r.body["id"].(string)
	if name, _ := r.body["display_name"].(map[string]any); name["ar"] != "صالون الأناقة" || name["en"] != "Elegance Barbers" {
		t.Errorf("display_name = %v", r.body["display_name"])
	}

	// A retry is refused, pointing at the existing business.
	if r := a.do(t, http.MethodPost, "/v1/businesses", owner, registration); r.status != http.StatusConflict || r.body["code"] != "business_already_registered" {
		t.Fatalf("retry: %d %v", r.status, r.body)
	}

	// The owner sees it, and the business-mode switch shows it.
	if r := a.do(t, http.MethodGet, "/v1/businesses/"+id, owner, ""); r.status != http.StatusOK || r.body["id"] != id {
		t.Fatalf("get: %d %v", r.status, r.body)
	}
	data := memberships(t, a.do(t, http.MethodGet, "/v1/me/memberships", owner, ""))
	if len(data) != 1 {
		t.Fatalf("memberships = %v", data)
	}
	m, _ := data[0].(map[string]any)
	biz, _ := m["business"].(map[string]any)
	if m["role"] != "owner" || biz["id"] != id || biz["status"] != "draft" || m["staff_id"] == nil {
		t.Errorf("membership = %v", m)
	}

	// Another user can't tell the business exists…
	stranger := a.signIn(t, "0559876543")
	for _, path := range []string{"/v1/businesses/" + id, "/v1/businesses/0190a1b2-c3d4-7e5f-8a6b-7c8d9e0f1a2b"} {
		if r := a.do(t, http.MethodGet, path, stranger, ""); r.status != http.StatusNotFound || r.body["code"] != "not_found" {
			t.Errorf("stranger GET %s: %d %v", path, r.status, r.body)
		}
	}
	if data := memberships(t, a.do(t, http.MethodGet, "/v1/me/memberships", stranger, "")); len(data) != 0 {
		t.Errorf("stranger's memberships = %v", data)
	}
	// …and may register the same CR number as their own draft (ADR-0015).
	if r := a.do(t, http.MethodPost, "/v1/businesses", stranger, registration); r.status != http.StatusCreated || r.body["id"] == id {
		t.Errorf("stranger's draft: %d %v", r.status, r.body)
	}
}

func TestBusinessAPIRejects(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	token := a.signIn(t, "0551234567")

	tests := []struct {
		name, method, path, token, body string
		status                          int
		code                            string
	}{
		{"no token", http.MethodPost, "/v1/businesses", "", registration, http.StatusUnauthorized, "unauthorized"},
		{"bad token", http.MethodGet, "/v1/me/memberships", "not-a-token", "", http.StatusUnauthorized, "unauthorized"},
		{"malformed id", http.MethodGet, "/v1/businesses/42", token, "", http.StatusBadRequest, "validation_failed"},
		{"unknown field", http.MethodPost, "/v1/businesses", token, strings.Replace(registration, `"legal_name"`, `"status":"active","legal_name"`, 1), http.StatusBadRequest, "validation_failed"},
		{"no arabic name", http.MethodPost, "/v1/businesses", token, strings.Replace(registration, `"ar":"صالون الأناقة",`, "", 1), http.StatusBadRequest, "validation_failed"},
		{"short cr number", http.MethodPost, "/v1/businesses", token, strings.Replace(registration, "١٠١٠ ١٢٣ ٤٥٦", "12345", 1), http.StatusBadRequest, "validation_failed"},
		{"letters in cr number", http.MethodPost, "/v1/businesses", token, strings.Replace(registration, "١٠١٠ ١٢٣ ٤٥٦", "10101234AB", 1), http.StatusUnprocessableEntity, "validation_failed"},
		{"blank legal name", http.MethodPost, "/v1/businesses", token, strings.Replace(registration, "مؤسسة الأناقة للحلاقة", "   ", 1), http.StatusUnprocessableEntity, "validation_failed"},
	}
	for _, tt := range tests {
		r := a.do(t, tt.method, tt.path, tt.token, tt.body)
		if r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", tt.name, r.status, r.body, tt.status, tt.code)
		}
		if tt.status == http.StatusUnauthorized && r.headers.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate challenge", tt.name)
		}
	}
	if data := memberships(t, a.do(t, http.MethodGet, "/v1/me/memberships", token, "")); len(data) != 0 {
		t.Errorf("a refused registration left a membership: %v", data)
	}
}

func TestUpdateBusinessAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner := a.signIn(t, "0551234567")
	r := a.do(t, http.MethodPost, "/v1/businesses", owner, registration)
	if r.status != http.StatusCreated {
		t.Fatalf("register: %d %v", r.status, r.body)
	}
	path := "/v1/businesses/" + r.body["id"].(string)
	patch := func(token, body string, headers ...string) response {
		t.Helper()
		return a.do(t, http.MethodPatch, path, token, body, headers...)
	}

	// The owner renames, sending the version they read (ETag form).
	r = patch(owner, `{"legal_name":"مؤسسة الفخامة"}`, "If-Match", `"1"`)
	if r.status != http.StatusOK || r.body["legal_name"] != "مؤسسة الفخامة" || r.body["version"] != float64(2) {
		t.Fatalf("rename: %d %v", r.status, r.body)
	}
	if name, _ := r.body["display_name"].(map[string]any); name["en"] != "Elegance Barbers" {
		t.Errorf("display_name changed: %v", r.body["display_name"])
	}

	// Version 1 is stale now: a second phone's edit is refused, not merged.
	if r := patch(owner, `{"display_name":{"ar":"صالون الفخامة"}}`, "If-Match", "1"); r.status != http.StatusPreconditionFailed || r.body["code"] != "version_conflict" {
		t.Fatalf("stale version: %d %v", r.status, r.body)
	}
	// Replacing display_name without "en" clears the English name.
	r = patch(owner, `{"display_name":{"ar":"صالون الفخامة"}}`, "If-Match", "2")
	if name, _ := r.body["display_name"].(map[string]any); r.status != http.StatusOK || name["ar"] != "صالون الفخامة" || name["en"] != nil {
		t.Fatalf("display name: %d %v", r.status, r.body)
	}

	// A stranger gets 404, like for a business that doesn't exist.
	stranger := a.signIn(t, "0559876543")
	if r := patch(stranger, `{"legal_name":"مختطف"}`, "If-Match", "3"); r.status != http.StatusNotFound || r.body["code"] != "not_found" {
		t.Errorf("stranger: %d %v", r.status, r.body)
	}

	tests := []struct {
		name, body string
		headers    []string
		status     int
	}{
		{"no If-Match", `{"legal_name":"x"}`, nil, http.StatusBadRequest},
		{"If-Match not a number", `{"legal_name":"x"}`, []string{"If-Match", "*"}, http.StatusBadRequest},
		{"empty body", `{}`, []string{"If-Match", "3"}, http.StatusBadRequest},
		{"unknown field", `{"status":"active"}`, []string{"If-Match", "3"}, http.StatusBadRequest},
		{"blank legal name", `{"legal_name":"  "}`, []string{"If-Match", "3"}, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		if r := patch(owner, tt.body, tt.headers...); r.status != tt.status || r.body["code"] != "validation_failed" {
			t.Errorf("%s: %d %v, want %d validation_failed", tt.name, r.status, r.body, tt.status)
		}
	}

	// Nothing refused was saved.
	if r := a.do(t, http.MethodGet, path, owner, ""); r.body["version"] != float64(3) || r.body["legal_name"] != "مؤسسة الفخامة" {
		t.Fatalf("after refusals: %v", r.body)
	}
}
