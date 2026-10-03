package push

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// One key for every test: generating RSA keys is slow.
var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

const (
	deviceToken = "dQw4w9WgXcQ:APA91bHun4MxP5egoKMwt2KZFBaFUH-1RYqx_Ln5L6Zc"
	project     = "barbershop-test"
	email       = "pusher@barbershop-test.iam.gserviceaccount.com"
)

// credentials is a service account key as Google issues it (PKCS #8),
// signing in at tokenURI.
func credentials(t *testing.T, tokenURI string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": project, "private_key_id": "key-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": email, "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// google is a fake of Google's token endpoint and FCM. Each answer to a push
// comes from answers in turn (the last one repeats).
type google struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	issued  int      // access tokens handed out
	tokenOK bool     // false: refuse to sign in
	tokenAt int      // status for token requests when !tokenOK
	answers []answer // for pushes
	pushes  []pushed
	block   chan struct{} // if set, pushes wait on it (timeouts)
}

type answer struct {
	status     int
	body       string
	retryAfter string
}

type pushed struct {
	auth string
	path string
	body map[string]any
}

func newGoogle(t *testing.T, answers ...answer) *google {
	t.Helper()
	g := &google{t: t, tokenOK: true, answers: answers}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *google) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/token":
		g.token(w, r)
	default:
		g.push(w, r)
	}
}

func (g *google) token(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		g.t.Errorf("token request: %v, %v", err, r.PostForm)
	}
	// The assertion is signed by the account's key, for this endpoint. (Its
	// times are checked apart: some tests move the sender's clock.)
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims, func(tok *jwt.Token) (any, error) {
		if tok.Method != jwt.SigningMethodRS256 || tok.Header["kid"] != "key-1" {
			return nil, fmt.Errorf("signed with %v, kid %v", tok.Method.Alg(), tok.Header["kid"])
		}
		return &key(g.t).PublicKey, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil || !parsed.Valid || claims["iss"] != email || claims["aud"] != g.srv.URL+"/token" ||
		claims["scope"] != "https://www.googleapis.com/auth/firebase.messaging" {
		g.t.Errorf("assertion: %v, %v", err, claims)
	}
	if iat, exp := claims["iat"].(float64), claims["exp"].(float64); exp-iat != 3600 {
		g.t.Errorf("assertion lasts %vs, want an hour", exp-iat)
	}
	if !g.tokenOK {
		w.WriteHeader(g.tokenAt)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)
		return
	}
	g.issued++
	_, _ = fmt.Fprintf(w, `{"access_token":"ya29.access-%d","expires_in":3599,"token_type":"Bearer"}`, g.issued)
}

func (g *google) push(w http.ResponseWriter, r *http.Request) {
	if g.block != nil {
		select {
		case <-g.block:
		case <-r.Context().Done():
			return
		}
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		g.t.Errorf("push body: %v", err)
	}
	g.mu.Lock()
	g.pushes = append(g.pushes, pushed{auth: r.Header.Get("Authorization"), path: r.URL.Path, body: body})
	a := answer{status: http.StatusOK, body: `{"name":"projects/barbershop-test/messages/1"}`}
	if len(g.answers) > 0 {
		a = g.answers[min(len(g.pushes), len(g.answers))-1]
	}
	g.mu.Unlock()
	if a.retryAfter != "" {
		w.Header().Set("Retry-After", a.retryAfter)
	}
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

func (g *google) count() (tokens, pushes int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.issued, len(g.pushes)
}

// fcmError is FCM's error body for status and errorCode.
func fcmError(code int, status, errorCode string) answer {
	details := ""
	if errorCode != "" {
		details = fmt.Sprintf(`,"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":%q}]`, errorCode)
	}
	return answer{status: code, body: fmt.Sprintf(`{"error":{"code":%d,"message":"Requested entity was not found. %s","status":%q%s}}`, code, deviceToken, status, details)}
}

// sender is an FCM sender talking to g, logging to logs, with pauses
// recorded instead of slept.
func sender(t *testing.T, g *google, logs *bytes.Buffer) (*FCM, *[]time.Duration) {
	t.Helper()
	f, err := NewFCM(FCMConfig{
		Credentials: credentials(t, g.srv.URL+"/token"), Timeout: 2 * time.Second,
		BaseURL: g.srv.URL, Transport: g.srv.Client().Transport,
	}, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	f.retry.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	return f, &slept
}

func pushTo(t *testing.T) app.Push {
	t.Helper()
	tok, err := domain.ParseToken(deviceToken)
	if err != nil {
		t.Fatal(err)
	}
	return app.Push{
		Device: domain.Device{ID: shared.NewID[domain.DeviceTag](), Token: tok, Platform: domain.Android, Locale: shared.Arabic},
		Title:  "تم تأكيد حجزك", Body: "صالون الأناقة، الخميس 1 أكتوبر، 16:30. نراك قريبًا.",
		Data:        map[string]string{"kind": "booking_confirmed", "appointment_id": "a1"},
		CollapseKey: "event-1", // the event's ID, in the app
	}
}

// noSecrets fails if a credential shows in s.
func noSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, secret := range []string{deviceToken, "ya29.", "PRIVATE KEY", "eyJ"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s shows a secret (%q): %s", what, secret, s)
		}
	}
}

