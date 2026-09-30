package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// SubmitBusiness handles POST /v1/businesses/{business_id}/verification/submit.
func (h *Handlers) SubmitBusiness(ctx context.Context, req apigen.SubmitBusinessRequestObject) (apigen.SubmitBusinessResponseObject, error) {
	fail := func(err error) (apigen.SubmitBusinessResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.SubmitBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		return fail(err)
	}
	b, err := h.uc.Submit.Handle(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId), version)
	if err != nil {
		return fail(err)
	}
	return apigen.SubmitBusiness200JSONResponse(toAPIBusiness(b)), nil
}

// ListBusinessesForReview handles GET /v1/admin/businesses.
func (h *Handlers) ListBusinessesForReview(ctx context.Context, req apigen.ListBusinessesForReviewRequestObject) (apigen.ListBusinessesForReviewResponseObject, error) {
	fail := func(err error) (apigen.ListBusinessesForReviewResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ListBusinessesForReviewdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	admin, err := adminFrom(ctx)
	if err != nil {
		return fail(err)
	}
	status, limit := domain.StatusPendingReview, 20
	if req.Params.Status != nil {
		status = domain.Status(*req.Params.Status)
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	var after *app.QueuePosition
	if req.Params.Cursor != nil {
		if after, err = decodeCursor(*req.Params.Cursor); err != nil {
			return fail(err)
		}
	}
	page, next, err := h.uc.Review.Queue(ctx, admin, status, after, limit)
	if err != nil {
		return fail(err)
	}
	out := apigen.ListBusinessesForReview200JSONResponse{Data: make([]apigen.AdminBusiness, 0, len(page))}
	for _, b := range page {
		out.Data = append(out.Data, toAPIAdminBusiness(b))
	}
	if next != nil {
		out.NextCursor = new(encodeCursor(*next))
	}
	return out, nil
}

// GetBusinessForReview handles GET /v1/admin/businesses/{business_id}.
func (h *Handlers) GetBusinessForReview(ctx context.Context, req apigen.GetBusinessForReviewRequestObject) (apigen.GetBusinessForReviewResponseObject, error) {
	fail := func(err error) (apigen.GetBusinessForReviewResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.GetBusinessForReviewdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	admin, err := adminFrom(ctx)
	if err != nil {
		return fail(err)
	}
	v, err := h.uc.Review.Detail(ctx, admin, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		return fail(err)
	}
	out := apigen.GetBusinessForReview200JSONResponse{
		Business:  toAPIAdminBusiness(v.Business),
		Documents: make([]apigen.VerificationDocument, 0, len(v.Documents)),
		Branches:  make([]apigen.Branch, 0, len(v.Branches)),
	}
	for _, d := range v.Documents {
		out.Documents = append(out.Documents, toAPIDocument(d))
	}
	for _, b := range v.Branches {
		out.Branches = append(out.Branches, toAPIBranch(b))
	}
	return out, nil
}

// ApproveBusiness handles POST /v1/admin/businesses/{business_id}/approve.
func (h *Handlers) ApproveBusiness(ctx context.Context, req apigen.ApproveBusinessRequestObject) (apigen.ApproveBusinessResponseObject, error) {
	fail := func(err error) (apigen.ApproveBusinessResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ApproveBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	admin, err := adminFrom(ctx)
	if err != nil {
		return fail(err)
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		return fail(err)
	}
	b, err := h.uc.Review.Approve(ctx, admin, shared.IDFromUUID[shared.BusinessTag](req.BusinessId), version)
	if err != nil {
		return fail(err)
	}
	return apigen.ApproveBusiness200JSONResponse(toAPIAdminBusiness(b)), nil
}

// RejectBusiness handles POST /v1/admin/businesses/{business_id}/reject.
func (h *Handlers) RejectBusiness(ctx context.Context, req apigen.RejectBusinessRequestObject) (apigen.RejectBusinessResponseObject, error) {
	fail := func(err error) (apigen.RejectBusinessResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.RejectBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	admin, err := adminFrom(ctx)
	if err != nil {
		return fail(err)
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		return fail(err)
	}
	b, err := h.uc.Review.Reject(ctx, admin, shared.IDFromUUID[shared.BusinessTag](req.BusinessId), version, req.Body.Reason)
	if err != nil {
		return fail(err)
	}
	return apigen.RejectBusiness200JSONResponse(toAPIAdminBusiness(b)), nil
}

// adminFrom reads the caller. Whether they are a platform admin is decided
// by the use case, not here.
func adminFrom(ctx context.Context) (app.Admin, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return app.Admin{}, httpx.ErrNoPrincipal
	}
	return app.Admin{ID: p.UserID, IsPlatformAdmin: p.IsPlatformAdmin()}, nil
}

func toAPIAdminBusiness(b *domain.Business) apigen.AdminBusiness {
	out := apigen.AdminBusiness{Business: toAPIBusiness(b), OwnerUserId: b.OwnerID().UUID()}
	if r := b.Review(); !r.ReviewedBy.IsZero() {
		id := r.ReviewedBy.UUID()
		out.ReviewedBy = &id
	}
	return out
}

// errBadCursor reports a cursor this API didn't issue.
var errBadCursor = errors.New("cursor: not one this API issued")

// A cursor is opaque to clients: base64url of "<submitted_at>|<id>". It is
// not signed — it only says where to continue, and every page is still
// filtered by status and checked for admin rights.
func encodeCursor(p app.QueuePosition) string {
	return base64.RawURLEncoding.EncodeToString([]byte(p.SubmittedAt.UTC().Format(time.RFC3339Nano) + "|" + p.ID.String()))
}

func decodeCursor(s string) (*app.QueuePosition, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errBadCursor
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, errBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, errBadCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, errBadCursor
	}
	return &app.QueuePosition{SubmittedAt: t, ID: shared.IDFromUUID[shared.BusinessTag](u)}, nil
}
