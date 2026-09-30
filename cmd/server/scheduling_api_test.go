package main

import (
	"net/http"
	"testing"
)

func TestOpeningHoursAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	var branches []string
	for range 2 {
		branches = append(branches, a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON).body["id"].(string))
	}
	hours := biz + "/branches/" + branches[0] + "/opening-hours"

	// Never set: all seven days, closed, version 0.
	r := a.do(t, http.MethodGet, hours, owner, "")
	days, _ := r.body["days"].([]any)
	if r.status != http.StatusOK || len(days) != 7 || r.body["version"] != float64(0) || r.body["updated_at"] != nil {
		t.Fatalf("new hours: %d %v", r.status, r.body)
	}
	if first := days[0].(map[string]any); first["weekday"] != "sunday" || len(first["intervals"].([]any)) != 0 {
		t.Errorf("sunday = %v", first)
	}

	// A manager of the branch sets them: a split Sunday, a Thursday shift
	// that runs past midnight, and Friday all day.
	_, token := a.invite(t, biz, owner, "0552222222", "manager", branches[0])
	manager := a.signIn(t, "0552222222")
	a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(token))
	week := `{"days":[
		{"weekday":"sunday","intervals":[{"opens":"09:00","closes":"12:00"},{"opens":"16:00","closes":"22:00"}]},
		{"weekday":"thursday","intervals":[{"opens":"16:00","closes":"02:00"}]},
		{"weekday":"friday","intervals":[{"opens":"00:00","closes":"24:00"}]}]}`
	// Thursday 16:00–02:00 ends Friday 02:00, but Friday opens at 00:00: overlap.
	if r := a.do(t, http.MethodPut, hours, manager, week, "If-Match", "0"); r.status != http.StatusUnprocessableEntity || r.body["code"] != "validation_failed" {
		t.Fatalf("overlap into Friday: %d %v", r.status, r.body)
	}
	week = `{"days":[
		{"weekday":"sunday","intervals":[{"opens":"09:00","closes":"12:00"},{"opens":"16:00","closes":"22:00"}]},
		{"weekday":"thursday","intervals":[{"opens":"16:00","closes":"02:00"}]},
		{"weekday":"friday","intervals":[{"opens":"14:00","closes":"24:00"}]}]}`
	r = a.do(t, http.MethodPut, hours, manager, week, "If-Match", `"0"`)
	if r.status != http.StatusOK || r.body["version"] != float64(1) || r.body["updated_at"] == nil {
		t.Fatalf("set: %d %v", r.status, r.body)
	}
	days = r.body["days"].([]any)
	thursday := days[4].(map[string]any)["intervals"].([]any)[0].(map[string]any)
	friday := days[5].(map[string]any)["intervals"].([]any)[0].(map[string]any)
	if thursday["opens"] != "16:00" || thursday["closes"] != "02:00" || friday["closes"] != "24:00" || len(days[0].(map[string]any)["intervals"].([]any)) != 2 {
		t.Errorf("saved week = %v", days)
	}

	// Staff of the branch read them; barbers can't change them.
	_, token = a.invite(t, biz, owner, "0553333333", "barber", branches[0])
	barber := a.signIn(t, "0553333333")
	a.do(t, http.MethodPost, "/v1/invitations/accept", barber, acceptBody(token))
	if r := a.do(t, http.MethodGet, hours, barber, ""); r.status != http.StatusOK || r.body["version"] != float64(1) {
		t.Errorf("barber reads: %d %v", r.status, r.body)
	}
	for name, tt := range map[string]struct {
		token, path, body, version string
		status                     int
	}{
		"barber sets":                {barber, hours, `{"days":[]}`, "1", http.StatusForbidden},
		"manager, other branch":      {manager, biz + "/branches/" + branches[1] + "/opening-hours", `{"days":[]}`, "0", http.StatusForbidden},
		"stale version":              {manager, hours, `{"days":[]}`, "0", http.StatusPreconditionFailed},
		"not on the 5-minute grid":   {manager, hours, `{"days":[{"weekday":"monday","intervals":[{"opens":"09:03","closes":"10:00"}]}]}`, "1", http.StatusBadRequest},
		"25 o'clock":                 {manager, hours, `{"days":[{"weekday":"monday","intervals":[{"opens":"25:00","closes":"10:00"}]}]}`, "1", http.StatusBadRequest},
		"a weekday that isn't":       {manager, hours, `{"days":[{"weekday":"funday","intervals":[]}]}`, "1", http.StatusBadRequest},
		"five shifts on one day":     {manager, hours, `{"days":[{"weekday":"monday","intervals":[{"opens":"01:00","closes":"02:00"},{"opens":"03:00","closes":"04:00"},{"opens":"05:00","closes":"06:00"},{"opens":"07:00","closes":"08:00"},{"opens":"09:00","closes":"10:00"}]}]}`, "1", http.StatusBadRequest},
		"Saturday night into Sunday": {manager, hours, `{"days":[{"weekday":"saturday","intervals":[{"opens":"20:00","closes":"10:00"}]},{"weekday":"sunday","intervals":[{"opens":"09:00","closes":"12:00"}]}]}`, "1", http.StatusUnprocessableEntity},
	} {
		if r := a.do(t, http.MethodPut, tt.path, tt.token, tt.body, "If-Match", tt.version); r.status != tt.status {
			t.Errorf("%s: %d %v, want %d", name, r.status, r.body, tt.status)
		}
	}

	// Another business sees nothing, even with this branch under its own ID.
	other, otherBiz := a.register(t, "0559876543", "1010000002")
	for name, path := range map[string]string{"direct": hours, "under own business": otherBiz + "/branches/" + branches[0] + "/opening-hours"} {
		if r := a.do(t, http.MethodGet, path, other, ""); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
}

