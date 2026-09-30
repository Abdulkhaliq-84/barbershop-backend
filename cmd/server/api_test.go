package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	cfg := config.Config{
		Auth: config.Auth{
			OTPSecret:   "test-only-otp-secret-0123456789abcdef",
			TokenSecret: "test-only-token-secret-0123456789abcdef",
		},
		Media: config.Media{Dir: t.TempDir(), SigningSecret: "test-only-media-secret-0123456789abcdef"},
	}
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

const branchJSON = `{"name":{"ar":"فرع العليا","en":"Olaya"},"city_code":"riyadh","district":"العليا","address":"شارع العليا العام","location":{"latitude":24.6911,"longitude":46.6851},"phone":"0551234567"}`

// register signs up a user with a business and returns the token and the
// business path.
func (a *api) register(t *testing.T, phone, cr string) (token, path string) {
	t.Helper()
	token = a.signIn(t, phone)
	r := a.do(t, http.MethodPost, "/v1/businesses", token, strings.Replace(registration, "١٠١٠ ١٢٣ ٤٥٦", cr, 1))
	if r.status != http.StatusCreated {
		t.Fatalf("register: %d %v", r.status, r.body)
	}
	return token, "/v1/businesses/" + r.body["id"].(string)
}

func TestBranchesAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")

	// Create: draft, Riyadh time, default booking policy.
	r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON)
	if r.status != http.StatusCreated || r.body["status"] != "draft" || r.body["timezone"] != "Asia/Riyadh" ||
		r.body["phone"] != "+966551234567" || r.body["version"] != float64(1) {
		t.Fatalf("create: %d %v", r.status, r.body)
	}
	policy, _ := r.body["booking_policy"].(map[string]any)
	if policy["slot_interval_minutes"] != float64(15) || policy["min_lead_minutes"] != float64(30) || policy["auto_confirm"] != true {
		t.Errorf("default policy = %v", policy)
	}
	branch := biz + "/branches/" + r.body["id"].(string)

	// A second branch with its own policy.
	own := strings.Replace(branchJSON, `"phone":"0551234567"`, `"booking_policy":{"min_lead_minutes":60,"horizon_days":14,"slot_interval_minutes":30,"buffer_minutes":5,"cancellation_window_minutes":180,"auto_confirm":false,"pending_expiry_minutes":30,"max_active_bookings":1}`, 1)
	if r := a.do(t, http.MethodPost, biz+"/branches", owner, own); r.status != http.StatusCreated || r.body["phone"] != nil {
		t.Fatalf("create with policy: %d %v", r.status, r.body)
	}

	// List and get.
	r = a.do(t, http.MethodGet, biz+"/branches", owner, "")
	if data, _ := r.body["data"].([]any); r.status != http.StatusOK || len(data) != 2 {
		t.Fatalf("list: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, branch, owner, ""); r.status != http.StatusOK || r.body["address"] != "شارع العليا العام" {
		t.Fatalf("get: %d %v", r.status, r.body)
	}

	// Edit: move it, clear the phone, change the policy.
	r = a.do(t, http.MethodPatch, branch, owner, `{"location":{"latitude":24.7136,"longitude":46.6753},"phone":"","booking_policy":{"min_lead_minutes":0,"horizon_days":7,"slot_interval_minutes":10,"buffer_minutes":0,"cancellation_window_minutes":60,"auto_confirm":true,"pending_expiry_minutes":15,"max_active_bookings":3}}`, "If-Match", `"1"`)
	if r.status != http.StatusOK || r.body["version"] != float64(2) || r.body["phone"] != nil {
		t.Fatalf("patch: %d %v", r.status, r.body)
	}
	if loc, _ := r.body["location"].(map[string]any); loc["latitude"] != 24.7136 {
		t.Errorf("location = %v", loc)
	}
	if r := a.do(t, http.MethodPatch, branch, owner, `{"address":"x"}`, "If-Match", "1"); r.status != http.StatusPreconditionFailed || r.body["code"] != "version_conflict" {
		t.Errorf("stale version: %d %v", r.status, r.body)
	}

	// Invalid input.
	for name, tt := range map[string]struct {
		body   string
		status int
	}{
		"city in capitals":   {strings.Replace(branchJSON, `"riyadh"`, `"Riyadh"`, 1), http.StatusBadRequest},
		"no location":        {strings.Replace(branchJSON, `"location":{"latitude":24.6911,"longitude":46.6851},`, "", 1), http.StatusBadRequest},
		"latitude off globe": {strings.Replace(branchJSON, "24.6911", "124.6911", 1), http.StatusBadRequest},
		"slot every 7 min":   {strings.Replace(own, `"slot_interval_minutes":30`, `"slot_interval_minutes":7`, 1), http.StatusBadRequest},
		"unknown time zone":  {strings.Replace(branchJSON, `"phone"`, `"timezone":"Asia/Atlantis","phone"`, 1), http.StatusUnprocessableEntity},
		"landline phone":     {strings.Replace(branchJSON, "0551234567", "0114567890", 1), http.StatusUnprocessableEntity},
		"blank address":      {strings.Replace(branchJSON, "شارع العليا العام", "  ", 1), http.StatusUnprocessableEntity},
		"no location (0, 0)": {strings.Replace(strings.Replace(branchJSON, "24.6911", "0", 1), "46.6851", "0", 1), http.StatusUnprocessableEntity},
	} {
		if r := a.do(t, http.MethodPost, biz+"/branches", owner, tt.body); r.status != tt.status || r.body["code"] != "validation_failed" {
			t.Errorf("%s: %d %v, want %d", name, r.status, r.body, tt.status)
		}
	}
}

