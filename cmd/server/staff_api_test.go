package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// lastInvitation returns the token of the most recent development
// invitation SMS.
func (b *logBuffer) lastInvitation(t *testing.T) string {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	link := ""
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var line struct {
			Link string `json:"invitation_link"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Link != "" {
			link = line.Link
		}
	}
	u, err := url.Parse(link)
	if err != nil || u.Query().Get("token") == "" {
		t.Fatalf("no invitation link in the log (%q)", link)
	}
	return u.Query().Get("token")
}

// invite has the owner invite phone and returns the invitation and its token.
func (a *api) invite(t *testing.T, biz, owner, phone, role string, branches ...string) (response, string) {
	t.Helper()
	body := `{"phone":"` + phone + `","display_name":"أحمد","role":"` + role + `","branch_ids":["` + strings.Join(branches, `","`) + `"]}`
	r := a.do(t, http.MethodPost, biz+"/staff/invitations", owner, body)
	if r.status != http.StatusCreated {
		t.Fatalf("invite %s: %d %v", phone, r.status, r.body)
	}
	return r, a.logs.lastInvitation(t)
}

func acceptBody(token string) string { return `{"token":"` + token + `"}` }

func TestStaffInvitationsAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	var branches []string
	for range 2 {
		r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON)
		if r.status != http.StatusCreated {
			t.Fatalf("branch: %d %v", r.status, r.body)
		}
		branches = append(branches, r.body["id"].(string))
	}
	mine, other := biz+"/branches/"+branches[0], biz+"/branches/"+branches[1]

	// The owner invites a manager and a barber for the first branch. The
	// response never carries the token: only the invited phone gets it.
	r, managerToken := a.invite(t, biz, owner, "0552222222", "manager", branches[0])
	if r.body["status"] != "pending" || r.body["phone"] != "+966552222222" || r.body["role"] != "manager" || r.body["token"] != nil {
		t.Errorf("invitation = %v", r.body)
	}
	_, barberToken := a.invite(t, biz, owner, "0553333333", "barber", branches[0])
	if r := a.do(t, http.MethodGet, biz+"/staff/invitations", owner, ""); len(r.body["data"].([]any)) != 2 {
		t.Fatalf("pending: %d %v", r.status, r.body)
	}

	// The manager signs in with the invited phone. Someone else's link is refused.
	manager := a.signIn(t, "0552222222")
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(barberToken)); r.status != http.StatusNotFound || r.body["code"] != "invitation_invalid" {
		t.Errorf("wrong phone: %d %v", r.status, r.body)
	}
	r = a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(managerToken))
	if r.status != http.StatusOK || r.body["role"] != "manager" {
		t.Fatalf("accept: %d %v", r.status, r.body)
	}
	if list := memberships(t, a.do(t, http.MethodGet, "/v1/me/memberships", manager, "")); len(list) != 1 {
		t.Errorf("manager memberships = %v", list)
	}
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(managerToken)); r.status != http.StatusNotFound || r.body["code"] != "invitation_invalid" {
		t.Errorf("used link: %d %v", r.status, r.body)
	}

	barber := a.signIn(t, "0553333333")
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", barber, acceptBody(barberToken)); r.status != http.StatusOK || r.body["role"] != "barber" {
		t.Fatalf("barber accept: %d %v", r.status, r.body)
	}

	// The team: the owner and managers see it; barbers don't.
	r = a.do(t, http.MethodGet, biz+"/staff", manager, "")
	if data, _ := r.body["data"].([]any); r.status != http.StatusOK || len(data) != 3 {
		t.Fatalf("staff: %d %v", r.status, r.body)
	}
	if last := r.body["data"].([]any)[2].(map[string]any); last["display_name"] != "أحمد" || len(last["branch_ids"].([]any)) != 1 {
		t.Errorf("barber in staff list = %v", last)
	}
	if r := a.do(t, http.MethodGet, biz+"/staff", barber, ""); r.status != http.StatusForbidden {
		t.Errorf("barber lists staff: %d", r.status)
	}

	// A manager runs their own branch, and only that one.
	if r := a.do(t, http.MethodPatch, mine, manager, `{"address":"طريق الملك فهد"}`, "If-Match", "1"); r.status != http.StatusOK {
		t.Errorf("manager edits own branch: %d %v", r.status, r.body)
	}
	for name, r := range map[string]response{
		"manager edits another branch": a.do(t, http.MethodPatch, other, manager, `{"address":"x"}`, "If-Match", "1"),
		"barber edits a branch":        a.do(t, http.MethodPatch, mine, barber, `{"address":"x"}`, "If-Match", "2"),
		"manager adds a branch":        a.do(t, http.MethodPost, biz+"/branches", manager, branchJSON),
		"manager invites":              a.do(t, http.MethodPost, biz+"/staff/invitations", manager, `{"phone":"0559999999","display_name":"x","role":"barber","branch_ids":["`+branches[0]+`"]}`),
		"manager lists invitations":    a.do(t, http.MethodGet, biz+"/staff/invitations", manager, ""),
		"manager reads the business":   a.do(t, http.MethodGet, biz, manager, ""),
	} {
		if r.status != http.StatusForbidden || r.body["code"] != "forbidden" {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
}

func TestStaffInvitationsAPIRefusals(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON)
	branch := r.body["id"].(string)
	other, otherBiz := a.register(t, "0556666666", "1010000002")
	otherBranch := a.do(t, http.MethodPost, otherBiz+"/branches", other, branchJSON).body["id"].(string)

	// Revoked: the link stops working.
	r, token := a.invite(t, biz, owner, "0554444444", "barber", branch)
	invitation := biz + "/staff/invitations/" + r.body["id"].(string)
	if r := a.do(t, http.MethodDelete, invitation, owner, ""); r.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodDelete, invitation, owner, ""); r.status != http.StatusConflict || r.body["code"] != "invitation_closed" {
		t.Errorf("revoke twice: %d %v", r.status, r.body)
	}
	invitee := a.signIn(t, "0554444444")
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", invitee, acceptBody(token)); r.status != http.StatusNotFound || r.body["code"] != "invitation_invalid" {
		t.Errorf("revoked link: %d %v", r.status, r.body)
	}

	// Expired: a week later the link stops working.
	_, token = a.invite(t, biz, owner, "0554444444", "barber", branch)
	if _, err := a.pool.Exec(t.Context(), `UPDATE business.invitations SET created_at = created_at - interval '8 days', expires_at = expires_at - interval '8 days' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", invitee, acceptBody(token)); r.status != http.StatusNotFound || r.body["code"] != "invitation_invalid" {
		t.Errorf("expired link: %d %v", r.status, r.body)
	}

	// The owner can't join their own business twice.
	_, token = a.invite(t, biz, owner, "0551234567", "barber", branch)
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", owner, acceptBody(token)); r.status != http.StatusConflict || r.body["code"] != "already_staff" {
		t.Errorf("owner accepts: %d %v", r.status, r.body)
	}

	invite := func(body string) response {
		return a.do(t, http.MethodPost, biz+"/staff/invitations", owner, body)
	}
	for name, tt := range map[string]struct {
		r      response
		status int
	}{
		"owner role":            {invite(`{"phone":"0557777777","display_name":"x","role":"owner","branch_ids":["` + branch + `"]}`), http.StatusBadRequest},
		"no branches":           {invite(`{"phone":"0557777777","display_name":"x","role":"barber","branch_ids":[]}`), http.StatusBadRequest},
		"blank name":            {invite(`{"phone":"0557777777","display_name":"  ","role":"barber","branch_ids":["` + branch + `"]}`), http.StatusUnprocessableEntity},
		"landline":              {invite(`{"phone":"0114567890","display_name":"x","role":"barber","branch_ids":["` + branch + `"]}`), http.StatusUnprocessableEntity},
		"another shop's branch": {invite(`{"phone":"0557777777","display_name":"x","role":"barber","branch_ids":["` + otherBranch + `"]}`), http.StatusUnprocessableEntity},
		"empty token":           {a.do(t, http.MethodPost, "/v1/invitations/accept", invitee, `{"token":""}`), http.StatusBadRequest},
	} {
		if tt.r.status != tt.status || tt.r.body["code"] != "validation_failed" {
			t.Errorf("%s: %d %v, want %d", name, tt.r.status, tt.r.body, tt.status)
		}
	}

	// Another business's owner sees nothing here.
	for name, r := range map[string]response{
		"list staff":       a.do(t, http.MethodGet, biz+"/staff", other, ""),
		"list invitations": a.do(t, http.MethodGet, biz+"/staff/invitations", other, ""),
		"revoke":           a.do(t, http.MethodDelete, invitation, other, ""),
	} {
		if r.status != http.StatusNotFound || r.body["code"] != "not_found" {
			t.Errorf("stranger %s: %d %v", name, r.status, r.body)
		}
	}
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", "", acceptBody(token)); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous accept: %d", r.status)
	}
}
