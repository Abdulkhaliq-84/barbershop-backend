package httpx

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Helpers every module's HTTP adapter needs.

// ErrNoPrincipal: an operation that needs a signed-in caller ran without
// one. The spec validator refuses those first, so a handler seeing it is a
// wiring bug; it still answers 401.
var ErrNoPrincipal = errors.New("a valid access token is required")

// ErrBadIfMatch reports an If-Match header that isn't a version number.
var ErrBadIfMatch = errors.New("if-match: not a version")

// ParseIfMatch reads a version from If-Match, bare (3) or as an ETag ("3").
// lowest is the smallest version the resource can have: 1, or 0 where a
// resource never saved yet has version 0.
func ParseIfMatch(v string, lowest int) (int, error) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lowest {
		return 0, ErrBadIfMatch
	}
	return n, nil
}

// APIText returns both languages of t: business-mode screens edit them.
func APIText(t shared.LocalizedText) apigen.LocalizedText {
	text := apigen.LocalizedText{Ar: t.Ar()}
	if en := t.En(); en != "" {
		text.En = new(en)
	}
	return text
}
