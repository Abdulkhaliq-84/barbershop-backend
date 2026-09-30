package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// InviteStaff handles POST /v1/businesses/{business_id}/staff/invitations.
func (h *Handlers) InviteStaff(ctx context.Context, req apigen.InviteStaffRequestObject) (apigen.InviteStaffResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.InviteStaffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	body := req.Body
	branches := make([]shared.BranchID, 0, len(body.BranchIds))
	for _, id := range body.BranchIds {
		branches = append(branches, shared.IDFromUUID[shared.BranchTag](id))
	}
	inv, err := h.uc.Staff.Invite(ctx, app.InviteStaff{
		Actor:      p.UserID,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		Phone:      body.Phone,
		Name:       body.DisplayName,
		Role:       string(body.Role),
		Branches:   branches,
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.InviteStaffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.InviteStaff201JSONResponse(toAPIInvitation(inv)), nil
}

// ListStaffInvitations handles GET /v1/businesses/{business_id}/staff/invitations.
func (h *Handlers) ListStaffInvitations(ctx context.Context, req apigen.ListStaffInvitationsRequestObject) (apigen.ListStaffInvitationsResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.ListStaffInvitationsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	invs, err := h.uc.Staff.ListInvitations(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListStaffInvitationsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListStaffInvitations200JSONResponse{Data: make([]apigen.StaffInvitation, 0, len(invs))}
	for _, inv := range invs {
		list.Data = append(list.Data, toAPIInvitation(inv))
	}
	return list, nil
}

// RevokeStaffInvitation handles DELETE /v1/businesses/{business_id}/staff/invitations/{invitation_id}.
func (h *Handlers) RevokeStaffInvitation(ctx context.Context, req apigen.RevokeStaffInvitationRequestObject) (apigen.RevokeStaffInvitationResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.RevokeStaffInvitationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	err := h.uc.Staff.Revoke(ctx, app.InvitationQuery{
		Actor:        p.UserID,
		BusinessID:   shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		InvitationID: shared.IDFromUUID[domain.InvitationTag](req.InvitationId),
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.RevokeStaffInvitationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.RevokeStaffInvitation204Response{}, nil
}

// ListStaff handles GET /v1/businesses/{business_id}/staff.
func (h *Handlers) ListStaff(ctx context.Context, req apigen.ListStaffRequestObject) (apigen.ListStaffResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.ListStaffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	members, err := h.uc.Staff.ListStaff(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListStaffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListStaff200JSONResponse{Data: make([]apigen.StaffMember, 0, len(members))}
	for _, m := range members {
		list.Data = append(list.Data, apigen.StaffMember{
			Id:          m.ID().UUID(),
			DisplayName: m.DisplayName(),
			Role:        apigen.StaffRole(m.Role()),
			BranchIds:   toAPIIDs(m.Branches()),
			Active:      m.IsActive(),
			JoinedAt:    m.CreatedAt(),
		})
	}
	return list, nil
}

// AcceptStaffInvitation handles POST /v1/invitations/accept.
func (h *Handlers) AcceptStaffInvitation(ctx context.Context, req apigen.AcceptStaffInvitationRequestObject) (apigen.AcceptStaffInvitationResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.AcceptStaffInvitationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	v, err := h.uc.Staff.Accept(ctx, p.UserID, req.Body.Token)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.AcceptStaffInvitationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.AcceptStaffInvitation200JSONResponse(toAPIMembership(v)), nil
}

func toAPIInvitation(inv *domain.Invitation) apigen.StaffInvitation {
	return apigen.StaffInvitation{
		Id:          inv.ID().UUID(),
		Phone:       inv.Phone().String(),
		DisplayName: inv.Name(),
		Role:        apigen.StaffRole(inv.Role()),
		BranchIds:   toAPIIDs(inv.Branches()),
		Status:      apigen.StaffInvitationStatus(inv.Status()),
		CreatedAt:   inv.CreatedAt(),
		ExpiresAt:   inv.ExpiresAt(),
	}
}

func toAPIIDs(ids []shared.BranchID) []openapi_types.UUID {
	out := make([]openapi_types.UUID, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.UUID())
	}
	return out
}
