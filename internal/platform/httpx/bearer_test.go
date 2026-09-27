package httpx

import "testing"

func TestBearerToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header, want string
		ok           bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc", "abc", true}, // the scheme is case-insensitive
		{"BEARER   abc  ", "abc", true},
		{"", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Basic dXNlcjpwYXNz", "", false},
		{"Bearerabc", "", false},
		{"Token abc", "", false},
	}
	for _, tt := range tests {
		got, ok := bearerToken(tt.header)
		if got != tt.want || ok != tt.ok {
			t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}
