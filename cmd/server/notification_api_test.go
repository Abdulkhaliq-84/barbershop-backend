package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// pushes returns the development pushes logged so far.
func (b *logBuffer) pushes() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil && strings.HasPrefix(str(line["msg"]), "DEVELOPMENT PUSH") {
			out = append(out, line)
		}
	}
	return out
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNotificationAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz, branch, service, _ := a.published(t)
	customer, other := a.signIn(t, "0557777777"), a.signIn(t, "0558888888")
	phoneToken := "dQw4w9WgXcQ:APA91bHun4MxP5egoKMwt2KZFBaFUH-1RYqx_Ln5L6Zc"
	tabletToken := "ios-test-device-token-0000000000-not-a-real-one"
	register := func(token, pushToken, platform, locale string) response {
		t.Helper()
		return a.do(t, http.MethodPost, "/v1/me/devices", token,
			`{"token":"`+pushToken+`","platform":"`+platform+`","locale":"`+locale+`"}`)
	}

	// Signed in only, and only what a push token looks like.
	if r := register("", phoneToken, "android", "ar"); r.status != http.StatusUnauthorized {
		t.Errorf("no access token: %d %v", r.status, r.body)
	}
	for name, body := range map[string][3]string{
		"short token":       {"too-short", "android", "ar"},
		"token with spaces": {strings.Repeat("a b ", 10), "android", "ar"},
		"web":               {phoneToken, "web", "ar"},
		"french":            {phoneToken, "android", "fr"},
	} {
		if r := register(customer, body[0], body[1], body[2]); r.status != http.StatusBadRequest || r.body["code"] != "validation_failed" {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}

	// The customer's phone (Arabic) and tablet (English). The token is never
	// sent back.
	r := register(customer, phoneToken, "android", "ar")
	if r.status != http.StatusOK || r.body["platform"] != "android" || r.body["locale"] != "ar" || r.body["token"] != nil {
		t.Fatalf("register: %d %v", r.status, r.body)
	}
	phone := str(r.body["id"])
	if r := register(customer, phoneToken, "android", "ar"); r.status != http.StatusOK || r.body["id"] != phone {
		t.Errorf("registered again: %d %v; want the same device", r.status, r.body)
	}
	tablet := str(register(customer, tabletToken, "ios", "en").body["id"])

	// Booked (and confirmed at once): both hear about it, each in its
	// language, a moment later.
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().In(riyadh).AddDate(0, 0, 1)
	start := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 10, 0, 0, 0, riyadh)
	r = a.do(t, http.MethodPost, "/v1/appointments", customer,
		`{"branch_id":"`+branch+`","starts_at":"`+start.Format(time.RFC3339)+`","service_ids":["`+service+`"]}`, "Idempotency-Key", uuid.NewString())
	if r.status != http.StatusCreated || r.body["status"] != "confirmed" {
		t.Fatalf("book: %d %v", r.status, r.body)
	}
	appointment := str(r.body["id"])
	deliveries := func(kind string) int {
		var n int
		if err := a.pool.QueryRow(t.Context(),
			`SELECT count(*) FROM notification.deliveries WHERE kind = $1 AND appointment_id = $2 AND outcome = 'sent'`, kind, appointment).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	until(t, "the booking pushes", func() bool { return deliveries("booking_confirmed") == 2 })
	byDevice := map[string]map[string]any{}
	for _, p := range a.logs.pushes() {
		byDevice[str(p["device_id"])] = p
	}
	if p := byDevice[phone]; p["title"] != "تم تأكيد حجزك" || !strings.Contains(str(p["body"]), "10:00") || p["kind"] != "booking_confirmed" {
		t.Errorf("the phone's push: %v", p)
	}
	if p := byDevice[tablet]; p["title"] != "Booking confirmed" || !strings.Contains(str(p["body"]), "10:00") {
		t.Errorf("the tablet's push: %v", p)
	}

	// The tablet signs out; the shop then cancels: only the phone hears.
	if r := a.do(t, http.MethodDelete, "/v1/me/devices/"+tablet, other, ""); r.status != http.StatusNotFound {
		t.Errorf("someone else removing it: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodDelete, "/v1/me/devices/"+tablet, customer, ""); r.status != http.StatusNoContent {
		t.Errorf("remove: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodDelete, "/v1/me/devices/"+tablet, customer, ""); r.status != http.StatusNotFound {
		t.Errorf("remove twice: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, biz+"/appointments/"+appointment+"/cancel", owner, `{"reason":"الحلاق مريض"}`); r.status != http.StatusOK {
		t.Fatalf("the shop cancels: %d %v", r.status, r.body)
	}
	until(t, "the cancellation push", func() bool { return deliveries("booking_cancelled") == 1 })
	var device string
	if err := a.pool.QueryRow(t.Context(),
		`SELECT device_id FROM notification.deliveries WHERE kind = 'booking_cancelled'`).Scan(&device); err != nil || device != phone {
		t.Errorf("cancellation pushed to %s, %v; want the phone %s", device, err, phone)
	}

	// Push tokens are credentials: never in the log.
	if logs := a.logs.String(); strings.Contains(logs, phoneToken) || strings.Contains(logs, tabletToken) {
		t.Error("a push token was logged")
	}
}
