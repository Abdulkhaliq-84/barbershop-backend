package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
)

// Request body caps. JSON documents are small; file uploads (sent as
// application/octet-stream) may be up to 10 MiB, which media checks again.
const (
	maxBodyBytes   = 1 << 20  // 1 MiB
	maxUploadBytes = 10 << 20 // 10 MiB
)

// BearerChallenge is the WWW-Authenticate value sent with 401 responses
// from protected operations (RFC 6750).
const BearerChallenge = `Bearer realm="barbershop-api"` //nolint:gosec // G101: a challenge naming the scheme, not a credential

// errNoPrincipal tells the spec validator that a protected operation was
// called without a valid access token.
var errNoPrincipal = errors.New("a valid access token is required")

// MountAPI serves every operation of api/openapi.yaml on r. Each request is
// first validated against the spec (types, required fields, lengths, unknown
// fields, and security), so handlers only ever see well-formed input from
// authenticated callers where the spec requires it.
//
// authenticate verifies bearer tokens. The spec decides which operations need
// one: everything is protected unless it declares `security: []`.
func MountAPI(r chi.Router, server apigen.StrictServerInterface, logger *slog.Logger, authenticate auth.Authenticator) error {
	spec, err := apigen.GetSpec()
	if err != nil {
		return fmt.Errorf("load embedded openapi spec: %w", err)
	}
	spec.Servers = nil // match paths whatever host the API runs on

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		SilenceServersWarning: true,
		Options: openapi3filter.Options{
			// Called for operations with a security requirement. The token was
			// already verified by bearerAuth below; this only checks the result.
			AuthenticationFunc: func(ctx context.Context, _ *openapi3filter.AuthenticationInput) error {
				if _, ok := auth.PrincipalFrom(ctx); ok {
					return nil
				}
				return errNoPrincipal
			},
		},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				WriteProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Detail: "the request body is too large"})
				return
			}
			if opts.StatusCode == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", BearerChallenge)
				WriteProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Detail: errNoPrincipal.Error()})
				return
			}
			WriteProblem(w, r, Problem{Status: opts.StatusCode, Code: "validation_failed", Detail: validationDetail(err)})
		},
	})

	handler := apigen.NewStrictHandlerWithOptions(server, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: "request body is not valid JSON"})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.ErrorContext(r.Context(), "api handler failed", slog.String("error_type", fmt.Sprintf("%T", err)))
			WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal"})
		},
	})

	r.Group(func(r chi.Router) {
		r.Use(limitBody(maxBodyBytes, maxUploadBytes), bearerAuth(authenticate), validator)
		apigen.HandlerWithOptions(handler, apigen.ChiServerOptions{
			BaseRouter: r,
			// A path or header parameter the router can't parse (e.g. a
			// business_id that isn't a UUID). The default answers in plain
			// text and echoes the input; answer like every other error.
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: validationDetail(err)})
			},
		})
	})
	return nil
}

// APIProblem builds the generated Problem type for typed error responses,
// stamped with the request ID like every other error.
func APIProblem(ctx context.Context, status int, code, detail string) apigen.Problem {
	p := apigen.Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
	}
	if detail != "" {
		p.Detail = &detail
	}
	if id := RequestIDFrom(ctx); id != "" {
		p.RequestId = &id
	}
	return p
}

// validationDetail never exposes schema reasons or JSON paths: both can contain
// user-supplied property names and values.
func validationDetail(_ error) string {
	return "request does not match the API specification"
}

// limitBody stops reading a request body after n bytes (upload bytes for
// application/octet-stream bodies), so a client can't make the server read —
// and the validator buffer — an unbounded body.
func limitBody(n, upload int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := n
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/octet-stream" {
				limit = upload
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// bearerAuth verifies an "Authorization: Bearer <token>" header, when there
// is one, and puts the caller into the request context. It never rejects a
// request itself: public operations ignore a bad token, and protected ones
// are refused by the spec validator when no principal is present.
func bearerAuth(authenticate auth.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
				if p, err := authenticate(r.Context(), token); err == nil {
					r = r.WithContext(auth.WithPrincipal(r.Context(), p))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken extracts the token from an Authorization header value. The
// scheme is case-insensitive (RFC 7235); the token itself is not.
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
