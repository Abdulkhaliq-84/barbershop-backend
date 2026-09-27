package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestNewBookingPolicy(t *testing.T) {
	t.Parallel()
	if _, err := domain.NewBookingPolicy(domain.DefaultBookingPolicy().Rules()); err != nil {
		t.Fatalf("the defaults must be valid: %v", err)
	}

	tests := []struct {
		name   string
		change func(*domain.PolicyRules)
		ok     bool
	}{
		{"no lead time", func(r *domain.PolicyRules) { r.MinLead = 0 }, true},
		{"lead time 7 days", func(r *domain.PolicyRules) { r.MinLead = 7 * 24 * time.Hour }, true},
		{"lead time over 7 days", func(r *domain.PolicyRules) { r.MinLead = 7*24*time.Hour + time.Minute }, false},
		{"lead time in seconds", func(r *domain.PolicyRules) { r.MinLead = 90 * time.Second }, false},
		{"negative lead time", func(r *domain.PolicyRules) { r.MinLead = -time.Minute }, false},
		{"horizon 1 day", func(r *domain.PolicyRules) { r.HorizonDays = 1 }, true},
		{"horizon 180 days", func(r *domain.PolicyRules) { r.HorizonDays = 180 }, true},
		{"horizon 0", func(r *domain.PolicyRules) { r.HorizonDays = 0 }, false},
		{"horizon 181", func(r *domain.PolicyRules) { r.HorizonDays = 181 }, false},
		{"slot every 20 min", func(r *domain.PolicyRules) { r.SlotInterval = 20 * time.Minute }, true},
		{"slot every 25 min", func(r *domain.PolicyRules) { r.SlotInterval = 25 * time.Minute }, false},
		{"buffer 60 min", func(r *domain.PolicyRules) { r.Buffer = time.Hour }, true},
		{"buffer 61 min", func(r *domain.PolicyRules) { r.Buffer = 61 * time.Minute }, false},
		{"cancel until start", func(r *domain.PolicyRules) { r.CancellationWindow = 0 }, true},
		{"cancel window 49 h", func(r *domain.PolicyRules) { r.CancellationWindow = 49 * time.Hour }, false},
		{"pending expiry 4 min", func(r *domain.PolicyRules) { r.PendingExpiry = 4 * time.Minute }, false},
		{"pending expiry 2 h", func(r *domain.PolicyRules) { r.PendingExpiry = 2 * time.Hour }, true},
		{"no active bookings", func(r *domain.PolicyRules) { r.MaxActiveBookings = 0 }, false},
		{"11 active bookings", func(r *domain.PolicyRules) { r.MaxActiveBookings = 11 }, false},
		{"shop confirms", func(r *domain.PolicyRules) { r.AutoConfirm = false }, true},
	}
	for _, tt := range tests {
		r := domain.DefaultBookingPolicy().Rules()
		tt.change(&r)
		p, err := domain.NewBookingPolicy(r)
		if tt.ok && (err != nil || p.Rules() != r) {
			t.Errorf("%s: %v, %v", tt.name, p.Rules(), err)
		}
		var pe *domain.PolicyError
		if !tt.ok && (!errors.Is(err, domain.ErrInvalidBookingPolicy) || !errors.As(err, &pe) || pe.Rule == "") {
			t.Errorf("%s: error = %v, want a PolicyError", tt.name, err)
		}
	}
}

func profile(t *testing.T) domain.BranchProfile {
	t.Helper()
	name, _ := shared.NewLocalizedText("فرع العليا", "Olaya")
	loc, _ := shared.NewGeoPoint(24.6911, 46.6851)
	phone, _ := shared.NewPhoneNumber("0551234567")
	return domain.BranchProfile{
		Name: name, City: "riyadh", District: " العليا ", Address: " شارع العليا العام ",
		Location: loc, Phone: phone, Timezone: "Asia/Riyadh",
	}
}

