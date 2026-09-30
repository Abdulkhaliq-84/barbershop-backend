// Package httpapi serves media downloads over HTTP.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers serves /v1/media/*.
type Handlers struct {
	svc    *app.Service
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(svc *app.Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// DownloadMedia handles GET /v1/media/{media_id}?expires=…&signature=….
//
// Files are sent as application/octet-stream attachments with nosniff: a
// browser saves them instead of rendering them, so even a file that slipped
// past the type check could never run as a page on our domain.
func (h *Handlers) DownloadMedia(ctx context.Context, req apigen.DownloadMediaRequestObject) (apigen.DownloadMediaResponseObject, error) {
	id := shared.IDFromUUID[shared.MediaTag](req.MediaId)
	obj, body, err := h.svc.Open(ctx, id, req.Params.Expires, req.Params.Signature)
	if err != nil {
		problem := h.problem(ctx, err)
		return apigen.DownloadMediadefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status}, nil
	}
	disposition := fmt.Sprintf(`attachment; filename="%s%s"`, id, extension(obj.ContentType()))
	cache, nosniff := "private, no-store", "nosniff"
	return apigen.DownloadMedia200ApplicationoctetStreamResponse{
		Body:          body,
		ContentLength: obj.Size(),
		Headers: apigen.DownloadMedia200ResponseHeaders{
			ContentDisposition:  &disposition,
			CacheControl:        &cache,
			XContentTypeOptions: &nosniff,
		},
	}, nil
}

func extension(ct domain.ContentType) string {
	switch ct {
	case domain.PDF:
		return ".pdf"
	case domain.JPEG:
		return ".jpg"
	case domain.PNG:
		return ".png"
	default:
		return ""
	}
}

// problem maps a download error to an API error. A bad link and a missing
// file look different on purpose only when the link was genuine.
func (h *Handlers) problem(ctx context.Context, err error) apigen.Problem {
	switch {
	case errors.Is(err, domain.ErrLinkInvalid):
		return httpx.APIProblem(ctx, http.StatusForbidden, "download_link_invalid", "the link is invalid or expired; ask for a new one")
	case errors.Is(err, domain.ErrNotFound):
		return httpx.APIProblem(ctx, http.StatusNotFound, "not_found", "")
	default:
		h.logger.ErrorContext(ctx, "media download failed", slog.String("error_type", fmt.Sprintf("%T", err)))
		return httpx.APIProblem(ctx, http.StatusInternalServerError, "internal", "")
	}
}
