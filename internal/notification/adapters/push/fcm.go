package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
)

// FCM sends pushes through Firebase Cloud Messaging's HTTP v1 API, signed in
// as a Google service account (ADR-0034).
//
// An outage or throttling is retried here a few times, with growing pauses;
// if it lasts, Send returns an error and the outbox tries the event again
// later. What retrying can't fix comes back as app.ErrDeviceGone or
// app.ErrPushRejected. Neither the device's token nor the account's key is
// ever logged or put in an error.
type FCM struct {
	client   *http.Client
	endpoint string // …/v1/projects/{project}/messages:send
	tokens   *accessTokens
	logger   *slog.Logger
	retry    retryPolicy
}

// FCMConfig configures the FCM sender.
type FCMConfig struct {
	// Credentials is the service account's JSON key, as Google issues it. A
	// secret: whoever holds it can push to every install of the app.
	Credentials []byte
	// Timeout bounds each request to Google, connecting included.
	Timeout time.Duration
	// BaseURL is FCM's address; empty means Google's. Tests set a fake.
	BaseURL string
	// Transport carries the requests; nil means one with sane timeouts.
	// Tests pass their fake server's.
	Transport http.RoundTripper
}

const (
	fcmBaseURL = "https://fcm.googleapis.com"
	fcmScope   = "https://www.googleapis.com/auth/firebase.messaging"
)

// NewFCM checks the credentials and returns the sender. It doesn't call
// Google: the first push fetches the first access token.
func NewFCM(cfg FCMConfig, logger *slog.Logger) (*FCM, error) {
	account, key, err := parseServiceAccount(cfg.Credentials)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		return nil, errors.New("fcm: the timeout must be positive")
	}
	base := cfg.BaseURL
	if base == "" {
		base = fcmBaseURL
	}
	if !strings.HasPrefix(base, "https://") {
		return nil, errors.New("fcm: the base URL must be https")
	}
	transport := cfg.Transport
	if transport == nil {
		transport = newTransport(cfg.Timeout)
	}
	client := &http.Client{Transport: transport, Timeout: cfg.Timeout}
	return &FCM{
		client:   client,
		endpoint: base + "/v1/projects/" + url.PathEscape(account.ProjectID) + "/messages:send",
		tokens:   &accessTokens{client: client, account: account, key: key, now: time.Now},
		logger:   logger,
		retry:    defaultRetry,
	}, nil
}

// newTransport is http.DefaultTransport's setup with every wait bounded: Go's
// zero values mean "wait for ever".
func newTransport(timeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   10, // the worker sends to one host
		ForceAttemptHTTP2:     true,
	}
}

// serviceAccount is the part of Google's JSON key the sender needs.
type serviceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// parseServiceAccount reads and checks a JSON key. Its errors never quote
// the key.
func parseServiceAccount(raw []byte) (serviceAccount, *rsa.PrivateKey, error) {
	var a serviceAccount
	if err := json.Unmarshal(raw, &a); err != nil {
		return serviceAccount{}, nil, errors.New("fcm: the credentials are not a JSON key")
	}
	switch {
	case a.Type != "service_account":
		return serviceAccount{}, nil, errors.New("fcm: the credentials are not a service account key")
	case a.ProjectID == "" || a.ClientEmail == "":
		return serviceAccount{}, nil, errors.New("fcm: the credentials have no project_id or client_email")
	case !strings.HasPrefix(a.TokenURI, "https://"):
		return serviceAccount{}, nil, errors.New("fcm: the credentials' token_uri must be https")
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(a.PrivateKey))
	if err != nil {
		return serviceAccount{}, nil, errors.New("fcm: the credentials' private_key is not an RSA key")
	}
	a.PrivateKey = "" // parsed; don't keep a second copy around
	return a, key, nil
}