func TestFCMSend(t *testing.T) {
	t.Parallel()
	g := newGoogle(t)
	var logs bytes.Buffer
	f, _ := sender(t, g, &logs)
	p := pushTo(t)
	for range 2 {
		if err := f.Send(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	tokens, pushes := g.count()
	if tokens != 1 || pushes != 2 {
		t.Fatalf("%d tokens, %d pushes; want one token for both", tokens, pushes)
	}
	got := g.pushes[0]
	if got.auth != "Bearer ya29.access-1" || got.path != "/v1/projects/barbershop-test/messages:send" {
		t.Errorf("sent to %s with %q", got.path, got.auth)
	}
	want := map[string]any{"message": map[string]any{
		"token":        deviceToken,
		"notification": map[string]any{"title": p.Title, "body": p.Body},
		"data":         map[string]any{"kind": "booking_confirmed", "appointment_id": "a1"},
		"android":      map[string]any{"collapse_key": p.CollapseKey, "priority": "high"},
		"apns":         map[string]any{"headers": map[string]any{"apns-priority": "10", "apns-collapse-id": p.CollapseKey}},
	}}
	if g, w := fmt.Sprint(got.body), fmt.Sprint(want); g != w {
		t.Errorf("message\n got %s\nwant %s", g, w)
	}
	noSecrets(t, "the log", logs.String())
}

// An access token is used until five minutes before it expires.
func TestFCMRenewsAccessToken(t *testing.T) {
	t.Parallel()
	g := newGoogle(t)
	var logs bytes.Buffer
	f, _ := sender(t, g, &logs)
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	f.tokens.now = func() time.Time { return now }
	send := func() string {
		t.Helper()
		if err := f.Send(t.Context(), pushTo(t)); err != nil {
			t.Fatal(err)
		}
		return g.pushes[len(g.pushes)-1].auth
	}
	first := send()
	now = now.Add(3599*time.Second - tokenMargin - time.Second)
	if send() != first {
		t.Error("renewed too early")
	}
	now = now.Add(time.Second)
	if send() == first {
		t.Error("an access token about to expire was used")
	}
}

// Concurrent pushes share one sign-in.
func TestFCMConcurrentSignIn(t *testing.T) {
	t.Parallel()
	g := newGoogle(t)
	var logs bytes.Buffer
	f, _ := sender(t, g, &logs)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := f.Send(t.Context(), pushTo(t)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if tokens, pushes := g.count(); tokens != 1 || pushes != 10 {
		t.Errorf("%d tokens for %d pushes, want 1", tokens, pushes)
	}
}

func TestFCMRetries(t *testing.T) {
	t.Parallel()
	unavailable := fcmError(http.StatusServiceUnavailable, "UNAVAILABLE", "UNAVAILABLE")
	throttled := fcmError(http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "QUOTA_EXCEEDED")
	ok := answer{status: http.StatusOK, body: `{}`}
	for _, c := range []struct {
		name    string
		answers []answer
		pushes  int
		ok      bool
		waits   []time.Duration // the least each pause must be
	}{
		{"unavailable once", []answer{unavailable, ok}, 2, true, []time.Duration{1}},
		{"internal error twice", []answer{fcmError(500, "INTERNAL", "INTERNAL"), fcmError(502, "", ""), ok}, 3, true, []time.Duration{1, 1}},
		{"throttled, asked to wait 3s", []answer{{status: 429, body: throttled.body, retryAfter: "3"}, ok}, 2, true, []time.Duration{3 * time.Second}},
		{"down for good", []answer{unavailable}, 3, false, []time.Duration{1, 1}},
		{"asked to wait a minute", []answer{{status: 503, body: unavailable.body, retryAfter: "60"}}, 1, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g := newGoogle(t, c.answers...)
			var logs bytes.Buffer
			f, slept := sender(t, g, &logs)
			err := f.Send(t.Context(), pushTo(t))
			if _, pushes := g.count(); (err == nil) != c.ok || pushes != c.pushes {
				t.Fatalf("Send = %v after %d pushes; want ok=%v after %d", err, pushes, c.ok, c.pushes)
			}
			if errors.Is(err, app.ErrDeviceGone) || errors.Is(err, app.ErrPushRejected) {
				t.Errorf("a failure that may pass came back as final: %v", err)
			}
			if len(*slept) != len(c.waits) {
				t.Fatalf("paused %v, want %d pauses", *slept, len(c.waits))
			}
			for i, least := range c.waits {
				if d := (*slept)[i]; d < least || d > max(least, defaultRetry.cap) {
					t.Errorf("pause %d = %s, want between %s and %s", i, d, least, max(least, defaultRetry.cap))
				}
			}
			if err != nil {
				noSecrets(t, "the error", err.Error())
			}
			noSecrets(t, "the log", logs.String())
		})
	}
}

func TestFCMFinalAnswers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		answer answer
		want   error // nil: an error the outbox retries later
	}{
		{"uninstalled", fcmError(404, "NOT_FOUND", "UNREGISTERED"), app.ErrDeviceGone},
		{"another project's token", fcmError(403, "PERMISSION_DENIED", "SENDER_ID_MISMATCH"), app.ErrDeviceGone},
		{"invalid token or message", fcmError(400, "INVALID_ARGUMENT", "INVALID_ARGUMENT"), app.ErrPushRejected},
		{"a 400 with no details", answer{status: 400, body: "not json"}, app.ErrPushRejected},
		{"no permission (our setup)", fcmError(403, "PERMISSION_DENIED", ""), nil},
		{"wrong project (our setup)", fcmError(404, "NOT_FOUND", ""), nil},
		{"Apple refused Firebase's key", fcmError(401, "UNAUTHENTICATED", "THIRD_PARTY_AUTH_ERROR"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g := newGoogle(t, c.answer)
			var logs bytes.Buffer
			f, slept := sender(t, g, &logs)
			err := f.Send(t.Context(), pushTo(t))
			tokens, pushes := g.count()
			if err == nil || pushes != 1 || tokens != 1 || len(*slept) != 0 {
				t.Fatalf("Send = %v after %d pushes, %d tokens, %d pauses; want an error after one try", err, pushes, tokens, len(*slept))
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("Send = %v, want %v", err, c.want)
			}
			if c.want == nil && (errors.Is(err, app.ErrDeviceGone) || errors.Is(err, app.ErrPushRejected)) {
				t.Errorf("Send = %v, want an error worth retrying later", err)
			}
			noSecrets(t, "the error", err.Error())
			noSecrets(t, "the log", logs.String())
		})
	}
}

