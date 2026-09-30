package httpapi

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// UploadVerificationDocument handles POST /v1/businesses/{business_id}/verification/documents.
func (h *Handlers) UploadVerificationDocument(ctx context.Context, req apigen.UploadVerificationDocumentRequestObject) (apigen.UploadVerificationDocumentResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.UploadVerificationDocumentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	view, err := h.uc.Documents.Upload(ctx, app.UploadDocument{
		Actor:      p.UserID,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		Kind:       string(req.Params.Kind),
		Body:       req.Body,
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.UploadVerificationDocumentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.UploadVerificationDocument201JSONResponse(toAPIDocument(view)), nil
}

// ListVerificationDocuments handles GET /v1/businesses/{business_id}/verification/documents.
func (h *Handlers) ListVerificationDocuments(ctx context.Context, req apigen.ListVerificationDocumentsRequestObject) (apigen.ListVerificationDocumentsResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.ListVerificationDocumentsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	views, err := h.uc.Documents.List(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListVerificationDocumentsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListVerificationDocuments200JSONResponse{Data: make([]apigen.VerificationDocument, 0, len(views))}
	for _, v := range views {
		list.Data = append(list.Data, toAPIDocument(v))
	}
	return list, nil
}

func toAPIDocument(v app.DocumentView) apigen.VerificationDocument {
	d := v.Document
	return apigen.VerificationDocument{
		Id:                   d.FileID().UUID(),
		Kind:                 apigen.VerificationDocumentKind(d.Kind()),
		ContentType:          apigen.VerificationDocumentContentType(d.ContentType()),
		SizeBytes:            d.Size(),
		UploadedAt:           d.UploadedAt(),
		DownloadUrl:          v.DownloadURL,
		DownloadUrlExpiresAt: v.Expires,
	}
}
