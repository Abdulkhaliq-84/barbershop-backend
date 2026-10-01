package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWalkInAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz, branch, service, me := a.published(t)
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().In(riyadh).AddDate(0, 0, 1)
	at := func(h, m int) string {
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, riyadh).Format(time.RFC3339)
	}
	walkIn := func(start, barber string) string {
		return `{"starts_at":"` + start + `","service_ids":["` + service + `"],"barber_id":"` + barber + `","customer_name":"أبو فهد"}`
	}
	path := biz + "/branches/" + branch + "/appointments" // biz is its path
	book := func(token, body, key string) response {
		return a.do(t, http.MethodPost, path, token, body, "Idempotency-Key", key)
	}
	key := uuid.NewString()
	first := strings.TrimSuffix(walkIn(at(10, 7), me), "}") + `,"note":"بدون موس"}`

	// The owner books a walk-in with themselves at 10:07: off the slot grid,
	// fine for the shop. Confirmed at once, with a name and no account.
	r := book(owner, first, key)
	if r.status != http.StatusCreated || r.body["status"] != "confirmed" || r.body["source"] != "staff" || r.body["customer_name"] != "أبو فهد" || r.body["customer_id"] != nil || r.body["note"] != "بدون موس" || r.headers.Get("Idempotent-Replayed") != "" {
		t.Fatalf("walk-in: %d %v", r.status, r.body)
	}
	id := r.body["id"].(string)
	if r := book(owner, first, key); r.status != http.StatusCreated || r.body["id"] != id || r.headers.Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry: %d %v", r.status, r.body)
	}
	if r := book(owner, walkIn(at(10, 7), me), key); r.status != http.StatusUnprocessableEntity || r.body["code"] != "idempotency_key_reused" {
		t.Errorf("the key again, without the note: %d %v", r.status, r.body)
	}
	draft := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON)
	if draft.status != http.StatusCreated {
		t.Fatalf("a second branch: %d %v", draft.status, draft.body)
	}
	for name, tt := range map[string]struct {
		token, body string
		status      int
		code        string
	}{
		"the same time again":      {owner, walkIn(at(10, 30), me), http.StatusConflict, "slot_unavailable"},
		"a customer":               {a.signIn(t, "0557777777"), walkIn(at(12, 0), me), http.StatusNotFound, "not_found"},
		"not a barber here":        {owner, walkIn(at(12, 0), uuid.NewString()), http.StatusUnprocessableEntity, "barber_unavailable"},
		"seconds":                  {owner, walkIn(tomorrowAt(tomorrow, riyadh, 12, 0, 30), me), http.StatusUnprocessableEntity, "invalid_start"},
		"no name":                  {owner, strings.Replace(walkIn(at(12, 0), me), "أبو فهد", "", 1), http.StatusBadRequest, "validation_failed"},
		"a blank name":             {owner, strings.Replace(walkIn(at(12, 0), me), "أبو فهد", "   ", 1), http.StatusUnprocessableEntity, "validation_failed"},
		"after the barber's hours": {owner, walkIn(at(17, 45), me), http.StatusConflict, "slot_unavailable"},
		"an unpublished branch":    {owner, walkIn(at(12, 0), me), http.StatusConflict, "branch_not_bookable"},
	} {
		to := path
		if name == "an unpublished branch" {
			to = biz + "/branches/" + draft.body["id"].(string) + "/appointments"
		}
		if r := a.do(t, http.MethodPost, to, tt.token, tt.body, "Idempotency-Key", uuid.NewString()); r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tt.status, tt.code)
		}
	}

	// The shop's day shows it, and the shop can act on it.
	r = a.do(t, http.MethodGet, path+"?date="+tomorrow.Format(time.DateOnly), owner, "")
	if list, _ := r.body["appointments"].([]any); r.status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["customer_name"] != "أبو فهد" {
		t.Errorf("day: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, biz+"/appointments/"+id+"/cancel", owner, ""); r.status != http.StatusOK || r.body["status"] != "cancelled" {
		t.Errorf("cancel the walk-in: %d %v", r.status, r.body)
	}

	// The worker runs booking's expiry task (once as it starts, then every minute).
	deadline := time.Now().Add(15 * time.Second)
	for {
		var runs int
		if err := a.pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job WHERE kind = 'scheduled_task' AND args->>'name' = 'booking.expire_pending' AND state = 'completed'`).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if runs > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the expiry task never ran")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func tomorrowAt(day time.Time, loc *time.Location, h, m, s int) string {
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, s, 0, loc).Format(time.RFC3339)
}
