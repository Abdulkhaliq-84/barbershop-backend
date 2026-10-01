package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBookingAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	_, _, branch, service, me := a.published(t)
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().In(riyadh).AddDate(0, 0, 1)
	at := func(h, m int) string {
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, riyadh).Format(time.RFC3339)
	}
	request := func(start string) string {
		return `{"branch_id":"` + branch + `","starts_at":"` + start + `","service_ids":["` + service + `"],"note":"قصير من الجوانب"}`
	}
	customer, other := a.signIn(t, "0557777777"), a.signIn(t, "0558888888")
	key := uuid.NewString()
	book := func(token, body, key string) response {
		return a.do(t, http.MethodPost, "/v1/appointments", token, body, "Idempotency-Key", key)
	}

	if r := book("", request(at(10, 0)), key); r.status != http.StatusUnauthorized {
		t.Errorf("no token: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, "/v1/appointments", customer, request(at(10, 0))); r.status != http.StatusBadRequest {
		t.Errorf("no Idempotency-Key: %d %v", r.status, r.body)
	}

	// Booked: confirmed (the branch confirms automatically), the owner's
	// haircut at their price, 10:00–10:30.
	r := book(customer, request(at(10, 0)), key)
	if r.status != http.StatusCreated || r.body["status"] != "confirmed" || r.body["assignment"] != "any_barber" || r.headers.Get("Idempotent-Replayed") != "" {
		t.Fatalf("book: %d %v", r.status, r.body)
	}
	id := r.body["id"].(string)
	items := r.body["items"].([]any)
	if barber := r.body["barber"].(map[string]any); barber["id"] != me || len(items) != 1 || r.body["price"].(map[string]any)["amount"] != float64(6000) || r.body["note"] != "قصير من الجوانب" {
		t.Errorf("appointment = %v", r.body)
	}
	if ends, _ := time.Parse(time.RFC3339, r.body["ends_at"].(string)); ends.In(riyadh).Format("15:04") != "10:30" {
		t.Errorf("ends at %v", r.body["ends_at"])
	}

	// The app retries (a timeout): the same appointment, marked as a replay.
	r = book(customer, request(at(10, 0)), key)
	if r.status != http.StatusCreated || r.body["id"] != id || r.headers.Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry: %d %v %v", r.status, r.headers.Get("Idempotent-Replayed"), r.body)
	}
	if r := book(customer, request(at(11, 0)), key); r.status != http.StatusUnprocessableEntity || r.body["code"] != "idempotency_key_reused" {
		t.Errorf("key reused: %d %v", r.status, r.body)
	}
	// Someone else wants 10:00: taken — and availability agrees.
	if r := book(other, request(at(10, 0)), uuid.NewString()); r.status != http.StatusConflict || r.body["code"] != "slot_unavailable" {
		t.Errorf("taken: %d %v", r.status, r.body)
	}
	slots := a.do(t, http.MethodGet, "/v1/branches/"+branch+"/availability?date="+tomorrow.Format(time.DateOnly)+"&service_ids="+service, "", "").body["slots"].([]any)
	if first, _ := time.Parse(time.RFC3339, slots[0].(map[string]any)["starts_at"].(string)); first.In(riyadh).Format("15:04") != "10:30" {
		t.Errorf("first free slot = %v, want 10:30", slots[0])
	}

	// My appointment is mine alone.
	if r := a.do(t, http.MethodGet, "/v1/me/appointments/"+id, customer, ""); r.status != http.StatusOK || r.body["id"] != id || r.body["barber"].(map[string]any)["id"] != me {
		t.Errorf("mine: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, "/v1/me/appointments/"+id, other, ""); r.status != http.StatusNotFound {
		t.Errorf("someone else's: %d %v", r.status, r.body)
	}

	for name, tt := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"off the grid":   {request(at(11, 7)), http.StatusUnprocessableEntity, "invalid_start"},
		"too far ahead":  {request(time.Now().AddDate(0, 3, 0).Truncate(time.Hour).Format(time.RFC3339)), http.StatusUnprocessableEntity, "invalid_start"},
		"no services":    {`{"branch_id":"` + branch + `","starts_at":"` + at(11, 0) + `","service_ids":[]}`, http.StatusBadRequest, "validation_failed"},
		"no such branch": {`{"branch_id":"01a0f000-0000-7000-8000-000000000000","starts_at":"` + at(11, 0) + `","service_ids":["` + service + `"]}`, http.StatusNotFound, "not_found"},
	} {
		if r := book(customer, tt.body, uuid.NewString()); r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tt.status, tt.code)
		}
	}

	// The branch allows two upcoming bookings each.
	if r := book(customer, request(at(11, 0)), uuid.NewString()); r.status != http.StatusCreated {
		t.Fatalf("second: %d %v", r.status, r.body)
	}
	if r := book(customer, request(at(12, 0)), uuid.NewString()); r.status != http.StatusConflict || r.body["code"] != "booking_limit_reached" {
		t.Errorf("third: %d %v", r.status, r.body)
	}
	// Each booking's event is on its way (notifications, M7).
	var events int
	if err := a.pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job WHERE kind = 'outbox_event' AND args->'event'->>'type' = 'booking.appointment_booked'`).Scan(&events); err != nil || events != 2 {
		t.Errorf("appointment_booked events = %d, %v", events, err)
	}
}
