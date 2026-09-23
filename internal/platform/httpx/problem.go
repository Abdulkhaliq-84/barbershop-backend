// Package httpx is the HTTP edge shared by every module: router, middleware,
// health checks, graceful server shutdown and RFC 9457 error responses.
// It contains no business rules.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 9457 "problem details" error body. Code is a stable,
// machine-readable identifier the Flutter app switches on (e.g.
// "slot_unavailable"); Title/Detail are for humans and get localised later.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteProblem sends p as application/problem+json, stamping the request ID
// so a user-reported error can be matched to its log line.
func WriteProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	p.RequestID = RequestIDFrom(r.Context())
	writeJSON(w, p.Status, "application/problem+json", p)
}

// WriteJSON sends v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, "application/json", v)
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// The status line is already sent, so an encoding error can't change the
	// response any more; the client sees a truncated body.
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found"})
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, Problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed"})
}