func TestNewBranch(t *testing.T) {
	t.Parallel()
	id, biz := shared.NewID[shared.BranchTag](), shared.NewID[shared.BusinessTag]()
	b, err := domain.NewBranch(id, biz, profile(t), domain.BookingPolicy{}, t0.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	p := b.Profile()
	if b.ID() != id || b.BusinessID() != biz || b.Status() != domain.BranchDraft || b.Version() != 1 ||
		p.District != "العليا" || p.Address != "شارع العليا العام" || !b.CreatedAt().Equal(t0) {
		t.Errorf("branch = %+v", b)
	}
	// No policy given: the defaults.
	if b.Policy() != domain.DefaultBookingPolicy() {
		t.Errorf("policy = %+v, want the defaults", b.Policy().Rules())
	}
}

func TestNewBranchRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.BranchProfile)
		want   error
	}{
		{"no arabic name", func(p *domain.BranchProfile) { p.Name = shared.LocalizedText{} }, shared.ErrArabicRequired},
		{"name too long", func(p *domain.BranchProfile) { p.Name, _ = shared.NewLocalizedText(strings.Repeat("ب", 101), "") }, domain.ErrTextTooLong},
		{"city with capitals", func(p *domain.BranchProfile) { p.City = "Riyadh" }, domain.ErrInvalidCityCode},
		{"city in arabic", func(p *domain.BranchProfile) { p.City = "الرياض" }, domain.ErrInvalidCityCode},
		{"no city", func(p *domain.BranchProfile) { p.City = "" }, domain.ErrInvalidCityCode},
		{"blank address", func(p *domain.BranchProfile) { p.Address = "  " }, domain.ErrAddressRequired},
		{"address too long", func(p *domain.BranchProfile) { p.Address = strings.Repeat("ب", 201) }, domain.ErrTextTooLong},
		{"district too long", func(p *domain.BranchProfile) { p.District = strings.Repeat("ب", 81) }, domain.ErrTextTooLong},
		{"no location", func(p *domain.BranchProfile) { p.Location = shared.GeoPoint{} }, shared.ErrInvalidCoordinates},
		{"no timezone", func(p *domain.BranchProfile) { p.Timezone = "" }, domain.ErrInvalidTimezone},
		{"server's timezone", func(p *domain.BranchProfile) { p.Timezone = "Local" }, domain.ErrInvalidTimezone},
		{"made-up timezone", func(p *domain.BranchProfile) { p.Timezone = "Asia/Atlantis" }, domain.ErrInvalidTimezone},
	}
	for _, tt := range tests {
		p := profile(t)
		tt.change(&p)
		if _, err := domain.NewBranch(shared.NewID[shared.BranchTag](), shared.NewID[shared.BusinessTag](), p, domain.BookingPolicy{}, t0); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	// No phone is fine.
	p := profile(t)
	p.Phone = shared.PhoneNumber{}
	if _, err := domain.NewBranch(shared.NewID[shared.BranchTag](), shared.NewID[shared.BusinessTag](), p, domain.BookingPolicy{}, t0); err != nil {
		t.Errorf("no phone: %v", err)
	}
}

func TestBranchEdit(t *testing.T) {
	t.Parallel()
	b, err := domain.NewBranch(shared.NewID[shared.BranchTag](), shared.NewID[shared.BusinessTag](), profile(t), domain.BookingPolicy{}, t0)
	if err != nil {
		t.Fatal(err)
	}
	rules := b.Policy().Rules()
	rules.AutoConfirm = false
	policy, _ := domain.NewBookingPolicy(rules)
	p := b.Profile()
	p.Address = "طريق الملك فهد"

	if err := b.Edit(p, policy, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if b.Profile().Address != "طريق الملك فهد" || b.Policy().Rules().AutoConfirm || b.Version() != 2 || !b.UpdatedAt().Equal(t0.Add(time.Hour)) {
		t.Errorf("after edit: %+v", b)
	}

	// A refused edit changes nothing.
	p.City = "Bad City"
	if err := b.Edit(p, policy, t0.Add(2*time.Hour)); !errors.Is(err, domain.ErrInvalidCityCode) {
		t.Fatalf("error = %v", err)
	}
	if err := b.Edit(b.Profile(), domain.BookingPolicy{}, t0.Add(2*time.Hour)); !errors.Is(err, domain.ErrInvalidBookingPolicy) {
		t.Fatalf("missing policy: error = %v", err)
	}
	if b.Version() != 2 || b.Profile().City != "riyadh" {
		t.Fatalf("a refused edit changed the branch: %+v", b)
	}
}
