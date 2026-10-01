package domain_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"صالون الأناقة":          "صالون الاناقه", // hamza on alef, taa marbuta
		"إبداع":                  "ابداع",         // hamza below
		"آفاق":                   "افاق",          // madda
		"ٱلنخبة":                 "النخبه",        // wasla
		"مُحَمَّد":               "محمد",          // tashkeel
		"صـــالون":               "صالون",         // tatweel
		"مقهى":                   "مقهي",          // alef maqsura
		"Elegance BARBERS":       "elegance barbers",
		"  Ali's   Barber-Shop ": "ali s barber shop",
		"صالون 7":                "صالون 7",
		"!!!":                    "",
		"":                       "",
	} {
		if got := domain.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	// The same shop, spelled the ways customers type it.
	for _, spelling := range []string{"الأناقة", "الاناقه", "الإناقة", "الأَنَاقَة", "الأنـاقـة"} {
		if got := domain.Normalize(spelling); got != "الاناقه" {
			t.Errorf("Normalize(%q) = %q", spelling, got)
		}
	}
}

func TestSearchTextAndQuery(t *testing.T) {
	t.Parallel()
	name, _ := shared.NewLocalizedText("صالون الأناقة", "Elegance Barbers")
	if got := domain.SearchText(name); got != "صالون الاناقه elegance barbers" {
		t.Errorf("SearchText = %q", got)
	}
	arOnly, _ := shared.NewLocalizedText("صالون", "")
	if got := domain.SearchText(arOnly); got != "صالون" {
		t.Errorf("SearchText(Arabic only) = %q", got)
	}
	for q, ok := range map[string]bool{"ab": true, "صا": true, "a": false, "أ!": false, "ـ ـ": false, "  x y ": true} {
		if _, err := domain.ParseQuery(q); (err == nil) != ok {
			t.Errorf("ParseQuery(%q): %v", q, err)
		}
	}
}

// FuzzNormalize checks the properties search relies on, for any input:
// normalising twice changes nothing (names and searches meet in one form),
// and the result is clean text with nothing left to fold.
func FuzzNormalize(f *testing.F) {
	for _, seed := range []string{"صالون الأناقة", "مُحَمَّد", "Ali's  Barber", "صـــالون", "İSTANBUL", "\xff\xfe", "ٱ ى ة", "ǅ ϒ ß"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := domain.Normalize(s)
		if again := domain.Normalize(n); again != n {
			t.Fatalf("not idempotent: %q → %q → %q", s, n, again)
		}
		if !utf8.ValidString(n) {
			t.Fatalf("invalid UTF-8 from %q: %q", s, n)
		}
		if strings.HasPrefix(n, " ") || strings.HasSuffix(n, " ") || strings.Contains(n, "  ") {
			t.Fatalf("stray spaces from %q: %q", s, n)
		}
		if utf8.RuneCountInString(n) > utf8.RuneCountInString(s) {
			t.Fatalf("longer than its input: %q → %q", s, n)
		}
		for _, r := range n {
			switch {
			case r == ' ':
			case strings.ContainsRune("أإآٱىةـ", r), unicode.Is(unicode.Mn, r):
				t.Fatalf("%q left %q in %q", s, r, n)
			case !unicode.IsLetter(r) && !unicode.IsDigit(r):
				t.Fatalf("%q left a separator %q in %q", s, r, n)
			case unicode.ToLower(r) != r:
				t.Fatalf("%q left a capital %q in %q", s, r, n)
			}
		}
	})
}
