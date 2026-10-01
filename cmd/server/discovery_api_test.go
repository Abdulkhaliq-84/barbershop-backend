package main

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

// until polls ok until it holds or 15 seconds pass. Discovery hears about
// branches through the outbox worker, a moment after the change.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDiscoveryAPI(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	listed := func(city string) []any {
		t.Helper()
		r := a.do(t, http.MethodGet, "/v1/branches?city="+city, "", "")
		if r.status != http.StatusOK {
			t.Fatalf("search %s: %d %v", city, r.status, r.body)
		}
		data, _ := r.body["data"].([]any)
		return data
	}

	// Cities are public reference data, the largest first.
	r := a.do(t, http.MethodGet, "/v1/cities", "", "")
	cities, _ := r.body["data"].([]any)
	if r.status != http.StatusOK || len(cities) < 20 {
		t.Fatalf("cities: %d %v", r.status, r.body)
	}
	if first := cities[0].(map[string]any); first["code"] != "riyadh" || first["name"].(map[string]any)["ar"] != "الرياض" {
		t.Errorf("first city = %v", first)
	}
	if got := listed("riyadh"); len(got) != 0 {
		t.Fatalf("listed before anything is published: %v", got)
	}

	// Published: listed, a moment later, with what customers need.
	owner, biz, branch, _, _ := a.published(t)
	var b map[string]any
	until(t, "the published branch to be listed", func() bool {
		got := listed("riyadh")
		if len(got) == 1 {
			b = got[0].(map[string]any)
		}
		return b != nil
	})
	if b["id"] != branch || b["name"].(map[string]any)["ar"] != "فرع العليا" || b["district"] != "العليا" || b["address"] != "شارع العليا العام" ||
		b["city"].(map[string]any)["code"] != "riyadh" || b["city"].(map[string]any)["name"].(map[string]any)["en"] != "Riyadh" ||
		b["location"].(map[string]any)["latitude"] != 24.6911 || b["location"].(map[string]any)["longitude"] != 46.6851 {
		t.Errorf("listing = %v", b)
	}
	// Kept for the branch's public page (M6.5), not listed yet.
	var phone, timezone string
	if err := a.pool.QueryRow(t.Context(), `SELECT phone, timezone FROM discovery.branch_listings WHERE branch_id = $1`, branch).Scan(&phone, &timezone); err != nil ||
		phone != "+966551234567" || timezone != "Asia/Riyadh" {
		t.Errorf("kept phone %q, timezone %q, %v", phone, timezone, err)
	}

	// Near a place: within the radius, with the distance in metres. About
	// 1.1 km from the branch; nothing near Jeddah; nothing filed under
	// Jeddah near Riyadh.
	near := func(query string) []any {
		t.Helper()
		r := a.do(t, http.MethodGet, "/v1/branches?"+query, "", "")
		if r.status != http.StatusOK {
			t.Fatalf("near %s: %d %v", query, r.status, r.body)
		}
		data, _ := r.body["data"].([]any)
		return data
	}
	if got := near("lat=24.70&lng=46.69"); len(got) != 1 || got[0].(map[string]any)["id"] != branch {
		t.Errorf("near the branch: %v", got)
	} else if d, _ := got[0].(map[string]any)["distance_m"].(float64); d < 1050 || d > 1160 {
		t.Errorf("distance = %v m, want about 1,100", got[0].(map[string]any)["distance_m"])
	}
	if got := near("lat=24.70&lng=46.69&radius_km=1"); len(got) != 0 {
		t.Errorf("within 1 km: %v", got)
	}
	if got := near("lat=21.54&lng=39.17&radius_km=50"); len(got) != 0 {
		t.Errorf("near Jeddah: %v", got)
	}
	if got := near("lat=24.70&lng=46.69&city=jeddah"); len(got) != 0 {
		t.Errorf("filed under Jeddah: %v", got)
	}
	if got := listed("riyadh"); len(got) != 1 || got[0].(map[string]any)["distance_m"] != nil {
		t.Errorf("by city, a distance: %v", got)
	}

	// Edited: the listing follows. Unpublished: gone.
	path := biz + "/branches/" + branch // biz is its path
	if r := a.do(t, http.MethodPatch, path, owner, `{"name":{"ar":"فرع الملقا","en":"Malqa"}}`, "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("edit: %d %v", r.status, r.body)
	}
	until(t, "the new name", func() bool {
		got := listed("riyadh")
		return len(got) == 1 && got[0].(map[string]any)["name"].(map[string]any)["ar"] == "فرع الملقا"
	})
	if r := a.do(t, http.MethodPost, path+"/unpublish", owner, "", "If-Match", "3"); r.status != http.StatusOK {
		t.Fatalf("unpublish: %d %v", r.status, r.body)
	}
	until(t, "the unpublished branch to go", func() bool { return len(listed("riyadh")) == 0 })

	// A city's listings come 20 a page; next_cursor leads through them.
	for i := range 21 {
		if _, err := a.pool.Exec(t.Context(), `INSERT INTO discovery.branch_listings
			(branch_id, business_id, version, listed, name_ar, city_code, address, latitude, longitude, timezone, updated_at)
			VALUES ($1, $2, 1, true, $3, 'tabuk', 'شارع الأمير', 28.38, 36.57, 'Asia/Riyadh', now())`,
			uuid.New(), uuid.New(), fmt.Sprintf("صالون %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	page := a.do(t, http.MethodGet, "/v1/branches?city=tabuk", "", "")
	first, _ := page.body["data"].([]any)
	next, _ := page.body["next_cursor"].(string)
	if page.status != http.StatusOK || len(first) != 20 || next == "" || first[0].(map[string]any)["name"].(map[string]any)["ar"] != "صالون 00" {
		t.Fatalf("first page: %d, %d listings, next %q", page.status, len(first), next)
	}
	page = a.do(t, http.MethodGet, "/v1/branches?city=tabuk&cursor="+url.QueryEscape(next), "", "")
	rest, _ := page.body["data"].([]any)
	if page.status != http.StatusOK || len(rest) != 1 || page.body["next_cursor"] != nil || rest[0].(map[string]any)["name"].(map[string]any)["ar"] != "صالون 20" {
		t.Errorf("last page: %d %v", page.status, page.body)
	}
	// A cursor belongs to its search.
	if r := a.do(t, http.MethodGet, "/v1/branches?lat=28.39&lng=36.57&cursor="+url.QueryEscape(next), "", ""); r.status != http.StatusBadRequest {
		t.Errorf("a city's cursor near a place: %d %v", r.status, r.body)
	}
	// Near them: all 21 are at one place, so the IDs order them; two pages
	// show each once.
	seen, shown := map[any]bool{}, 0
	for query, pages := "lat=28.39&lng=36.57", 0; ; pages++ {
		r := a.do(t, http.MethodGet, "/v1/branches?"+query, "", "")
		data, _ := r.body["data"].([]any)
		for _, x := range data {
			seen[x.(map[string]any)["id"]] = true
		}
		shown += len(data)
		cursor, _ := r.body["next_cursor"].(string)
		if r.status != http.StatusOK || cursor == "" || pages > 3 {
			break
		}
		query = "lat=28.39&lng=36.57&cursor=" + url.QueryEscape(cursor)
		if r := a.do(t, http.MethodGet, "/v1/branches?city=tabuk&cursor="+url.QueryEscape(cursor), "", ""); r.status != http.StatusBadRequest {
			t.Errorf("a cursor from near a place, by city: %d %v", r.status, r.body)
		}
	}
	if len(seen) != 21 || shown != 21 {
		t.Errorf("paging near them showed %d, %d different, want 21", shown, len(seen))
	}

	for name, tt := range map[string]struct {
		query  string
		status int
		code   string
	}{
		"a city not on the list": {"city=atlantis", http.StatusUnprocessableEntity, "unknown_city"},
		"a city in capitals":     {"city=Riyadh", http.StatusBadRequest, "validation_failed"},
		"no city":                {"", http.StatusBadRequest, "validation_failed"},
		"lat without lng":        {"lat=24.7", http.StatusBadRequest, "validation_failed"},
		"a radius alone":         {"city=riyadh&radius_km=5", http.StatusBadRequest, "validation_failed"},
		"a radius of 51 km":      {"lat=24.7&lng=46.7&radius_km=51", http.StatusBadRequest, "validation_failed"},
		"a latitude off Earth":   {"lat=91&lng=46.7", http.StatusBadRequest, "validation_failed"},
		"a made-up cursor":       {"city=riyadh&cursor=bm90LWEtY3Vyc29y", http.StatusBadRequest, "validation_failed"},
	} {
		if r := a.do(t, http.MethodGet, "/v1/branches?"+tt.query, "", ""); r.status != tt.status || r.body["code"] != tt.code {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tt.status, tt.code)
		}
	}
}