// team registers a business with two branches and staff:
// a manager and a barber at branch 0, a barber at branch 1.
type team struct {
	owner, biz                       string
	branches                         []string
	manager, barber, otherBarber     string // access tokens
	managerID, barberID, otherID, me string // staff IDs (me: the owner's)
}

func newTeam(t *testing.T, a *api) team {
	t.Helper()
	tm := team{}
	tm.owner, tm.biz = a.register(t, "0551234567", "1010000001")
	for range 2 {
		tm.branches = append(tm.branches, a.do(t, http.MethodPost, tm.biz+"/branches", tm.owner, branchJSON).body["id"].(string))
	}
	join := func(phone, role, branch string) string {
		_, token := a.invite(t, tm.biz, tm.owner, phone, role, branch)
		access := a.signIn(t, phone)
		if r := a.do(t, http.MethodPost, "/v1/invitations/accept", access, acceptBody(token)); r.status != http.StatusOK {
			t.Fatalf("accept: %d %v", r.status, r.body)
		}
		return access
	}
	tm.manager = join("0552222222", "manager", tm.branches[0])
	tm.barber = join("0553333333", "barber", tm.branches[0])
	tm.otherBarber = join("0554444444", "barber", tm.branches[1])
	for _, s := range a.do(t, http.MethodGet, tm.biz+"/staff", tm.owner, "").body["data"].([]any) {
		m := s.(map[string]any)
		switch {
		case m["role"] == "owner":
			tm.me = m["id"].(string)
		case m["role"] == "manager":
			tm.managerID = m["id"].(string)
		case m["branch_ids"].([]any)[0] == tm.branches[0]:
			tm.barberID = m["id"].(string)
		default:
			tm.otherID = m["id"].(string)
		}
	}
	return tm
}

func (tm team) schedule(branch int, staff string) string {
	return tm.biz + "/branches/" + tm.branches[branch] + "/staff/" + staff + "/schedule"
}

func TestBarberScheduleAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tm := newTeam(t, a)
	mine := tm.schedule(0, tm.barberID)

	// Never set: empty, version 0; any colleague at the branch reads it.
	r := a.do(t, http.MethodGet, mine, tm.manager, "")
	if r.status != http.StatusOK || r.body["version"] != float64(0) || len(r.body["weekly"].([]any)) != 7 || len(r.body["overrides"].([]any)) != 0 {
		t.Fatalf("new schedule: %d %v", r.status, r.body)
	}

	// The barber sets their own: Thursday night, and a day off on 8 October.
	body := `{"weekly":[{"weekday":"thursday","intervals":[{"opens":"16:00","closes":"02:00"}]}],
		"overrides":[{"date":"2026-10-08","intervals":[]},{"date":"2026-10-11","intervals":[{"opens":"10:00","closes":"14:00"}]}]}`
	r = a.do(t, http.MethodPut, mine, tm.barber, body, "If-Match", "0")
	overrides, _ := r.body["overrides"].([]any)
	if r.status != http.StatusOK || r.body["version"] != float64(1) || len(overrides) != 2 {
		t.Fatalf("barber sets own: %d %v", r.status, r.body)
	}
	if first := overrides[0].(map[string]any); first["date"] != "2026-10-08" || len(first["intervals"].([]any)) != 0 {
		t.Errorf("day off = %v", first)
	}
	thursday := r.body["weekly"].([]any)[4].(map[string]any)["intervals"].([]any)[0].(map[string]any)
	if thursday["opens"] != "16:00" || thursday["closes"] != "02:00" {
		t.Errorf("thursday = %v", thursday)
	}
	// The branch manager changes it too; the owner as well.
	if r := a.do(t, http.MethodPut, mine, tm.manager, `{"weekly":[{"weekday":"sunday","intervals":[{"opens":"09:00","closes":"17:00"}]}]}`, "If-Match", "1"); r.status != http.StatusOK {
		t.Errorf("manager sets: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, mine, tm.owner, `{"weekly":[]}`, "If-Match", "2"); r.status != http.StatusOK {
		t.Errorf("owner sets: %d %v", r.status, r.body)
	}

	// The owner works at both branches: Thursday night at branch 0 clashes
	// with Friday 01:00 at branch 1.
	if r := a.do(t, http.MethodPut, tm.schedule(0, tm.me), tm.owner, `{"weekly":[{"weekday":"thursday","intervals":[{"opens":"16:00","closes":"02:00"}]}]}`, "If-Match", "0"); r.status != http.StatusOK {
		t.Fatalf("owner's own schedule: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, tm.schedule(1, tm.me), tm.owner, `{"weekly":[{"weekday":"friday","intervals":[{"opens":"01:00","closes":"03:00"}]}]}`, "If-Match", "0"); r.status != http.StatusUnprocessableEntity {
		t.Errorf("two branches at once: %d %v", r.status, r.body)
	}

	for name, tt := range map[string]struct {
		token, method, path, body, version string
		status                             int
	}{
		"barber sets a colleague's":      {tm.barber, http.MethodPut, tm.schedule(0, tm.managerID), `{"weekly":[]}`, "0", http.StatusForbidden},
		"barber of another branch reads": {tm.otherBarber, http.MethodGet, mine, "", "", http.StatusForbidden},
		"manager, other branch's barber": {tm.manager, http.MethodGet, tm.schedule(1, tm.otherID), "", "", http.StatusForbidden},
		"someone not at this branch":     {tm.owner, http.MethodGet, tm.schedule(0, tm.otherID), "", "", http.StatusNotFound},
		"made-up staff":                  {tm.owner, http.MethodGet, tm.schedule(0, "01a0f000-0000-7000-8000-000000000000"), "", "", http.StatusNotFound},
		"stale version":                  {tm.barber, http.MethodPut, mine, `{"weekly":[]}`, "1", http.StatusPreconditionFailed},
		"a date twice":                   {tm.barber, http.MethodPut, mine, `{"weekly":[],"overrides":[{"date":"2026-10-08","intervals":[]},{"date":"2026-10-08","intervals":[]}]}`, "3", http.StatusUnprocessableEntity},
		"overlapping hours on a date":    {tm.barber, http.MethodPut, mine, `{"weekly":[],"overrides":[{"date":"2026-10-08","intervals":[{"opens":"10:00","closes":"12:00"},{"opens":"11:00","closes":"13:00"}]}]}`, "3", http.StatusUnprocessableEntity},
		"not a date":                     {tm.barber, http.MethodPut, mine, `{"weekly":[],"overrides":[{"date":"2026-02-30","intervals":[]}]}`, "3", http.StatusBadRequest},
	} {
		if r := a.do(t, tt.method, tt.path, tt.token, tt.body, "If-Match", tt.version); r.status != tt.status {
			t.Errorf("%s: %d %v, want %d", name, r.status, r.body, tt.status)
		}
	}
}

func TestTimeOffAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	tm := newTeam(t, a)
	mine := tm.biz + "/staff/" + tm.barberID + "/time-off"
	off := `{"starts_at":"2030-10-05T10:00:00+03:00","ends_at":"2030-10-05T12:00:00+03:00","reason":"موعد طبيب"}`

	// The barber records their own; it comes back in UTC.
	r := a.do(t, http.MethodPost, mine, tm.barber, off)
	if r.status != http.StatusCreated || r.body["starts_at"] != "2030-10-05T07:00:00Z" || r.body["reason"] != "موعد طبيب" {
		t.Fatalf("add: %d %v", r.status, r.body)
	}
	id := r.body["id"].(string)
	// Overlapping time off is refused.
	if r := a.do(t, http.MethodPost, mine, tm.manager, `{"starts_at":"2030-10-05T11:00:00+03:00","ends_at":"2030-10-05T13:00:00+03:00"}`); r.status != http.StatusConflict || r.body["code"] != "time_off_overlaps" {
		t.Errorf("overlap: %d %v", r.status, r.body)
	}
	// Their manager and the owner see it; a barber elsewhere doesn't.
	for name, token := range map[string]string{"self": tm.barber, "manager": tm.manager, "owner": tm.owner} {
		if r := a.do(t, http.MethodGet, mine, token, ""); r.status != http.StatusOK || len(r.body["data"].([]any)) != 1 {
			t.Errorf("%s lists: %d %v", name, r.status, r.body)
		}
	}
	for name, tt := range map[string]struct {
		token, method, path, body string
		status                    int
	}{
		"another branch's barber":           {tm.otherBarber, http.MethodGet, mine, "", http.StatusForbidden},
		"manager, barber of another branch": {tm.manager, http.MethodPost, tm.biz + "/staff/" + tm.otherID + "/time-off", off, http.StatusForbidden},
		"manager, the owner's time off":     {tm.manager, http.MethodGet, tm.biz + "/staff/" + tm.me + "/time-off", "", http.StatusForbidden},
		"made-up staff":                     {tm.owner, http.MethodGet, tm.biz + "/staff/01a0f000-0000-7000-8000-000000000000/time-off", "", http.StatusNotFound},
		"ends before it starts":             {tm.barber, http.MethodPost, mine, `{"starts_at":"2030-10-05T12:00:00Z","ends_at":"2030-10-05T10:00:00Z"}`, http.StatusUnprocessableEntity},
		"no time zone":                      {tm.barber, http.MethodPost, mine, `{"starts_at":"2030-10-05T12:00:00","ends_at":"2030-10-05T14:00:00"}`, http.StatusBadRequest},
	} {
		if r := a.do(t, tt.method, tt.path, tt.token, tt.body); r.status != tt.status {
			t.Errorf("%s: %d %v, want %d", name, r.status, r.body, tt.status)
		}
	}
	// Removing it.
	if r := a.do(t, http.MethodDelete, mine+"/"+id, tm.manager, ""); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodDelete, mine+"/"+id, tm.manager, ""); r.status != http.StatusNotFound {
		t.Errorf("delete twice: %d", r.status)
	}
	// Another business sees nothing.
	other, _ := a.register(t, "0559876543", "1010000002")
	if r := a.do(t, http.MethodGet, mine, other, ""); r.status != http.StatusNotFound {
		t.Errorf("another business: %d", r.status)
	}
}
