package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MinQueryLen is the shortest search, in letters after Normalize: a single
// letter matches nearly everything.
const MinQueryLen = 2

// tatweel stretches Arabic words for looks ("صـــالون"); it carries no meaning.
const tatweel = '\u0640'

// Normalize makes a name and a search comparable, so what a customer types
// finds a shop however either of them spelled it (domain-model.md §3.6):
//   - marks are dropped: Arabic short vowels and shadda (tashkeel), the
//     dagger alef, and accents written as separate marks; tatweel too;
//   - alef forms become one: أ إ آ ٱ → ا; and ى → ي, ة → ه;
//   - letters are lowercased (Latin, and any script that has case);
//   - anything that isn't a letter or digit separates words, and words are
//     joined by single spaces.
//
// It is applied to names when discovery saves them and to every search, so
// the two always meet in the same form.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	gap := false // a separator is waiting for the next word
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r) || r == tatweel:
			continue // inside a word: "مُحَمَّد" is "محمد"
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if gap && b.Len() > 0 {
				b.WriteByte(' ')
			}
			gap = false
			b.WriteRune(fold(r))
		default:
			gap = true
		}
	}
	return b.String()
}

func fold(r rune) rune {
	switch r {
	case 'أ', 'إ', 'آ', 'ٱ':
		return 'ا'
	case 'ى':
		return 'ي'
	case 'ة':
		return 'ه'
	}
	return unicode.ToLower(r)
}

// SearchText is what a search matches a branch's name against: both
// languages, normalised.
func SearchText(name shared.LocalizedText) string {
	return strings.TrimSpace(Normalize(name.Ar()) + " " + Normalize(name.En()))
}

// ParseQuery normalises what a customer typed; it must keep at least
// MinQueryLen letters or digits.
func ParseQuery(q string) (string, error) {
	n := Normalize(q)
	if utf8.RuneCountInString(n) < MinQueryLen {
		return "", ErrQueryTooShort
	}
	return n, nil
}
