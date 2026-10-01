package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAppointmentLifecycleAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz, branch, service, me := a.published(t)
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().In(riyadh).AddDate(0, 0, 1)
	at := func(h, m int) time.Time {
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, riyadh)
	}
	customer, other := a.signIn(t, "0557777777"), a.signIn(t, "0558888888")
	book := func(start time.Time) string {
		t.Helper()
		r := a.do(t, http.MethodPost, "/v1/appointments", customer,
			`{"branch_id":"`+branch+`","starts_at":"`+start.Format(time.RFC3339)+`","service_ids":["`+service+`"]}`, "Idempotency-Key", uuid.NewString())
		if r.status != http.StatusCreated {
			t.Fatalf("book %s: %d %v", start, r.status, r.body)
		}
		return r.body["id"].(string)
	}
	first, second := book(at(10, 0)), book(at(11, 0))

	// The booking carries its own deadline: the start minus the branch's
	// two-hour window.
	r := a.do(t, http.MethodGet, "/v1/me/appointments/"+first, customer, "")
	if until, _ := time.Parse(time.RFC3339, str(r.body["cancellable_until"])); !until.Equal(at(8, 0)) || r.body["source"] != "customer_app" || r.body["customer_id"] == nil || r.body["cancellation"] != nil {
		t.Errorf("mine: %v", r.body)
	}

	// The customer cancels; not twice, and not someone else's.
	cancel := func(token, id, body string) response {
		return a.do(t, http.MethodPost, "/v1/me/appointments/"+id+"/cancel", token, body)
	}
	if r := cancel(other, first, ""); r.status != http.StatusNotFound {
		t.Errorf("someone else's: %d %v", r.status, r.body)
	}
	r = cancel(customer, first, `{"reason":"تغيرت خططي"}`)
	if c, _ := r.body["cancellation"].(map[string]any); r.status != http.StatusOK || r.body["status"] != "cancelled" || c["by"] != "customer" || c["reason"] != "تغيرت خططي" {
		t.Errorf("cancel: %d %v", r.status, r.body)
	}
	if r := cancel(customer, first, ""); r.status != http.StatusConflict || r.body["code"] != "invalid_transition" || !strings.Contains(str(r.body["detail"]), "cancelled") {
		t.Errorf("cancel twice: %d %v", r.status, r.body)
	}

	// The shop's day: both, by start, the cancelled one included.
	dayPath := biz + "/branches/" + branch + "/appointments?date=" + tomorrow.Format(time.DateOnly) // biz is its path
	r = a.do(t, http.MethodGet, dayPath, owner, "")
	list, _ := r.body["appointments"].([]any)
	if r.status != http.StatusOK || len(list) != 2 || r.body["time_zone"] != "Asia/Riyadh" {
		t.Fatalf("day: %d %v", r.status, r.body)
	}
	if one := list[0].(map[string]any); one["id"] != first || one["status"] != "cancelled" || list[1].(map[string]any)["id"] != second || one["barber"].(map[string]any)["id"] != me {
		t.Errorf("day = %v", list)
	}
	if r := a.do(t, http.MethodGet, dayPath, customer, ""); r.status != http.StatusNotFound {
		t.Errorf("a customer reading the shop's day: %d %v", r.status, r.body)
	}

	// The shop acts on the second.
	act := func(token, action, body string) response {
		return a.do(t, http.MethodPost, biz+"/appointments/"+second+"/"+action, token, body)
	}
	for name, tt := range map[string]struct {
		token, action, body string
		status              int
		code                string
	}{
		"complete before it starts": {owner, "complete", "", http.StatusConflict, "appointment_not_started"},
		"confirm a confirmed one":   {owner, "confirm", "", http.StatusConflict, "invalid_transition"},
		"a reason with confirm":     {owner, "confirm", `{"reason":"x"}`, http.StatusUnprocessableEntity, "validation_failed"},
		"not staff":                 {customer, "cancel", "", http.StatusNotFound, "not_found"},
		"no such action":            {owner, "dance", "", http.StatusBadRequest, "validation_failed"},
	} {
		if r := act(tt.token, tt.action, tt.body); r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tt.status, tt.code)
		}
	}
	r = act(owner, "cancel", `{"reason":"الحلاق مريض"}`)
	if c, _ := r.body["cancellation"].(map[string]any); r.status != http.StatusOK || r.body["status"] != "cancelled" || c["by"] != "staff" {
		t.Errorf("the shop cancels: %d %v", r.status, r.body)
	}
	// The customer sees it, and the time is free again.
	if r := a.do(t, http.MethodGet, "/v1/me/appointments/"+second, customer, ""); r.body["status"] != "cancelled" {
		t.Errorf("the customer's view: %v", r.body)
	}
	book(at(11, 0))

	var events int
	if err := a.pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job WHERE kind = 'outbox_event' AND args->'event'->>'type' = 'booking.appointment_cancelled'`).Scan(&events); err != nil || events != 2 {
		t.Errorf("appointment_cancelled events = %d, %v", events, err)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
