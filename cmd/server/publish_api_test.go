package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestPublishBranchAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.approved(t, "0551234567", "1010000001", "0500000001")
	id := a.firstBranch(t, biz, owner)
	branch := biz + "/branches/" + id
	var me string
	for _, s := range a.do(t, http.MethodGet, biz+"/staff", owner, "").body["data"].([]any) {
		if m := s.(map[string]any); m["role"] == "owner" {
			me = m["id"].(string)
		}
	}
	publish := func(version string) response {
		return a.do(t, http.MethodPost, branch+"/publish", owner, "", "If-Match", version)
	}

	// Nothing set up: the problem says what's missing.
	r := publish("1")
	if detail, _ := r.body["detail"].(string); r.status != http.StatusConflict || r.body["code"] != "branch_not_ready" ||
		!strings.Contains(detail, "opening hours") || !strings.Contains(detail, "add a service") {
		t.Fatalf("empty branch: %d %v", r.status, r.body)
	}
	// Opening hours, and a haircut the owner performs...
	if r := a.do(t, http.MethodPut, branch+"/opening-hours", owner, `{"days":[{"weekday":"sunday","intervals":[{"opens":"09:00","closes":"21:00"}]}]}`, "If-Match", "0"); r.status != http.StatusOK {
		t.Fatalf("hours: %d %v", r.status, r.body)
	}
	service := a.do(t, http.MethodPost, branch+"/services", owner, serviceJSON).body["id"].(string)
	if r := a.do(t, http.MethodPut, branch+"/services/"+service+"/offerings", owner, `{"offerings":[{"staff_id":"`+me+`"}]}`, "If-Match", "1"); r.status != http.StatusOK {
		t.Fatalf("offering: %d %v", r.status, r.body)
	}
	// ...but nobody who performs it has a schedule yet.
	if r := publish("1"); r.status != http.StatusConflict || !strings.Contains(r.body["detail"].(string), "weekly schedule") {
		t.Fatalf("no schedule: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, branch+"/staff/"+me+"/schedule", owner, `{"weekly":[{"weekday":"sunday","intervals":[{"opens":"10:00","closes":"18:00"}]}]}`, "If-Match", "0"); r.status != http.StatusOK {
		t.Fatalf("schedule: %d %v", r.status, r.body)
	}

	// Only the owner publishes.
	_, token := a.invite(t, biz, owner, "0552222222", "manager", id)
	manager := a.signIn(t, "0552222222")
	a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(token))
	if r := a.do(t, http.MethodPost, branch+"/publish", manager, "", "If-Match", "1"); r.status != http.StatusForbidden {
		t.Errorf("manager publishes: %d %v", r.status, r.body)
	}
	// Ready: published.
	if r := publish("1"); r.status != http.StatusOK || r.body["status"] != "published" || r.body["version"] != float64(2) {
		t.Fatalf("publish: %d %v", r.status, r.body)
	}
	if r := publish("2"); r.status != http.StatusConflict || r.body["code"] != "invalid_state_transition" {
		t.Errorf("publish twice: %d %v", r.status, r.body)
	}
	// The event is on its way to discovery (M6), in the same transaction.
	var events int
	if err := a.pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job WHERE kind = 'outbox_event' AND args->'event'->>'type' = 'business.branch_published'`).Scan(&events); err != nil || events != 1 {
		t.Errorf("branch_published events = %d, %v", events, err)
	}

	// Unpublish, then again.
	if r := a.do(t, http.MethodPost, branch+"/unpublish", owner, "", "If-Match", "2"); r.status != http.StatusOK || r.body["status"] != "unpublished" {
		t.Errorf("unpublish: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, branch+"/unpublish", owner, "", "If-Match", "3"); r.status != http.StatusConflict {
		t.Errorf("unpublish twice: %d %v", r.status, r.body)
	}

	// A business not approved yet can't publish; another business can't
	// touch this branch.
	draftOwner, draft := a.register(t, "0553333333", "1010000002")
	draftBranch := a.do(t, http.MethodPost, draft+"/branches", draftOwner, branchJSON).body["id"].(string)
	if r := a.do(t, http.MethodPost, draft+"/branches/"+draftBranch+"/publish", draftOwner, "", "If-Match", "1"); r.status != http.StatusConflict || r.body["code"] != "business_not_active" {
		t.Errorf("draft business: %d %v", r.status, r.body)
	}
	for name, path := range map[string]string{"direct": branch + "/publish", "under own business": draft + "/branches/" + id + "/publish"} {
		if r := a.do(t, http.MethodPost, path, draftOwner, "", "If-Match", "3"); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
}
