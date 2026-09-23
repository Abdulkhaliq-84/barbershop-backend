package httpx

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// Middleware in Go is just a function that wraps an http.Handler and returns
// another one — the same idea as Express's (req, res, next), without `next()`:
// you call next.ServeHTTP(w, r) where you would call next().

// RequestIDHeader carries the request ID in and out.
const RequestIDHeader = "X-Request-Id"

// A client-supplied ID is only trusted if it looks like an ID, so nobody can
// inject newlines or megabytes into our logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID reuses a well-formed incoming X-Request-Id or generates one, puts
// it in the request context and echoes it on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = rand.Text() // 26 random base32 characters (crypto/rand, Go 1.24+)
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request ID stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// AccessLog writes one structured log line per request, after it completes.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor) // records status and size
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 { // handler wrote nothing: net/http sends 200
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("took", time.Since(start)),
				slog.String("request_id", RequestIDFrom(r.Context())),
			)
		})
	}
}

// Recover turns a panic in a handler into a logged 500 problem response
// instead of a dropped connection. In Go a panic is a bug, not control flow:
// errors are returned as values, so reaching this means something is broken.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is net/http's deliberate way to abort a
				// response; let the server handle it as designed.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
					slog.String("request_id", RequestIDFrom(r.Context())),
				)
				WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal"})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
