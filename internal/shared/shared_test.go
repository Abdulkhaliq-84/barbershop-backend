package shared_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestID(t *testing.T) {
	t.Parallel()

	a, b := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	if a == b || a.IsZero() {
		t.Fatalf("NewID returned duplicate or zero IDs: %v %v", a, b)
	}
	if v := a.UUID().Version(); v != 7 {
		t.Errorf("UUID version = %d, want 7", v)
	}

	parsed, err := shared.ParseID[shared.UserTag](a.String())
	if err != nil || parsed != a {
		t.Fatalf("ParseID(%s) = %v, %v", a, parsed, err)
	}

	for _, bad := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		if _, err := shared.ParseID[shared.BranchTag](bad); !errors.Is(err, shared.ErrInvalidID) {
			t.Errorf("ParseID(%q) error = %v, want ErrInvalidID", bad, err)
		}
	}

	// IDs travel through JSON as plain strings.
	type payload struct {
		ID shared.BranchID `json:"id"`
	}
	in := payload{ID: shared.NewID[shared.BranchTag]()}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out payload
	if err := json.Unmarshal(raw, &out); err != nil || out != in {
		t.Fatalf("JSON round trip: %s → %v, %v", raw, out, err)
	}
	// The next line would not compile — that's the point of typed IDs:
	//   var u shared.UserID = in.ID
}

func TestMoney(t *testing.T) {
	t.Parallel()

	if _, err := shared.NewMoney(100, "USD"); !errors.Is(err, shared.ErrUnsupportedCurrency) {
		t.Errorf("NewMoney(USD) error = %v, want ErrUnsupportedCurrency", err)
	}

	haircut, beard := shared.Halalas(6000), shared.Halalas(3500)
	total, err := haircut.Add(beard)
	if err != nil || total.Amount() != 9500 || total.String() != "95.00 SAR" {
		t.Errorf("60 + 35 = %v (%v), want 95.00 SAR", total, err)
	}

	double, err := haircut.Multiply(2)
	if err != nil || double.Amount() != 12000 {
		t.Errorf("60 × 2 = %v (%v)", double, err)
	}

	overflows := []func() (shared.Money, error){
		func() (shared.Money, error) { return shared.Halalas(math.MaxInt64).Add(shared.Halalas(1)) },
		func() (shared.Money, error) { return shared.Halalas(math.MinInt64).Add(shared.Halalas(-1)) },
		func() (shared.Money, error) { return shared.Halalas(math.MaxInt64).Multiply(2) },
		func() (shared.Money, error) { return shared.Halalas(-1).Multiply(math.MinInt64) },
	}
	for i, op := range overflows {
		if _, err := op(); !errors.Is(err, shared.ErrMoneyOverflow) {
			t.Errorf("overflow case %d: error = %v, want ErrMoneyOverflow", i, err)
		}
	}

	formats := map[int64]string{0: "0.00 SAR", 5: "0.05 SAR", 6000: "60.00 SAR", -250: "-2.50 SAR", math.MinInt64: "-92233720368547758.08 SAR"}
	for amount, want := range formats {
		if got := shared.Halalas(amount).String(); got != want {
			t.Errorf("Halalas(%d).String() = %q, want %q", amount, got, want)
		}
	}
}

