package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	owner, biz, branch, service, me := a.published(t)
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
		b["location"].(map[string]any)["latitude"] != 24.6911 || b["location"].(map[string]any)["longitude"] != 46.6851 ||
		priceFrom(b) != 6000 || b["price_from"].(map[string]any)["currency"] != "SAR" {
		t.Errorf("listing = %v", b)
	}
	if _, ok := b["open_now"].(bool); !ok {
		t.Errorf("open_now = %v", b["open_now"])
	}

	// Its public page: the listing, its week as the owner set it (09:00–21:00
	// every day), and its menu, a moment after the hours' event.
	branchPage := func(id string) response {
		t.Helper()
		return a.do(t, http.MethodGet, "/v1/branches/"+id, "", "")
	}
	var p map[string]any
	until(t, "the branch's page with its week", func() bool {
		r := branchPage(branch)
		days, _ := r.body["opening_hours"].([]any)
		if r.status == http.StatusOK && len(days) == 7 && len(days[0].(map[string]any)["intervals"].([]any)) == 1 {
			p = r.body
		}
		return p != nil
	})
	sunday := p["opening_hours"].([]any)[0].(map[string]any)
	menu, _ := p["services"].([]any)
	if p["id"] != branch || p["name"].(map[string]any)["ar"] != "فرع العليا" || p["timezone"] != "Asia/Riyadh" ||
		p["phone"] != "+966551234567" || priceFrom(p) != 6000 || sunday["weekday"] != "sunday" ||
		sunday["intervals"].([]any)[0].(map[string]any)["opens"] != "09:00" || sunday["intervals"].([]any)[0].(map[string]any)["closes"] != "21:00" {
		t.Errorf("page = %v", p)
	}
	if _, ok := p["open_now"].(bool); !ok {
		t.Errorf("page open_now = %v", p["open_now"])
	}
	if len(menu) != 1 {
		t.Fatalf("menu = %v", menu)
	}
	if m := menu[0].(map[string]any); m["id"] != service || m["category"] != "haircut" || m["name"].(map[string]any)["ar"] != "قص شعر" ||
		m["name"].(map[string]any)["en"] != "Haircut" ||
		m["duration_minutes"] != float64(30) || m["price_from"].(map[string]any)["amount"] != float64(6000) {
		t.Errorf("menu item = %v", m)
	}
	if r := branchPage(uuid.NewString()); r.status != http.StatusNotFound || r.body["code"] != "not_found" {
		t.Errorf("no such branch: %d %v", r.status, r.body)
	}
	if r := branchPage("not-a-uuid"); r.status != http.StatusBadRequest {
		t.Errorf("not an ID: %d %v", r.status, r.body)
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

	// By name, however it's spelled: ى for ي, capitals, with a city or a
	// place to narrow it.
	for query, found := range map[string]bool{
		"q=العلىا":                    true,
		"q=OLAYA":                     true,
		"q=olaya&city=riyadh":         true,
		"q=olaya&city=jeddah":         false,
		"q=olaya&lat=24.70&lng=46.69": true,
		"q=الملقا":                    false,
		"q=olaya&lat=21.54&lng=39.17": false,
	} {
		if got := near(query); (len(got) == 1 && got[0].(map[string]any)["id"] == branch) != found || (!found && len(got) != 0) {
			t.Errorf("%s: %v", query, got)
		}
	}

	// What it sells: only branches offering a service of the category, in
	// any kind of search, from the least it costs.
	for query, found := range map[string]bool{
		"city=riyadh&category=haircut":                 true,
		"city=riyadh&category=beard":                   false,
		"lat=24.70&lng=46.69&category=haircut":         true,
		"lat=24.70&lng=46.69&category=beard":           false,
		"q=olaya&category=haircut":                     true,
		"q=olaya&category=kids":                        false,
		"q=olaya&lat=24.70&lng=46.69&category=haircut": true,
	} {
		if got := near(query); (len(got) == 1 && got[0].(map[string]any)["id"] == branch) != found || (!found && len(got) != 0) {
			t.Errorf("%s: %v", query, got)
		}
	}
	path := biz + "/branches/" + branch // biz is its path
	services := path + "/services/" + service
	// The owner, its one barber, charges less than the service's price: the
	// price from is theirs.
	if r := a.do(t, http.MethodPut, services+"/offerings", owner,
		`{"offerings":[{"staff_id":"`+me+`","price":{"amount":4500,"currency":"SAR"}}]}`, "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("offerings: %d %v", r.status, r.body)
	}
	until(t, "the owner's own price as the price from", func() bool {
		got := listed("riyadh")
		return len(got) == 1 && priceFrom(got[0]) == 4500
	})
	// Turned off, the branch offers nothing: it isn't shown. On again, it is.
	if r := a.do(t, http.MethodPatch, services, owner, `{"active":false}`, "If-Match", "3"); r.status != http.StatusOK {
		t.Fatalf("turn off: %d %v", r.status, r.body)
	}
	until(t, "a branch offering nothing to go", func() bool { return len(listed("riyadh")) == 0 })
	if r := a.do(t, http.MethodPatch, services, owner, `{"active":true,"category":"beard"}`, "If-Match", "4"); r.status != http.StatusOK {
		t.Fatalf("turn on: %d %v", r.status, r.body)
	}
	until(t, "the branch back, now for beards", func() bool { return len(near("city=riyadh&category=beard")) == 1 })
	if got := near("city=riyadh&category=haircut"); len(got) != 0 {
		t.Errorf("no haircut any more: %v", got)
	}

	// Open now, by the hours the owner sets. Around the clock: open
	// whenever this runs, and kept by open_now=true. Closed all week: never.
	week := func(interval string) string {
		days := ""
		for i, d := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
			if i > 0 {
				days += ","
			}
			days += `{"weekday":"` + d + `","intervals":[` + interval + `]}`
		}
		return `{"days":[` + days + `]}`
	}
	openNow := func() any {
		got := listed("riyadh")
		if len(got) != 1 {
			return nil
		}
		return got[0].(map[string]any)["open_now"]
	}
	if r := a.do(t, http.MethodPut, path+"/opening-hours", owner, week(`{"opens":"00:00","closes":"00:00"}`), "If-Match", "1"); r.status != http.StatusOK {
		t.Fatalf("around the clock: %d %v", r.status, r.body)
	}
	until(t, "open around the clock", func() bool { return openNow() == true })
	for _, query := range []string{"city=riyadh&open_now=true", "lat=24.70&lng=46.69&open_now=true", "q=olaya&open_now=true", "city=riyadh&open_now=false"} {
		if got := near(query); len(got) != 1 || got[0].(map[string]any)["open_now"] != true {
			t.Errorf("%s: %v", query, got)
		}
	}
	if r := a.do(t, http.MethodPut, path+"/opening-hours", owner, `{"days":[]}`, "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("closed all week: %d %v", r.status, r.body)
	}
	until(t, "closed all week", func() bool { return openNow() == false })
	for _, query := range []string{"city=riyadh&open_now=true", "lat=24.70&lng=46.69&open_now=true", "q=olaya&open_now=true"} {
		if got := near(query); len(got) != 0 {
			t.Errorf("%s, closed: %v", query, got)
		}
	}

	// Edited: the listing follows. Unpublished: gone.
	if r := a.do(t, http.MethodPatch, path, owner, `{"name":{"ar":"فرع الملقا","en":"Malqa"}}`, "If-Match", "2"); r.status != http.StatusOK {
		t.Fatalf("edit: %d %v", r.status, r.body)
	}
	until(t, "the new name", func() bool {
		got := listed("riyadh")
		return len(got) == 1 && got[0].(map[string]any)["name"].(map[string]any)["ar"] == "فرع الملقا"
	})
	if got := near("q=الملقا"); len(got) != 1 {
		t.Errorf("the new name, searched: %v", got)
	}
	if got := near("q=olaya"); len(got) != 0 {
		t.Errorf("the old name, searched: %v", got)
	}
	if r := a.do(t, http.MethodPost, path+"/unpublish", owner, "", "If-Match", "3"); r.status != http.StatusOK {
		t.Fatalf("unpublish: %d %v", r.status, r.body)
	}
	until(t, "the unpublished branch to go", func() bool { return len(listed("riyadh")) == 0 })
	if r := branchPage(branch); r.status != http.StatusNotFound {
		t.Errorf("an unpublished branch's page: %d %v", r.status, r.body)
	}

	// A city's listings come 20 a page; next_cursor leads through them.
	for i := range 21 {
		branch, business := uuid.New(), uuid.New()
		if _, err := a.pool.Exec(t.Context(), `INSERT INTO discovery.branch_listings
			(branch_id, business_id, version, listed, name_ar, city_code, address, latitude, longitude, timezone, updated_at, search_text)
			VALUES ($1, $2, 1, true, $3, 'tabuk', 'شارع الأمير', 28.38, 36.57, 'Asia/Riyadh', now(), $3)`,
			branch, business, fmt.Sprintf("صالون %02d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pool.Exec(t.Context(), `INSERT INTO discovery.branch_services
			(service_id, branch_id, business_id, version, offered, category_code, price_from, updated_at)
			VALUES ($1, $2, $3, 1, true, 'haircut', 5000, now())`, uuid.New(), branch, business); err != nil {
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
	// By name: all 21 match "صالون" equally, so the IDs order them.
	seen, shown = map[any]bool{}, 0
	for query, pages := "q=صالون&city=tabuk", 0; ; pages++ {
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
		query = "q=صالون&city=tabuk&cursor=" + url.QueryEscape(cursor)
		if r := a.do(t, http.MethodGet, "/v1/branches?city=tabuk&cursor="+url.QueryEscape(cursor), "", ""); r.status != http.StatusBadRequest {
			t.Errorf("a cursor from a name search, by city: %d %v", r.status, r.body)
		}
	}
	if len(seen) != 21 || shown != 21 {
		t.Errorf("paging by name showed %d, %d different, want 21", shown, len(seen))
	}

	for name, tt := range map[string]struct {
		query  string
		status int
		code   string
	}{
		"a city not on the list": {"city=atlantis", http.StatusUnprocessableEntity, "unknown_city"},
		"a category not on it":   {"city=riyadh&category=massage", http.StatusUnprocessableEntity, "unknown_category"},
		"a category alone":       {"category=haircut", http.StatusBadRequest, "validation_failed"},
		"open_now not a boolean": {"city=riyadh&open_now=yes", http.StatusBadRequest, "validation_failed"},
		"a city in capitals":     {"city=Riyadh", http.StatusBadRequest, "validation_failed"},
		"no city":                {"", http.StatusBadRequest, "validation_failed"},
		"a one-letter name":      {"q=x", http.StatusBadRequest, "validation_failed"},
		"a name of punctuation":  {"q=" + url.QueryEscape("!!"), http.StatusBadRequest, "validation_failed"},
		"a name too long":        {"q=" + strings.Repeat("a", 61), http.StatusBadRequest, "validation_failed"},
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

// priceFrom is a listing's price_from amount, in halalas; -1 if it has none.
func priceFrom(listing any) float64 {
	p, ok := listing.(map[string]any)["price_from"].(map[string]any)
	if !ok {
		return -1
	}
	amount, _ := p["amount"].(float64)
	return amount
}
