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