func TestLocalizedText(t *testing.T) {
	t.Parallel()

	if _, err := shared.NewLocalizedText("   ", "Haircut"); !errors.Is(err, shared.ErrArabicRequired) {
		t.Errorf("blank Arabic: error = %v, want ErrArabicRequired", err)
	}

	name, err := shared.NewLocalizedText("  قص شعر ", " Haircut ")
	if err != nil {
		t.Fatal(err)
	}
	if name.Ar() != "قص شعر" || name.En() != "Haircut" {
		t.Errorf("values not trimmed: %q / %q", name.Ar(), name.En())
	}
	if name.In(shared.English) != "Haircut" || name.In(shared.Arabic) != "قص شعر" {
		t.Error("In() picked the wrong language")
	}

	arOnly, _ := shared.NewLocalizedText("لحية", "")
	if arOnly.In(shared.English) != "لحية" {
		t.Error("In(English) should fall back to Arabic when English is empty")
	}

	tags := map[string]shared.Language{"en": shared.English, "en-US": shared.English, "EN": shared.English, "ar": shared.Arabic, "ar-SA": shared.Arabic, "": shared.Arabic, "fr": shared.Arabic}
	for tag, want := range tags {
		if got := shared.ParseLanguage(tag); got != want {
			t.Errorf("ParseLanguage(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestGeoPoint(t *testing.T) {
	t.Parallel()

	riyadh, err := shared.NewGeoPoint(24.7136, 46.6753)
	if err != nil || riyadh.Lat() != 24.7136 || riyadh.Lng() != 46.6753 {
		t.Fatalf("NewGeoPoint(Riyadh) = %v, %v", riyadh, err)
	}
	for _, c := range [][2]float64{{91, 0}, {-91, 0}, {0, 181}, {0, -181}, {math.NaN(), 0}, {0, math.Inf(1)}} {
		if _, err := shared.NewGeoPoint(c[0], c[1]); !errors.Is(err, shared.ErrInvalidCoordinates) {
			t.Errorf("NewGeoPoint(%v, %v) error = %v, want ErrInvalidCoordinates", c[0], c[1], err)
		}
	}
}

func at(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", "2026-10-01 "+hhmm)
	if err != nil {
		panic(err)
	}
	return t
}

func mustInterval(t *testing.T, from, to string) shared.Interval {
	t.Helper()
	i, err := shared.NewInterval(at(from), at(to))
	if err != nil {
		t.Fatalf("NewInterval(%s, %s): %v", from, to, err)
	}
	return i
}

func TestInterval(t *testing.T) {
	t.Parallel()

	if _, err := shared.NewInterval(at("16:00"), at("16:00")); !errors.Is(err, shared.ErrInvalidInterval) {
		t.Errorf("empty interval: error = %v, want ErrInvalidInterval", err)
	}

	appt := mustInterval(t, "16:00", "16:30")
	if appt.Duration() != 30*time.Minute {
		t.Errorf("Duration() = %v", appt.Duration())
	}

	tests := []struct {
		name     string
		from, to string
		overlaps bool
	}{
		{"same slot", "16:00", "16:30", true},
		{"starts inside", "16:15", "16:45", true},
		{"ends inside", "15:45", "16:15", true},
		{"contains it", "15:00", "17:00", true},
		{"touches after (half-open)", "16:30", "17:00", false},
		{"touches before (half-open)", "15:30", "16:00", false},
		{"far away", "18:00", "19:00", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			other := mustInterval(t, tt.from, tt.to)
			if got := appt.Overlaps(other); got != tt.overlaps {
				t.Errorf("Overlaps = %v, want %v", got, tt.overlaps)
			}
		})
	}

	shift := mustInterval(t, "10:00", "18:00")
	if !shift.Covers(appt) || appt.Covers(shift) {
		t.Error("Covers is wrong")
	}
	if !appt.Contains(at("16:00")) || appt.Contains(at("16:30")) {
		t.Error("Contains must include start and exclude end")
	}
}

// FuzzIntervalOverlaps checks properties that must hold for any intervals:
// overlap is symmetric, and "covers" implies "overlaps".
func FuzzIntervalOverlaps(f *testing.F) {
	f.Add(int64(0), int64(30), int64(15), int64(45))
	f.Add(int64(0), int64(30), int64(30), int64(60))
	f.Fuzz(func(t *testing.T, s1, e1, s2, e2 int64) {
		base := at("00:00")
		a, errA := shared.NewInterval(base.Add(time.Duration(s1)*time.Minute), base.Add(time.Duration(e1)*time.Minute))
		b, errB := shared.NewInterval(base.Add(time.Duration(s2)*time.Minute), base.Add(time.Duration(e2)*time.Minute))
		if errA != nil || errB != nil {
			return
		}
		if a.Overlaps(b) != b.Overlaps(a) {
			t.Fatalf("Overlaps not symmetric for %v and %v", a, b)
		}
		if a.Covers(b) && !a.Overlaps(b) {
			t.Fatalf("%v covers %v but does not overlap it", a, b)
		}
	})
}