// accessTokens signs in to Google as the service account (OAuth 2.0 JWT
// bearer grant) and reuses the access token until shortly before it
// expires. The lock makes concurrent pushes share one fetch.
type accessTokens struct {
	client  *http.Client
	account serviceAccount
	key     *rsa.PrivateKey
	now     func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// tokenMargin is how long before its expiry a token is replaced, so none
// expires in flight.
const tokenMargin = 5 * time.Minute

// get returns a valid access token, fetching a new one if needed.
func (a *accessTokens) get(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.token != "" && now.Before(a.expires.Add(-tokenMargin)) {
		return a.token, nil
	}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   a.account.ClientEmail,
		"scope": fcmScope,
		"aud":   a.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	assertion.Header["kid"] = a.account.PrivateKeyID
	signed, err := assertion.SignedString(a.key)
	if err != nil {
		return "", fmt.Errorf("fcm: sign the token request: %w", err)
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signed}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("fcm: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fcm: token request: %w", err)
	}
	defer resp.Body.Close()
	defer drain(resp.Body)
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"` // an OAuth error code, e.g. invalid_grant
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil && resp.StatusCode == http.StatusOK {
		return "", fmt.Errorf("fcm: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" || body.ExpiresIn <= 0 {
		return "", &tokenError{status: resp.StatusCode, code: body.Error}
	}
	a.token, a.expires = body.AccessToken, now.Add(time.Duration(body.ExpiresIn)*time.Second)
	return a.token, nil
}

// forget drops token if it's the one cached (Google refused it), so the
// next get fetches a new one.
func (a *accessTokens) forget(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == token {
		a.token = ""
	}
}

// tokenError is Google refusing to sign the account in. 5xx and 429 are
// worth retrying; the rest (a revoked key, a wrong clock) need an operator.
type tokenError struct {
	status int
	code   string
}

func (e *tokenError) Error() string {
	return fmt.Sprintf("fcm: access token refused: status %d %s", e.status, e.code)
}

// retryPolicy is how Send retries: up to attempts tries in all, pausing a
// random time up to base·2ⁿ (capped) in between, or as long as Google asks
// (Retry-After) if that's at most maxWait. A longer wait is left to the
// outbox's next try.
type retryPolicy struct {
	attempts int
	base     time.Duration
	cap      time.Duration
	maxWait  time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
}

var defaultRetry = retryPolicy{attempts: 3, base: 250 * time.Millisecond, cap: 2 * time.Second, maxWait: 10 * time.Second, sleep: sleep}

// pause is how long to wait after failed attempt n (0 for the first):
// "full jitter", so many workers retrying at once spread out.
func (p retryPolicy) pause(n int) time.Duration {
	ceiling := min(p.cap, p.base<<n)
	return rand.N(ceiling) + 1 //nolint:gosec // G404: spreading retries out needs no secret randomness
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Send pushes p to its device.
func (f *FCM) Send(ctx context.Context, p app.Push) error {
	body, err := json.Marshal(fcmRequest{Message: message(p)})
	if err != nil {
		return fmt.Errorf("fcm: encode: %w", err)
	}
	refreshed := false
	for n := 0; ; n++ {
		err := f.post(ctx, body)
		var retry *retryable
		switch {
		case err == nil:
			return nil
		case errors.Is(err, app.ErrDeviceGone):
			f.logger.InfoContext(ctx, "fcm: device no longer registered",
				slog.String("device_id", p.Device.ID.String()), slog.String("error", err.Error()))
			return err
		case errors.Is(err, app.ErrPushRejected):
			f.logger.ErrorContext(ctx, "fcm: push rejected",
				slog.String("device_id", p.Device.ID.String()), slog.String("error", err.Error()))
			return err
		case errors.Is(err, errUnauthorized) && !refreshed:
			// The access token was revoked or expired early: fetch a new
			// one and try again at once, once.
			refreshed = true
			n--
			continue
		case !errors.As(err, &retry):
			return err
		case n+1 >= f.retry.attempts:
			return fmt.Errorf("%w (gave up after %d tries)", err, f.retry.attempts)
		}
		wait := f.retry.pause(n)
		if retry.after > 0 {
			if retry.after > f.retry.maxWait {
				return fmt.Errorf("%w (asked to wait %s: left to the next try)", err, retry.after)
			}
			wait = max(wait, retry.after)
		}
		f.logger.WarnContext(ctx, "fcm: retrying a push",
			slog.String("device_id", p.Device.ID.String()), slog.Int("attempt", n+1),
			slog.String("error", err.Error()), slog.Duration("wait", wait))
		if err := f.retry.sleep(ctx, wait); err != nil {
			return fmt.Errorf("fcm: %w", err)
		}
	}
}

// errUnauthorized is FCM refusing the access token.
var errUnauthorized = errors.New("fcm: access token refused")

// retryable is a failure that may pass: throttling, Google's server errors,
// network trouble. after is how long Google asked to wait, if it did.
type retryable struct {
	err   error
	after time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

// post makes one attempt, and says what came of it.
func (f *FCM) post(ctx context.Context, body []byte) error {
	token, err := f.tokens.get(ctx)
	if err != nil {
		var refused *tokenError
		if errors.As(err, &refused) && refused.status != http.StatusTooManyRequests && refused.status < 500 {
			return err
		}
		return &retryable{err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fcm: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() != nil { // the job was cancelled: don't retry here
			return fmt.Errorf("fcm: %w", ctx.Err())
		}
		// Network trouble or our timeout. The url.Error names only the
		// endpoint, never the token (it is in the body).
		return &retryable{err: fmt.Errorf("fcm: %w", err)}
	}
	defer resp.Body.Close()
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	code := errorCode(resp.Body)
	failed := fmt.Errorf("fcm: status %d %s", resp.StatusCode, code)
	switch {
	case code == "UNREGISTERED" || code == "SENDER_ID_MISMATCH":
		// Uninstalled, or the token belongs to another Firebase project.
		return fmt.Errorf("%w: %w", app.ErrDeviceGone, failed)
	case resp.StatusCode == http.StatusUnauthorized && code != "THIRD_PARTY_AUTH_ERROR":
		// (THIRD_PARTY_AUTH_ERROR is Apple refusing Firebase's own key: an
		// operator's fix, like the 403s below.)
		f.tokens.forget(token)
		return errUnauthorized
	case resp.StatusCode == http.StatusBadRequest:
		// INVALID_ARGUMENT: a token FCM doesn't accept, or a message it
		// can't send. The device is kept: a bad message would otherwise
		// cost every device.
		return fmt.Errorf("%w: %w", app.ErrPushRejected, failed)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return &retryable{err: failed, after: retryAfter(resp.Header.Get("Retry-After"))}
	}
	// 403 PERMISSION_DENIED and the like: our setup is wrong. The outbox
	// retries later, which works once an operator fixes it.
	return failed
}

// errorCode reads FCM's error body: the FcmError code if it gives one
// (UNREGISTERED, …), else the general status (INVALID_ARGUMENT, …). The
// human-readable message is left out.
func errorCode(r io.Reader) string {
	var body struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Type      string `json:"@type"`
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&body) != nil {
		return ""
	}
	for _, d := range body.Error.Details {
		if d.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && d.ErrorCode != "" {
			return d.ErrorCode
		}
	}
	return body.Error.Status
}

// retryAfter reads a Retry-After header given in seconds (Google's form).
func retryAfter(v string) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || s <= 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}

// drain reads what's left of a body (before it is closed) so the
// connection can be reused.
func drain(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
}

// fcmRequest is the body of messages:send.
type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
	Android      fcmAndroid        `json:"android"`
	APNs         fcmAPNs           `json:"apns"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmAndroid struct {
	CollapseKey string `json:"collapse_key,omitempty"`
	Priority    string `json:"priority"`
}

type fcmAPNs struct {
	Headers map[string]string `json:"headers"`
}

// message is p as FCM takes it: shown at once (high priority), and a
// repeat of the same event replacing the first on the phone.
func message(p app.Push) fcmMessage {
	headers := map[string]string{"apns-priority": "10"}
	if p.CollapseKey != "" {
		headers["apns-collapse-id"] = p.CollapseKey // at most 64 bytes: a UUID is 36
	}
	return fcmMessage{
		Token:        p.Device.Token.Reveal(),
		Notification: fcmNotification{Title: p.Title, Body: p.Body},
		Data:         p.Data,
		Android:      fcmAndroid{CollapseKey: p.CollapseKey, Priority: "high"},
		APNs:         fcmAPNs{Headers: headers},
	}
}
