package main

import (
	"net/http"
	"testing"
	"time"
)

// published sets up a branch customers can book: an approved business,
// open every day 09:00–21:00, a 30-minute haircut the owner performs, the
// owner working 10:00–18:00 every day. It returns the owner's token, the
// business path, the branch ID, the service ID and the owner's staff ID.
func (a *api) published(t *testing.T) (owner, biz, branch, service, me string) {
	t.Helper()
	owner, biz = a.approved(t, "0551234567", "1010000001", "0500000001")
	branch = a.firstBranch(t, biz, owner)
	path := biz + "/branches/" + branch
	for _, s := range a.do(t, http.MethodGet, biz+"/staff", owner, "").body["data"].([]any) {
		if m := s.(map[string]any); m["role"] == "owner" {
			me = m["id"].(string)
		}
	}
	days, hours := "", ""
	for i, d := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		if i > 0 {
			days, hours = days+",", hours+","
		}
		days += `{"weekday":"` + d + `","intervals":[{"opens":"09:00","closes":"21:00"}]}`
		hours += `{"weekday":"` + d + `","intervals":[{"opens":"10:00","closes":"18:00"}]}`
	}
	for _, step := range []struct{ method, path, body, version string }{
		{http.MethodPut, path + "/opening-hours", `{"days":[` + days + `]}`, "0"},
		{http.MethodPut, path + "/staff/" + me + "/schedule", `{"weekly":[` + hours + `]}`, "0"},
	} {
		if r := a.do(t, step.method, step.path, owner, step.body, "If-Match", step.version); r.status != http.StatusOK {
			t.Fatalf("%s: %d %v", step.path, r.status, r.body)
		}
	}
	service = a.do(t, http.MethodPost, path+"/services", owner, serviceJSON).body["id"].(string)
	if r := a.do(t, http.MethodPut, path+"/services/"+service+"/offerings", owner, `{"offerings":[{"staff_id":"`+me+`"}]}`, "If-Match", "1"); r.status != http.StatusOK {
		t.Fatalf("offering: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, path+"/publish", owner, "", "If-Match", "1"); r.status != http.StatusOK {
		t.Fatalf("publish: %d %v", r.status, r.body)
	}
	return owner, biz, branch, service, me
}

func TestAvailabilityAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz, branch, service, me := a.published(t)
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().In(riyadh).AddDate(0, 0, 1)
	date := tomorrow.Format(time.DateOnly)
	availability := "/v1/branches/" + branch + "/availability?date=" + date + "&service_ids=" + service

	// No token needed: a published branch's free times are public. Every
	// quarter hour from 10:00 to 17:30 (a 30-minute haircut before 18:00).
	r := a.do(t, http.MethodGet, availability, "", "")
	if r.status != http.StatusOK || r.body["time_zone"] != "Asia/Riyadh" || r.body["date"] != date {
		t.Fatalf("availability: %d %v", r.status, r.body)
	}
	barbers := r.body["barbers"].([]any)
	if first := barbers[0].(map[string]any); len(barbers) != 1 || first["id"] != me || first["duration_minutes"] != float64(30) || first["price"].(map[string]any)["amount"] != float64(6000) {
		t.Errorf("barbers = %v", barbers)
	}
	slots := r.body["slots"].([]any)
	startsAt := func(s any) string {
		at, _ := time.Parse(time.RFC3339, s.(map[string]any)["starts_at"].(string))
		return at.In(riyadh).Format("15:04")
	}
	if len(slots) != 31 || startsAt(slots[0]) != "10:00" || startsAt(slots[30]) != "17:30" {
		t.Fatalf("slots: %d, %v … %v", len(slots), slots[0], slots[len(slots)-1])
	}

	// A booking at 12:00 (written directly: booking arrives in M5.3) takes
	// 11:45, 12:00 and 12:15 away.
	noon := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 12, 0, 0, 0, riyadh)
	if _, err := a.pool.Exec(t.Context(), `
		INSERT INTO booking.appointments (id, business_id, branch_id, staff_id, customer_id, status, source, assignment,
			starts_at, ends_at, during, price_amount, price_currency, version, created_at, updated_at)
		SELECT gen_random_uuid(), business_id, $1, $2, gen_random_uuid(), 'confirmed', 'staff', 'requested_barber',
			$3, $4, tstzrange($3, $4, '[)'), 6000, 'SAR', 1, now(), now()
		FROM business.branches WHERE id = $1`, branch, me, noon, noon.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	r = a.do(t, http.MethodGet, availability, "", "")
	var starts []string
	for _, s := range r.body["slots"].([]any) {
		starts = append(starts, startsAt(s))
	}
	if len(starts) != 28 || starts[6] != "11:30" || starts[7] != "12:30" {
		t.Errorf("after a booking at noon: %v", starts)
	}

	for name, tt := range map[string]struct {
		path   string
		status int
		code   string
	}{
		"not a date":         {"/v1/branches/" + branch + "/availability?date=2029-02-30&service_ids=" + service, http.StatusBadRequest, "validation_failed"},
		"no services":        {"/v1/branches/" + branch + "/availability?date=" + date, http.StatusBadRequest, "validation_failed"},
		"a service twice":    {availability + "," + service, http.StatusUnprocessableEntity, "validation_failed"},
		"not on the menu":    {"/v1/branches/" + branch + "/availability?date=" + date + "&service_ids=01a0f000-0000-7000-8000-000000000000", http.StatusUnprocessableEntity, "service_unavailable"},
		"not their barber":   {availability + "&barber_id=01a0f000-0000-7000-8000-000000000000", http.StatusUnprocessableEntity, "barber_unavailable"},
		"no such branch":     {"/v1/branches/01a0f000-0000-7000-8000-000000000000/availability?date=" + date + "&service_ids=" + service, http.StatusNotFound, "not_found"},
		"past the horizon":   {"/v1/branches/" + branch + "/availability?date=" + tomorrow.AddDate(0, 0, 60).Format(time.DateOnly) + "&service_ids=" + service, http.StatusOK, ""},
		"the barber by name": {availability + "&barber_id=" + me, http.StatusOK, ""},
	} {
		r := a.do(t, http.MethodGet, tt.path, "", "")
		if r.status != tt.status || (tt.code != "" && r.body["code"] != tt.code) {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tt.status, tt.code)
		}
		if name == "past the horizon" && len(r.body["slots"].([]any)) != 0 {
			t.Errorf("past the horizon: %v", r.body["slots"])
		}
	}

	// Unpublished: customers can't see it any more.
	if r := a.do(t, http.MethodPost, biz+"/branches/"+branch+"/unpublish", owner, "", "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("unpublish: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, availability, "", ""); r.status != http.StatusNotFound {
		t.Errorf("unpublished: %d %v", r.status, r.body)
	}
}
