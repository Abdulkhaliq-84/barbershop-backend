package main

import (
	"net/http"
	"strings"
	"testing"
)

const serviceJSON = `{"category":"haircut","name":{"ar":"قص شعر","en":"Haircut"},"description":{"ar":"قص وتصفيف"},"duration_minutes":30,"price":{"amount":6000,"currency":"SAR"}}`

func TestServiceCategoriesArePublic(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	r := a.do(t, http.MethodGet, "/v1/service-categories", "", "")
	data, _ := r.body["data"].([]any)
	if r.status != http.StatusOK || len(data) != 7 {
		t.Fatalf("categories: %d %v", r.status, r.body)
	}
	if first := data[0].(map[string]any); first["code"] != "haircut" || first["icon"] != "scissors" {
		t.Errorf("first category = %v", first)
	}
}

func TestServicesAPI(t *testing.T) {
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
	mine, other := biz+"/branches/"+branches[0]+"/services", biz+"/branches/"+branches[1]+"/services"

	// The owner adds a service.
	r := a.do(t, http.MethodPost, mine, owner, serviceJSON)
	price, _ := r.body["price"].(map[string]any)
	if r.status != http.StatusCreated || r.body["active"] != true || r.body["version"] != float64(1) ||
		price["amount"] != float64(6000) || price["currency"] != "SAR" || r.body["duration_minutes"] != float64(30) {
		t.Fatalf("create: %d %v", r.status, r.body)
	}
	service := mine + "/" + r.body["id"].(string)

	// A manager of the first branch and a barber join.
	_, token := a.invite(t, biz, owner, "0552222222", "manager", branches[0])
	manager := a.signIn(t, "0552222222")
	a.do(t, http.MethodPost, "/v1/invitations/accept", manager, acceptBody(token))
	_, token = a.invite(t, biz, owner, "0553333333", "barber", branches[0])
	barber := a.signIn(t, "0553333333")
	a.do(t, http.MethodPost, "/v1/invitations/accept", barber, acceptBody(token))

	// The manager runs their branch's menu, not the other branch's.
	beard := strings.Replace(serviceJSON, `"haircut"`, `"beard"`, 1)
	if r := a.do(t, http.MethodPost, mine, manager, beard); r.status != http.StatusCreated {
		t.Errorf("manager adds to own branch: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, other, manager, serviceJSON); r.status != http.StatusForbidden {
		t.Errorf("manager adds to another branch: %d %v", r.status, r.body)
	}
	// The barber reads the menu but can't change it.
	r = a.do(t, http.MethodGet, mine, barber, "")
	if data, _ := r.body["data"].([]any); r.status != http.StatusOK || len(data) != 2 {
		t.Fatalf("barber lists: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, mine, barber, serviceJSON); r.status != http.StatusForbidden {
		t.Errorf("barber adds: %d %v", r.status, r.body)
	}

	// Deactivate; a stale version is refused.
	r = a.do(t, http.MethodPatch, service, manager, `{"active":false,"price":{"amount":7500,"currency":"SAR"}}`, "If-Match", `"1"`)
	price, _ = r.body["price"].(map[string]any)
	if r.status != http.StatusOK || r.body["active"] != false || r.body["version"] != float64(2) || price["amount"] != float64(7500) || r.body["name"] == nil {
		t.Fatalf("patch: %d %v", r.status, r.body)
	}
	if r := a.do(t, http.MethodPatch, service, manager, `{"active":true}`, "If-Match", "1"); r.status != http.StatusPreconditionFailed || r.body["code"] != "version_conflict" {
		t.Errorf("stale patch: %d %v", r.status, r.body)
	}
	// A service is only found under its own branch.
	wrongBranch := other + "/" + strings.TrimPrefix(service, mine+"/")
	if r := a.do(t, http.MethodPatch, wrongBranch, owner, `{"active":true}`, "If-Match", "2"); r.status != http.StatusNotFound {
		t.Errorf("service under another branch: %d %v", r.status, r.body)
	}

	// Invalid input.
	for name, tt := range map[string]struct {
		body   string
		status int
	}{
		"32 minutes":     {strings.Replace(serviceJSON, `"duration_minutes":30`, `"duration_minutes":32`, 1), http.StatusBadRequest},
		"dollars":        {strings.Replace(serviceJSON, `"SAR"`, `"USD"`, 1), http.StatusBadRequest},
		"unknown field":  {strings.Replace(serviceJSON, `"category"`, `"colour_hex":"#fff","category"`, 1), http.StatusBadRequest},
		"negative price": {strings.Replace(serviceJSON, `"amount":6000`, `"amount":-1`, 1), http.StatusUnprocessableEntity},
		"massage":        {strings.Replace(serviceJSON, `"haircut"`, `"massage"`, 1), http.StatusUnprocessableEntity},
		"no Arabic name": {strings.Replace(serviceJSON, `"ar":"قص شعر"`, `"ar":" "`, 1), http.StatusUnprocessableEntity},
	} {
		if r := a.do(t, http.MethodPost, mine, owner, tt.body); r.status != tt.status || r.body["code"] != "validation_failed" {
			t.Errorf("%s: %d %v, want %d", name, r.status, r.body, tt.status)
		}
	}
}

// Another business's owner can't see or change this menu — not even by
// putting this branch's ID under their own business.
func TestServicesAreIsolatedBetweenBusinesses(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	owner, biz := a.register(t, "0551234567", "1010000001")
	branch := a.do(t, http.MethodPost, biz+"/branches", owner, branchJSON).body["id"].(string)
	services := biz + "/branches/" + branch + "/services"
	service := services + "/" + a.do(t, http.MethodPost, services, owner, serviceJSON).body["id"].(string)

	other, otherBiz := a.register(t, "0559876543", "1010000002")
	for name, r := range map[string]response{
		"list":                 a.do(t, http.MethodGet, services, other, ""),
		"create":               a.do(t, http.MethodPost, services, other, serviceJSON),
		"patch":                a.do(t, http.MethodPatch, service, other, `{"active":false}`, "If-Match", "1"),
		"branch under own biz": a.do(t, http.MethodPost, otherBiz+"/branches/"+branch+"/services", other, serviceJSON),
	} {
		if r.status != http.StatusNotFound || r.body["code"] != "not_found" {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
	if r := a.do(t, http.MethodGet, services, "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", r.status)
	}
}