// Tenant isolation through the front door: another business's owner can't
// see, list, add to or edit this business's branches — not even by putting
// this branch's ID under their own business.
func TestBranchesAreIsolatedBetweenBusinesses(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	other, otherBiz := a.register(t, "0559876543", "1010000002")
	r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %v", r.status, r.body)
	}
	id := r.body["id"].(string)

	for name, req := range map[string]struct{ method, path, body string }{
		"get":                     {http.MethodGet, biz + "/branches/" + id, ""},
		"list":                    {http.MethodGet, biz + "/branches", ""},
		"create":                  {http.MethodPost, biz + "/branches", branchJSON},
		"edit":                    {http.MethodPatch, biz + "/branches/" + id, `{"address":"مختطف"}`},
		"get under own business":  {http.MethodGet, otherBiz + "/branches/" + id, ""},
		"edit under own business": {http.MethodPatch, otherBiz + "/branches/" + id, `{"address":"مختطف"}`},
	} {
		if r := a.do(t, req.method, req.path, other, req.body, "If-Match", "1"); r.status != http.StatusNotFound || r.body["code"] != "not_found" {
			t.Errorf("%s: %d %v, want 404", name, r.status, r.body)
		}
	}
	if r := a.do(t, http.MethodGet, biz+"/branches/"+id, owner, ""); r.body["version"] != float64(1) || r.body["address"] != "شارع العليا العام" {
		t.Fatalf("the branch changed: %v", r.body)
	}
}

// raw sends a request and returns the undecoded response (downloads aren't JSON).
func (a *api) raw(t *testing.T, method, path, token, contentType string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, a.url+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, data
}

// upload posts a file as a verification document.
func (a *api) upload(t *testing.T, biz, token string, file []byte) response {
	t.Helper()
	status, headers, data := a.raw(t, http.MethodPost, biz+"/verification/documents?kind=cr_certificate", token, "application/octet-stream", file)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("upload: response is not JSON: %s", data)
	}
	return response{status: status, headers: headers, body: body}
}

func pdfFile(size int) []byte {
	b := bytes.Repeat([]byte{'x'}, size)
	copy(b, "%PDF-1.7\n")
	return b
}

func TestVerificationDocumentsAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	file := pdfFile(50_000)

	// Upload: stored, typed from the content, with a signed link.
	r := a.upload(t, biz, owner, file)
	if r.status != http.StatusCreated || r.body["content_type"] != "application/pdf" || r.body["size_bytes"] != float64(50_000) || r.body["kind"] != "cr_certificate" {
		t.Fatalf("upload: %d %v", r.status, r.body)
	}
	link, _ := r.body["download_url"].(string)

	// The link downloads exactly the bytes, as a no-store attachment.
	status, headers, data := a.raw(t, http.MethodGet, link, "", "", nil)
	if status != http.StatusOK || !bytes.Equal(data, file) {
		t.Fatalf("download: %d, %d bytes", status, len(data))
	}
	if headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") ||
		headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("Cache-Control") != "private, no-store" {
		t.Errorf("download headers: %v", headers)
	}

	// A tampered link opens nothing: another expiry, another signature, another file.
	u, _ := url.Parse(link)
	q := u.Query()
	for name, bad := range map[string]string{
		"later expiry":    strings.Replace(link, "expires="+q.Get("expires"), "expires=9999999999", 1),
		"other signature": strings.Replace(link, "signature="+q.Get("signature"), "signature=AAAA", 1),
		"no signature":    u.Path + "?expires=" + q.Get("expires") + "&signature=x",
	} {
		if status, _, data := a.raw(t, http.MethodGet, bad, "", "", nil); status != http.StatusForbidden || !bytes.Contains(data, []byte("download_link_invalid")) {
			t.Errorf("%s: %d %s", name, status, data)
		}
	}
	other := a.upload(t, biz, owner, pdfFile(100))
	otherID, _ := other.body["id"].(string)
	if status, _, _ := a.raw(t, http.MethodGet, strings.Replace(link, u.Path, "/v1/media/"+otherID, 1), "", "", nil); status != http.StatusForbidden {
		t.Errorf("this link for another file: %d", status)
	}

	// The owner lists them with fresh links; a stranger learns nothing.
	r = a.do(t, http.MethodGet, biz+"/verification/documents", owner, "")
	if data, _ := r.body["data"].([]any); r.status != http.StatusOK || len(data) != 2 {
		t.Fatalf("list: %d %v", r.status, r.body)
	}
	stranger := a.signIn(t, "0559876543")
	if r := a.do(t, http.MethodGet, biz+"/verification/documents", stranger, ""); r.status != http.StatusNotFound {
		t.Errorf("stranger list: %d %v", r.status, r.body)
	}
	if r := a.upload(t, biz, stranger, file); r.status != http.StatusNotFound {
		t.Errorf("stranger upload: %d %v", r.status, r.body)
	}
	if r := a.upload(t, biz, "", file); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous upload: %d %v", r.status, r.body)
	}
}

func TestVerificationDocumentUploadRejects(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")

	tests := []struct {
		name   string
		file   []byte
		status int
		code   string
	}{
		{"html page named cr.pdf", []byte("<html><script>alert(1)</script></html>"), http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"windows program", append([]byte("MZ"), make([]byte, 100)...), http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"over 10 MiB", pdfFile(10<<20 + 1), http.StatusRequestEntityTooLarge, "payload_too_large"},
		{"way over 10 MiB", pdfFile(12 << 20), http.StatusRequestEntityTooLarge, "payload_too_large"},
	}
	for _, tt := range tests {
		if r := a.upload(t, biz, owner, tt.file); r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", tt.name, r.status, r.body, tt.status, tt.code)
		}
	}
	// Exactly 10 MiB is fine; then fill up to the limit of five.
	if r := a.upload(t, biz, owner, pdfFile(10<<20)); r.status != http.StatusCreated {
		t.Fatalf("10 MiB: %d %v", r.status, r.body)
	}
	for range 4 {
		if r := a.upload(t, biz, owner, pdfFile(10)); r.status != http.StatusCreated {
			t.Fatalf("upload: %d %v", r.status, r.body)
		}
	}
	if r := a.upload(t, biz, owner, pdfFile(10)); r.status != http.StatusConflict || r.body["code"] != "document_limit_reached" {
		t.Errorf("sixth document: %d %v", r.status, r.body)
	}
	// JSON is not a file.
	if status, _, data := a.raw(t, http.MethodPost, biz+"/verification/documents?kind=cr_certificate", owner, "application/json", []byte(`{}`)); status != http.StatusBadRequest {
		t.Errorf("json body: %d %s", status, data)
	}
}