// FCM refusing the access token: sign in again and retry once.
func TestFCMAccessTokenRefused(t *testing.T) {
	t.Parallel()
	unauthenticated := fcmError(401, "UNAUTHENTICATED", "")
	g := newGoogle(t, unauthenticated, answer{status: 200, body: `{}`})
	var logs bytes.Buffer
	f, _ := sender(t, g, &logs)
	if err := f.Send(t.Context(), pushTo(t)); err != nil {
		t.Fatal(err)
	}
	if tokens, pushes := g.count(); tokens != 2 || pushes != 2 || g.pushes[1].auth != "Bearer ya29.access-2" {
		t.Errorf("%d tokens, %d pushes, then %q; want a new token for the retry", tokens, pushes, g.pushes[1].auth)
	}

	// Refused again: give up (the outbox retries later).
	g = newGoogle(t, unauthenticated)
	f, _ = sender(t, g, &logs)
	if err := f.Send(t.Context(), pushTo(t)); err == nil {
		t.Error("refused twice: want an error")
	}
	if tokens, pushes := g.count(); tokens != 2 || pushes != 2 {
		t.Errorf("%d tokens, %d pushes; want one retry", tokens, pushes)
	}
}

func TestFCMSignInRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status  int
		retried bool
	}{
		{http.StatusBadRequest, false},        // invalid_grant: a revoked key, a wrong clock
		{http.StatusServiceUnavailable, true}, // Google's trouble: may pass
	} {
		g := newGoogle(t)
		g.tokenOK, g.tokenAt = false, c.status
		var logs bytes.Buffer
		f, slept := sender(t, g, &logs)
		err := f.Send(t.Context(), pushTo(t))
		if _, pushes := g.count(); err == nil || pushes != 0 || (len(*slept) > 0) != c.retried {
			t.Errorf("sign-in %d: %v, %d pushes, pauses %v", c.status, err, pushes, *slept)
		}
		if err != nil {
			noSecrets(t, "the error", err.Error())
		}
	}
}

