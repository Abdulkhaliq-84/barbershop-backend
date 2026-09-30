// Package httpapi exposes the business use cases over HTTP. It implements
// the business operations of the generated apigen.StrictServerInterface:
// translate the typed request into a command, call the use case, translate
// the result or error back. No business rules — and no authorization — live
// here: the use cases check membership themselves.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// UseCases are the business use cases the HTTP adapter calls.
type UseCases struct {
	Register    *app.RegisterBusinessHandler
	Get         *app.GetBusinessHandler
	Update      *app.UpdateBusinessHandler
	Branches    *app.BranchHandlers
	Documents   *app.DocumentHandlers
	Submit      *app.SubmitHandler
	Review      *app.ReviewHandlers
	Staff       *app.StaffHandlers
	Plan        *app.PlanHandler
	Memberships *app.ListMyMembershipsHandler
}

// Handlers serves /v1/businesses/* (businesses, branches, staff),
// /v1/invitations/accept and /v1/me/memberships.
type Handlers struct {
	uc     UseCases
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(uc UseCases, logger *slog.Logger) *Handlers {
	return &Handlers{uc: uc, logger: logger}
}

// errNoPrincipal guards against a route mounted without the spec's security
// requirement, which would otherwise have rejected an anonymous caller.
var errNoPrincipal = errors.New("no authenticated caller")

// RegisterBusiness handles POST /v1/businesses.
func (h *Handlers) RegisterBusiness(ctx context.Context, req apigen.RegisterBusinessRequestObject) (apigen.RegisterBusinessResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.RegisterBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	body := req.Body
	b, err := h.uc.Register.Handle(ctx, app.RegisterBusiness{
		Actor:         p.UserID,
		DisplayNameAr: body.DisplayName.Ar,
		DisplayNameEn: deref(body.DisplayName.En),
		LegalName:     body.LegalName,
		CRNumber:      body.CrNumber,
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.RegisterBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.RegisterBusiness201JSONResponse(toAPIBusiness(b)), nil
}

// GetBusiness handles GET /v1/businesses/{business_id}.
func (h *Handlers) GetBusiness(ctx context.Context, req apigen.GetBusinessRequestObject) (apigen.GetBusinessResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.GetBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	b, err := h.uc.Get.Handle(ctx, app.GetBusiness{
		Actor:      p.UserID,                                              // who asks: from the token, never the request
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId), // what they ask for: from the path
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.GetBusiness200JSONResponse(toAPIBusiness(b)), nil
}

// UpdateBusiness handles PATCH /v1/businesses/{business_id}.
func (h *Handlers) UpdateBusiness(ctx context.Context, req apigen.UpdateBusinessRequestObject) (apigen.UpdateBusinessResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.UpdateBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.UpdateBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	cmd := app.UpdateBusiness{
		Actor:           p.UserID,
		BusinessID:      shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		ExpectedVersion: version,
		LegalName:       req.Body.LegalName,
	}
	if n := req.Body.DisplayName; n != nil {
		cmd.DisplayName = &app.DisplayName{Ar: n.Ar, En: deref(n.En)}
	}
	b, err := h.uc.Update.Handle(ctx, cmd)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.UpdateBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.UpdateBusiness200JSONResponse(toAPIBusiness(b)), nil
}

// errBadIfMatch reports an If-Match header that isn't a version number.
var errBadIfMatch = errors.New("if-match: not a version")

// parseIfMatch reads a version from If-Match, bare (3) or as an ETag ("3").
func parseIfMatch(v string) (int, error) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, errBadIfMatch
	}
	return n, nil
}

// ListMyMemberships handles GET /v1/me/memberships.
func (h *Handlers) ListMyMemberships(ctx context.Context, _ apigen.ListMyMembershipsRequestObject) (apigen.ListMyMembershipsResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.ListMyMembershipsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	views, err := h.uc.Memberships.Handle(ctx, p.UserID)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListMyMembershipsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListMyMemberships200JSONResponse{Data: make([]apigen.Membership, 0, len(views))}
	for _, v := range views {
		list.Data = append(list.Data, toAPIMembership(v))
	}
	return list, nil
}

func toAPIMembership(v app.MembershipView) apigen.Membership {
	return apigen.Membership{
		StaffId: v.StaffID.UUID(),
		Role:    apigen.StaffRole(v.Role),
		Business: apigen.BusinessSummary{
			Id:          v.BusinessID.UUID(),
			DisplayName: toAPIText(v.DisplayName),
			Status:      apigen.BusinessStatus(v.Status),
		},
	}
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	var headers apigen.ProblemResponseHeaders
	var policyErr *domain.PolicyError // its text is ours, safe to show
	switch {
	case errors.Is(err, errNoPrincipal):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
		challenge := httpx.BearerChallenge
		headers.WWWAuthenticate = &challenge
	case errors.Is(err, errBadCursor):
		status, code, detail = http.StatusBadRequest, "validation_failed", "cursor: pass next_cursor from the previous page"
	case errors.Is(err, domain.ErrNotPlatformAdmin):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrSelfReview):
		status, code, detail = http.StatusForbidden, "forbidden", "another admin must review a business you own or work at"
	case errors.Is(err, domain.ErrCRDocumentRequired):
		status, code, detail = http.StatusConflict, "cr_document_required", "upload the CR certificate first"
	case errors.Is(err, domain.ErrBranchRequired):
		status, code, detail = http.StatusConflict, "branch_required", "add a branch with its location first"
	case errors.Is(err, domain.ErrCRNumberClaimed):
		status, code, detail = http.StatusConflict, "cr_number_taken", "another business on the platform already uses this CR number"
	case errors.Is(err, domain.ErrRejectionReasonRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "reason: required"
	case errors.Is(err, domain.ErrUnknownStatus):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "status: drafts are not in the review queue"
	case errors.Is(err, errBadIfMatch):
		status, code, detail = http.StatusBadRequest, "validation_failed", "If-Match: send the business version you last read"
	case errors.Is(err, domain.ErrBranchLimitReached):
		status, code, detail = http.StatusConflict, "plan_limit_reached", "your plan's branch limit is reached; see GET /v1/businesses/{business_id}/subscription"
	case errors.Is(err, domain.ErrTooManyInvitations):
		status, code, detail = http.StatusTooManyRequests, "rate_limited", "too many invitations; try again later"
		var rate *domain.InviteRateError
		if errors.As(err, &rate) {
			secs := int(math.Ceil(rate.RetryAfter.Seconds()))
			headers.RetryAfter = &secs
		}
	case errors.Is(err, domain.ErrTooManyRegistrations):
		status, code, detail = http.StatusConflict, "registration_limit_reached", "finish, or wait for the review of, an earlier registration first"
	case errors.Is(err, domain.ErrStaffLimitReached):
		status, code, detail = http.StatusConflict, "plan_limit_reached", "your plan's staff limit is reached (pending invitations count); see GET /v1/businesses/{business_id}/subscription"
	case errors.Is(err, domain.ErrInvitationInvalid):
		status, code, detail = http.StatusNotFound, "invitation_invalid", "this invitation link is not valid for your account; ask the owner to invite you again"
	case errors.Is(err, domain.ErrAlreadyStaff):
		status, code, detail = http.StatusConflict, "already_staff", "you already work at this business"
	case errors.Is(err, domain.ErrInvitationClosed):
		status, code, detail = http.StatusConflict, "invitation_closed", "it was already accepted or revoked"
	case errors.Is(err, domain.ErrUnknownBranch):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "branch_ids: every branch must belong to this business"
	case errors.Is(err, domain.ErrStaffBranchRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "branch_ids: pick at least one branch"
	case errors.Is(err, domain.ErrStaffNameRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "display_name: required"
	case errors.Is(err, domain.ErrInvalidInviteRole):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "role: invite a manager or a barber"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrAlreadyRegistered):
		status, code, detail = http.StatusConflict, "business_already_registered", "you already registered this CR number; see /v1/me/memberships"
	case errors.Is(err, domain.ErrVersionConflict):
		status, code, detail = http.StatusPreconditionFailed, "version_conflict", "it changed since you read it; reload and try again"
	case errors.Is(err, domain.ErrInvalidStateTransition):
		status, code, detail = http.StatusConflict, "invalid_state_transition", "only allowed while the business is a draft or rejected"
	case errors.Is(err, domain.ErrUnsupportedFile):
		status, code, detail = http.StatusUnsupportedMediaType, "unsupported_media_type", "send a PDF, JPEG or PNG file"
	case errors.Is(err, domain.ErrFileTooLarge):
		status, code, detail = http.StatusRequestEntityTooLarge, "payload_too_large", "files may be up to 10 MiB"
	case errors.Is(err, domain.ErrEmptyFile):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "the file is empty"
	case errors.Is(err, domain.ErrDocumentLimitReached):
		status, code, detail = http.StatusConflict, "document_limit_reached", "a business keeps at most 5 verification documents"
	case errors.Is(err, domain.ErrInvalidCRNumber):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "cr_number: must be 10 digits"
	case errors.Is(err, shared.ErrArabicRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "an Arabic name is required"
	case errors.As(err, &policyErr):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", policyErr.Error()
	case errors.Is(err, domain.ErrInvalidCityCode):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "city_code: not a city code"
	case errors.Is(err, domain.ErrAddressRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "address: required"
	case errors.Is(err, domain.ErrInvalidTimezone):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "timezone: not an IANA time zone such as Asia/Riyadh"
	case errors.Is(err, shared.ErrInvalidCoordinates):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "location: not a valid latitude/longitude"
	case errors.Is(err, shared.ErrInvalidPhoneNumber):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "phone: not a Saudi mobile number"
	case errors.Is(err, domain.ErrLegalNameRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "legal_name: required"
	case errors.Is(err, domain.ErrTextTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "a name is too long"
	default:
		h.logger.ErrorContext(ctx, "business request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}

func toAPIBusiness(b *domain.Business) apigen.Business {
	out := apigen.Business{
		Id:          b.ID().UUID(),
		DisplayName: toAPIText(b.DisplayName()),
		LegalName:   b.LegalName(),
		CrNumber:    b.CRNumber().String(),
		Status:      apigen.BusinessStatus(b.Status()),
		Version:     b.Version(),
		CreatedAt:   b.CreatedAt(),
		UpdatedAt:   b.UpdatedAt(),
		SubmittedAt: b.Review().SubmittedAt,
		ReviewedAt:  b.Review().ReviewedAt,
	}
	if reason := b.Review().RejectionReason; reason != "" && b.Status() == domain.StatusRejected {
		out.RejectionReason = &reason
	}
	return out
}

// toAPIText returns both languages: business-mode screens edit them.
func toAPIText(t shared.LocalizedText) apigen.LocalizedText {
	text := apigen.LocalizedText{Ar: t.Ar()}
	if en := t.En(); en != "" {
		text.En = &en
	}
	return text
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
