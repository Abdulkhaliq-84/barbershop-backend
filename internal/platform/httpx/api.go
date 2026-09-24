package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
)

// maxBodyBytes caps request bodies; the API only takes small JSON documents.
const maxBodyBytes = 1 << 20 // 1 MiB

// MountAPI serves every operation of api/openapi.yaml on r. Each request is
// first validated against the spec (types, required fields, lengths, unknown
// fields), so handlers only ever see well-formed input.
func MountAPI(r chi.Router, server apigen.StrictServerInterface, logger *slog.Logger) error {
	spec, err := apigen.GetSpec()
	if err != nil {
		return fmt.Errorf("load embedded openapi spec: %w", err)
	}
	spec.Servers = nil // match paths whatever host the API runs on

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		SilenceServersWarning: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			WriteProblem(w, r, Problem{Status: opts.StatusCode, Code: "validation_failed", Detail: validationDetail(err)})
		},
	})

	handler := apigen.NewStrictHandlerWithOptions(server, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			WriteProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: "request body is not valid JSON"})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.ErrorContext(r.Context(), "api handler failed", slog.Any("error", err))
			WriteProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal"})
		},
	})

	r.Group(func(r chi.Router) {
		r.Use(limitBody(maxBodyBytes), validator)
		apigen.HandlerWithOptions(handler, apigen.ChiServerOptions{BaseRouter: r})
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

// validationDetail explains what is wrong without echoing the submitted value
// (it may be a phone number): "phone: maximum string length is 64".
func validationDetail(err error) string {
	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		if path := strings.Join(schemaErr.JSONPointer(), "."); path != "" {
			return path + ": " + schemaErr.Reason
		}
		return schemaErr.Reason
	}
	var reqErr *openapi3filter.RequestError
	if errors.As(err, &reqErr) && reqErr.Reason != "" {
		return reqErr.Reason
	}
	return "request does not match the API specification"
}

func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
