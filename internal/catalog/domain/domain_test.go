package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 123456789, time.UTC)

func details(t *testing.T) domain.ServiceDetails {
	t.Helper()
	name, err := shared.NewLocalizedText("قص شعر", "Haircut")
	if err != nil {
		t.Fatal(err)
	}
	return domain.ServiceDetails{Category: "haircut", Name: name, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	cats := domain.Categories()
	if len(cats) != 7 || cats[0].Code != "haircut" || cats[0].Name.Ar() == "" || cats[0].Icon == "" {
		t.Fatalf("categories = %+v", cats)
	}
	cats[0].Code = "changed" // a copy: the reference data can't be changed from outside
	if domain.Categories()[0].Code != "haircut" {
		t.Error("Categories returned the shared slice")
	}
	if _, err := domain.ParseCategory("massage"); !errors.Is(err, domain.ErrUnknownCategory) {
		t.Errorf("unknown category: %v", err)
	}
}

func TestNewService(t *testing.T) {
	t.Parallel()
	d := details(t)
	d.Description = domain.Description{Ar: "  قص وتصفيف  ", En: " "}
	s, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), d, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Details()
	if !s.IsActive() || s.Version() != 1 || !s.CreatedAt().Equal(t0.Truncate(time.Microsecond)) ||
		got.Description != (domain.Description{Ar: "قص وتصفيف"}) || got.Price.Amount() != 6000 {
		t.Errorf("service = %+v, details %+v", s, got)
	}

	sar := func(n int64) shared.Money { m, _ := shared.NewMoney(n, shared.SAR); return m }
	tests := []struct {
		name   string
		change func(*domain.ServiceDetails)
		want   error
	}{
		{"unknown category", func(d *domain.ServiceDetails) { d.Category = "massage" }, domain.ErrUnknownCategory},
		{"long Arabic name", func(d *domain.ServiceDetails) { d.Name, _ = shared.NewLocalizedText(strings.Repeat("ق", 81), "") }, domain.ErrNameTooLong},
		{"long English name", func(d *domain.ServiceDetails) { d.Name, _ = shared.NewLocalizedText("قص", strings.Repeat("x", 81)) }, domain.ErrNameTooLong},
		{"long description", func(d *domain.ServiceDetails) { d.Description.En = strings.Repeat("x", 501) }, domain.ErrTextTooLong},
		{"too short", func(d *domain.ServiceDetails) { d.Duration = 0 }, domain.ErrInvalidDuration},
		{"too long", func(d *domain.ServiceDetails) { d.Duration = 485 * time.Minute }, domain.ErrInvalidDuration},
		{"not a 5-minute step", func(d *domain.ServiceDetails) { d.Duration = 32 * time.Minute }, domain.ErrInvalidDuration},
		{"negative price", func(d *domain.ServiceDetails) { d.Price = sar(-1) }, domain.ErrInvalidPrice},
		{"absurd price", func(d *domain.ServiceDetails) { d.Price = sar(10_000_001) }, domain.ErrInvalidPrice},
		{"no currency", func(d *domain.ServiceDetails) { d.Price = shared.Money{} }, domain.ErrInvalidPrice},
		{"negative sort", func(d *domain.ServiceDetails) { d.SortOrder = -1 }, domain.ErrInvalidSort},
		{"huge sort", func(d *domain.ServiceDetails) { d.SortOrder = 1001 }, domain.ErrInvalidSort},
	}
	for _, tt := range tests {
		d := details(t)
		tt.change(&d)
		if _, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), d, t0); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	// The limits themselves are allowed.
	d = details(t)
	d.Duration, d.Price, d.SortOrder = 480*time.Minute, sar(10_000_000), 1000
	d.Name, _ = shared.NewLocalizedText(strings.Repeat("ق", 80), strings.Repeat("x", 80))
	if _, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), d, t0); err != nil {
		t.Errorf("at the limits: %v", err)
	}
	d.Duration, d.Price = 5*time.Minute, sar(0)
	if _, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), d, t0); err != nil {
		t.Errorf("free, 5 minutes: %v", err)
	}
}

