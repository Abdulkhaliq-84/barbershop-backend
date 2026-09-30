package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// approved registers, readies, submits and approves a business, and
// returns the owner's token and the business path.
func (a *api) approved(t *testing.T, phone, cr, adminPhone string) (owner, biz string) {
	t.Helper()
	owner, biz = a.register(t, phone, cr)
	if r := a.upload(t, biz, owner, pdfFile(1000)); r.status != http.StatusCreated {
		t.Fatalf("upload: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON); r.status != http.StatusCreated {
		t.Fatalf("branch: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, biz+"/verification/submit", owner, "", "If-Match", "1"); r.status != http.StatusOK {
		t.Fatalf("submit: %d %v", r.status, r.body)
	}
	admin := a.admin(t, adminPhone)
	if r := a.do(t, http.MethodPost, "/v1/admin/businesses/"+strings.TrimPrefix(biz, "/v1/businesses/")+"/approve", admin, "", "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("approve: %d %v", r.status, r.body)
	}
	return owner, biz
}

// subscription polls until the subscription reaches status: the trial is
// started by the worker, a moment after the approval.
func (a *api) subscription(t *testing.T, biz, owner, status string) response {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := a.do(t, http.MethodGet, biz+"/subscription", owner, "")
		if r.status == http.StatusOK && r.body["status"] == status {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscription never became %s: %d %v", status, r.status, r.body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSubscriptionAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)

	// Before approval: setting up, under the Pro plan's limits.
	owner, biz := a.register(t, "0551234567", "1010000001")
	r := a.do(t, http.MethodGet, biz+"/subscription", owner, "")
	plan, _ := r.body["plan"].(map[string]any)
	if r.status != http.StatusOK || r.body["status"] != "setup" || plan["code"] != "pro" || r.body["max_branches"] != float64(5) ||
		r.body["max_staff"] != float64(30) || r.body["trial_ends_at"] != nil {
		t.Fatalf("before approval: %d %v", r.status, r.body)
	}

	// Approval starts the 30-day trial, through the outbox and the worker.
	owner, biz = a.approved(t, "0552222222", "1010000002", "0500000001")
	r = a.subscription(t, biz, owner, "trialing")
	var reviewedAt time.Time
	if err := a.pool.QueryRow(t.Context(), `SELECT reviewed_at FROM business.businesses WHERE id = $1`, strings.TrimPrefix(biz, "/v1/businesses/")).Scan(&reviewedAt); err != nil {
		t.Fatal(err)
	}
	ends, err := time.Parse(time.RFC3339Nano, r.body["trial_ends_at"].(string))
	if err != nil || !ends.Equal(reviewedAt.Add(30*24*time.Hour)) {
		t.Errorf("trial_ends_at = %v, want approval + 30 days (%v)", r.body["trial_ends_at"], reviewedAt.Add(30*24*time.Hour))
	}

	// Only the owner sees the plan.
	_, token := a.invite(t, biz, owner, "0553333333", "manager", a.firstBranch(t, biz, owner))
	manager := a.signIn(t, "0553333333")
	if r := a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(token)); r.status != http.StatusOK {
		t.Fatalf("accept: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, biz+"/subscription", manager, ""); r.status != http.StatusForbidden {
		t.Errorf("manager reads the plan: %d %v", r.status, r.body)
	}

	// The trial ends: Free — 1 branch (it has one), 3 staff (one used).
	if _, err := a.pool.Exec(t.Context(), `UPDATE billing.subscriptions SET created_at = created_at - interval '31 days', current_period_end = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	r = a.do(t, http.MethodGet, biz+"/subscription", owner, "")
	if r.body["status"] != "free" || r.body["max_branches"] != float64(1) || r.body["max_staff"] != float64(3) {
		t.Fatalf("after the trial: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON); r.status != http.StatusConflict || r.body["code"] != "plan_limit_reached" {
		t.Errorf("second branch on Free: %d %v", r.status, r.body)
	}
	branch := a.firstBranch(t, biz, owner)
	a.invite(t, biz, owner, "0554444444", "barber", branch)
	a.invite(t, biz, owner, "0555555555", "barber", branch)
	r = a.do(t, http.MethodPost, biz+"/staff/invitations", owner, `{"phone":"0556666666","display_name":"x","role":"barber","branch_ids":["`+branch+`"]}`)
	if r.status != http.StatusConflict || r.body["code"] != "plan_limit_reached" {
		t.Errorf("fourth person on Free: %d %v", r.status, r.body)
	}
	// Nothing was taken away: the manager still works there.
	if r := a.do(t, http.MethodGet, biz+"/staff", manager, ""); r.status != http.StatusOK {
		t.Errorf("manager after the trial: %d", r.status)
	}
}

func (a *api) firstBranch(t *testing.T, biz, token string) string {
	t.Helper()
	r := a.do(t, http.MethodGet, biz+"/branches", token, "")
	data, _ := r.body["data"].([]any)
	if len(data) == 0 {
		t.Fatalf("no branches: %d %v", r.status, r.body)
	}
	return data[0].(map[string]any)["id"].(string)
}