// A request that hangs is cut off by the timeout and retried.
func TestFCMTimeout(t *testing.T) {
	t.Parallel()
	g := newGoogle(t)
	g.block = make(chan struct{})
	defer close(g.block)
	var logs bytes.Buffer
	f, err := NewFCM(FCMConfig{
		Credentials: credentials(t, g.srv.URL+"/token"), Timeout: 200 * time.Millisecond,
		BaseURL: g.srv.URL, Transport: g.srv.Client().Transport,
	}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	pauses := 0
	f.retry.sleep = func(context.Context, time.Duration) error { pauses++; return nil }
	start := time.Now()
	err = f.Send(t.Context(), pushTo(t))
	if err == nil || pauses != 2 || time.Since(start) > 5*time.Second {
		t.Errorf("Send = %v after %s and %d pauses; want three timed-out tries", err, time.Since(start), pauses)
	}
}

// A cancelled job stops waiting at once.
func TestFCMCancelled(t *testing.T) {
	t.Parallel()
	g := newGoogle(t, fcmError(503, "UNAVAILABLE", ""))
	var logs bytes.Buffer
	f, _ := sender(t, g, &logs)
	f.retry.sleep = sleep
	f.retry.base, f.retry.cap = time.Hour, time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if err := f.Send(ctx, pushTo(t)); !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Errorf("Send = %v after %s; want context.Canceled at once", err, time.Since(start))
	}
}

// Pauses grow, stop growing at the cap, and are random below it, so
// workers retrying at once spread out ("full jitter").
func TestRetryPause(t *testing.T) {
	t.Parallel()
	p := defaultRetry
	for n := range 6 {
		ceiling := min(p.cap, p.base<<n)
		low := 0
		for range 1000 {
			d := p.pause(n)
			if d <= 0 || d > ceiling {
				t.Fatalf("pause(%d) = %s, want in (0, %s]", n, d, ceiling)
			}
			if d < ceiling/2 {
				low++
			}
		}
		if low < 300 || low > 700 { // about half, if spread evenly
			t.Errorf("pause(%d): %d of 1000 below half the ceiling, want about 500", n, low)
		}
	}
}

func TestNewFCMChecksCredentials(t *testing.T) {
	t.Parallel()
	good := credentials(t, "https://oauth2.googleapis.com/token")
	edit := func(field, value string) []byte {
		var m map[string]string
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		m[field] = value
		raw, _ := json.Marshal(m)
		return raw
	}
	if _, err := NewFCM(FCMConfig{Credentials: good, Timeout: time.Second}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("a good key: %v", err)
	}
	for name, cfg := range map[string]FCMConfig{
		"not JSON":             {Credentials: []byte("project_id=barbershop-test"), Timeout: time.Second},
		"a user's credentials": {Credentials: edit("type", "authorized_user"), Timeout: time.Second},
		"no project":           {Credentials: edit("project_id", ""), Timeout: time.Second},
		"no email":             {Credentials: edit("client_email", ""), Timeout: time.Second},
		"plain-HTTP sign-in":   {Credentials: edit("token_uri", "http://oauth2.googleapis.com/token"), Timeout: time.Second},
		"a broken key":         {Credentials: edit("private_key", "-----BEGIN PRIVATE KEY-----\nMIIBVQ==\n-----END PRIVATE KEY-----\n"), Timeout: time.Second},
		"no timeout":           {Credentials: good},
		"plain-HTTP FCM":       {Credentials: good, Timeout: time.Second, BaseURL: "http://fcm.googleapis.com"},
	} {
		_, err := NewFCM(cfg, slog.New(slog.DiscardHandler))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		noSecrets(t, name, err.Error())
	}
}