func TestEditService(t *testing.T) {
	t.Parallel()
	s, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), details(t), t0)
	if err != nil {
		t.Fatal(err)
	}
	d := s.Details()
	d.Duration = 45 * time.Minute
	later := t0.Add(time.Hour)
	if err := s.Edit(d, false, later); err != nil {
		t.Fatal(err)
	}
	if s.IsActive() || s.Version() != 2 || s.Details().Duration != 45*time.Minute || !s.UpdatedAt().Equal(later.Truncate(time.Microsecond)) {
		t.Errorf("after edit = %+v", s)
	}
	d.Duration = 7 * time.Minute
	if err := s.Edit(d, true, later); !errors.Is(err, domain.ErrInvalidDuration) || s.Version() != 2 || s.IsActive() {
		t.Errorf("bad edit changed the service: %v, %+v", err, s)
	}
}

func TestSetOfferings(t *testing.T) {
	t.Parallel()
	s, err := domain.NewService(shared.NewID[domain.ServiceTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), details(t), t0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	price, longer := shared.Halalas(9000), 45*time.Minute
	later := t0.Add(time.Hour)
	if err := s.SetOfferings([]domain.Offering{{Staff: b, Price: &price}, {Staff: a, Duration: &longer}}, later); err != nil {
		t.Fatal(err)
	}
	offs := s.Offerings()
	if s.Version() != 2 || !s.UpdatedAt().Equal(later.Truncate(time.Microsecond)) || len(offs) != 2 {
		t.Fatalf("service = %+v, offerings %+v", s, offs)
	}
	if offs[0].Staff.String() > offs[1].Staff.String() {
		t.Error("offerings are not in staff order")
	}
	for _, o := range offs {
		switch o.Staff {
		case a:
			if o.PriceOr(s.Details().Price).Amount() != 6000 || o.DurationOr(s.Details().Duration) != longer {
				t.Errorf("a = %+v", o)
			}
		case b:
			if o.PriceOr(s.Details().Price).Amount() != 9000 || o.DurationOr(s.Details().Duration) != 30*time.Minute {
				t.Errorf("b = %+v", o)
			}
		}
	}

	sar := func(n int64) *shared.Money { m, _ := shared.NewMoney(n, shared.SAR); return &m }
	dur := func(d time.Duration) *time.Duration { return &d }
	tooMany := make([]domain.Offering, domain.MaxOfferings+1)
	for i := range tooMany {
		tooMany[i] = domain.Offering{Staff: shared.NewID[shared.StaffTag]()}
	}
	for name, tt := range map[string]struct {
		offerings []domain.Offering
		want      error
	}{
		"twice":          {[]domain.Offering{{Staff: a}, {Staff: a}}, domain.ErrDuplicateOffering},
		"nobody":         {[]domain.Offering{{}}, domain.ErrUnknownStaff},
		"7 minutes":      {[]domain.Offering{{Staff: a, Duration: dur(7 * time.Minute)}}, domain.ErrInvalidDuration},
		"negative price": {[]domain.Offering{{Staff: a, Price: sar(-1)}}, domain.ErrInvalidPrice},
		"no currency":    {[]domain.Offering{{Staff: a, Price: &shared.Money{}}}, domain.ErrInvalidPrice},
		"too many":       {tooMany, domain.ErrTooManyOfferings},
	} {
		if err := s.SetOfferings(tt.offerings, later); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
	if s.Version() != 2 || len(s.Offerings()) != 2 {
		t.Error("refused offerings changed the service")
	}
	// A copy: changing what Offerings returned changes nothing.
	s.Offerings()[0].Staff = shared.StaffID{}
	if s.Offerings()[0].Staff.IsZero() {
		t.Error("Offerings returned the service's own slice")
	}
}
