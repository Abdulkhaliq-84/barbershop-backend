package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
)

// Request body caps. JSON documents are small; file uploads may be up to
// 10 MiB. Media counts the bytes itself and reads one past its limit to tell
// "exactly 10 MiB" from "too big", so the cap lets that one byte through.
const (
	maxBodyBytes   = 1 << 20         // 1 MiB
	maxUploadBytes = 10<<20 + 1      // media's 10 MiB, and the byte that proves "too big"
	transferTime   = 2 * time.Minute // 10 MiB at ~700 kbit/s: slow mobile links
)

// BearerChallenge is the WWW-Authenticate value sent with 401 responses
// from protected operations (RFC 6750).
const BearerChallenge = `Bearer realm="barbershop-api"` //nolint:gosec // G101: a challenge naming the scheme, not a credential

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

	operationOf, err := operationKinds(spec)
	if err != nil {
		return err
	}
	opts := &nethttpmiddleware.Options{
		SilenceServersWarning: true,
		Options: openapi3filter.Options{
			// Called for operations with a security requirement. The token was
			// already verified by bearerAuth below; this only checks the result.
			AuthenticationFunc: func(ctx context.Context, _ *openapi3filter.AuthenticationInput) error {
				if _, ok := auth.PrincipalFrom(ctx); ok {
					return nil
				}
				return ErrNoPrincipal
			},
		},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				WriteProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Detail: "the request body is too large"})
				return
			}
			if opts.StatusCode == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", BearerChallenge)
				WriteProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Detail: ErrNoPrincipal.Error()})
				return
			}
			WriteProblem(w, r, Problem{Status: opts.StatusCode, Code: "validation_failed", Detail: validationDetail(err)})
		},
	}
	// The validator reads a request body whole into memory (several copies)
	// to check it — and its security check reads it too, in case an
	// AuthenticationFunc wants it. For file uploads that is up to 10 MiB per
	// request, from anyone signed in, so their body — "binary", nothing to
	// check — is hidden from it (see transfers) and left to media, which
	// streams it and checks its size and type itself.
	fileOpts := *opts
	fileOpts.Options.ExcludeRequestBody = true
	validate := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, opts)
	validateUpload := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &fileOpts)

	handler := apigen.NewStrictHandlerWithOptions(server, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: "request body is not valid JSON"})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if responseStarted(w) {
				// A download whose client went away mid-file: the status and
				// part of the body are already out, so there's nothing to fix.
				logger.WarnContext(r.Context(), "api response cut short", slog.String("error_type", fmt.Sprintf("%T", err)))
				return
			}
			logger.ErrorContext(r.Context(), "api handler failed", slog.String("error_type", fmt.Sprintf("%T", err)))
			WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal"})
		},
	})

	r.Group(func(r chi.Router) {
		r.Use(bearerAuth(authenticate), transfers(operationOf, validate, validateUpload))
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

// opKind is what an operation moves: small JSON documents, or a file.
type opKind int

const (
	plainOp    opKind = iota
	uploadOp          // the request body is a file
	downloadOp        // the response body is a file
)

// operation is what the spec says a request's operation moves.
type operation struct {
	kind      opKind
	bodyTypes []string // an upload's media types, e.g. application/octet-stream
}

// operationKinds returns a lookup from a request to the operation it routes
// to, read from the spec: a "format: binary" request body is an upload, a
// binary response a download. It never trusts the client's Content-Type, so
// a JSON operation can't be made to skip its checks.
func operationKinds(spec *openapi3.T) (func(*http.Request) operation, error) {
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("route the openapi spec: %w", err)
	}
	ops := map[*openapi3.Operation]operation{}
	for _, path := range spec.Paths.Map() {
		for _, op := range path.Operations() {
			switch {
			case op.RequestBody != nil && binary(op.RequestBody.Value.Content):
				ops[op] = operation{kind: uploadOp, bodyTypes: slices.Collect(maps.Keys(op.RequestBody.Value.Content))}
			case op.Responses != nil && op.Responses.Status(http.StatusOK) != nil && binary(op.Responses.Status(http.StatusOK).Value.Content):
				ops[op] = operation{kind: downloadOp}
			}
		}
	}
	return func(r *http.Request) operation {
		route, _, err := router.FindRoute(r)
		if err != nil {
			return operation{} // unknown routes: the validator answers them
		}
		return ops[route.Operation]
	}, nil
}

func binary(content openapi3.Content) bool {
	for _, mt := range content {
		if mt.Schema != nil && mt.Schema.Value != nil && mt.Schema.Value.Format == "binary" {
			return true
		}
	}
	return false
}

// transfers caps each request body by what its operation expects — so a
// client can't make the server read an unbounded body — and gives file
// transfers more time than the server-wide read and write timeouts allow.
// Uploads are validated without their body (see MountAPI).
func transfers(operationOf func(*http.Request) operation, validate, validateUpload func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		checked := validate(next)
		// The upload's body goes around the validator, through the context;
		// its Content-Type, which the validator no longer checks, is checked
		// here — after the security check, so strangers still get 401.
		checkedUpload := validateUpload(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			up, _ := r.Context().Value(uploadKey{}).(upload)
			if !up.typeOK {
				WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: validationDetail(nil)})
				return
			}
			r.Body = up.body
			next.ServeHTTP(w, r)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op := operationOf(r)
			if op.kind != plainOp {
				// Both deadlines: an upload's reply goes out only after the
				// whole file came in. Best effort: a writer that can't (a
				// test recorder) keeps the server's.
				rc := http.NewResponseController(w)
				_ = rc.SetReadDeadline(time.Now().Add(transferTime))
				_ = rc.SetWriteDeadline(time.Now().Add(transferTime))
			}
			switch op.kind {
			case uploadOp:
				mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				up := upload{body: http.MaxBytesReader(w, r.Body, maxUploadBytes), typeOK: slices.Contains(op.bodyTypes, mt)}
				r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, up))
				r.Body = http.NoBody
				checkedUpload.ServeHTTP(w, r)
			default:
				r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
				checked.ServeHTTP(w, r)
			}
		})
	}
}

// upload carries an upload's body around the validator.
type (
	uploadKey struct{}
	upload    struct {
		body   io.ReadCloser
		typeOK bool
	}
)

// responseStarted reports whether a status line has already gone out, when
// w records it (AccessLog wraps every response in a writer that does).
func responseStarted(w http.ResponseWriter) bool {
	ww, ok := w.(middleware.WrapResponseWriter)
	return ok && ww.Status() != 0
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
