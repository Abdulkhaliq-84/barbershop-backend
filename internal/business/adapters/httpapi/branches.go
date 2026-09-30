package httpapi

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// ListBranches handles GET /v1/businesses/{business_id}/branches.
func (h *Handlers) ListBranches(ctx context.Context, req apigen.ListBranchesRequestObject) (apigen.ListBranchesResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, httpx.ErrNoPrincipal)
		return apigen.ListBranchesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	branches, err := h.uc.Branches.List(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListBranchesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListBranches200JSONResponse{Data: make([]apigen.Branch, 0, len(branches))}
	for _, b := range branches {
		list.Data = append(list.Data, toAPIBranch(b))
	}
	return list, nil
}

// CreateBranch handles POST /v1/businesses/{business_id}/branches.
func (h *Handlers) CreateBranch(ctx context.Context, req apigen.CreateBranchRequestObject) (apigen.CreateBranchResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, httpx.ErrNoPrincipal)
		return apigen.CreateBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	body := req.Body
	cmd := app.CreateBranch{
		Actor:      p.UserID,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		Name:       app.DisplayName{Ar: body.Name.Ar, En: deref(body.Name.En)},
		CityCode:   body.CityCode,
		District:   deref(body.District),
		Address:    body.Address,
		Location:   app.Location{Lat: body.Location.Latitude, Lng: body.Location.Longitude},
		Phone:      deref(body.Phone),
		Timezone:   deref(body.Timezone),
	}
	if body.BookingPolicy != nil {
		cmd.Policy = new(toPolicyRules(*body.BookingPolicy))
	}
	b, err := h.uc.Branches.Create(ctx, cmd)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.CreateBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.CreateBranch201JSONResponse(toAPIBranch(b)), nil
}

// GetBranch handles GET /v1/businesses/{business_id}/branches/{branch_id}.
func (h *Handlers) GetBranch(ctx context.Context, req apigen.GetBranchRequestObject) (apigen.GetBranchResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, httpx.ErrNoPrincipal)
		return apigen.GetBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	b, err := h.uc.Branches.Get(ctx, app.BranchQuery{
		Actor:      p.UserID,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		BranchID:   shared.IDFromUUID[shared.BranchTag](req.BranchId),
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.GetBranch200JSONResponse(toAPIBranch(b)), nil
}

// UpdateBranch handles PATCH /v1/businesses/{business_id}/branches/{branch_id}.
func (h *Handlers) UpdateBranch(ctx context.Context, req apigen.UpdateBranchRequestObject) (apigen.UpdateBranchResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, httpx.ErrNoPrincipal)
		return apigen.UpdateBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.UpdateBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	body := req.Body
	cmd := app.UpdateBranch{
		Actor:           p.UserID,
		BusinessID:      shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		BranchID:        shared.IDFromUUID[shared.BranchTag](req.BranchId),
		ExpectedVersion: version,
		CityCode:        body.CityCode,
		District:        body.District,
		Address:         body.Address,
		Phone:           body.Phone,
		Timezone:        body.Timezone,
	}
	if n := body.Name; n != nil {
		cmd.Name = &app.DisplayName{Ar: n.Ar, En: deref(n.En)}
	}
	if l := body.Location; l != nil {
		cmd.Location = &app.Location{Lat: l.Latitude, Lng: l.Longitude}
	}
	if bp := body.BookingPolicy; bp != nil {
		cmd.Policy = new(toPolicyRules(*bp))
	}
	b, err := h.uc.Branches.Update(ctx, cmd)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.UpdateBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.UpdateBranch200JSONResponse(toAPIBranch(b)), nil
}

func toAPIBranch(b *domain.Branch) apigen.Branch {
	p, r := b.Profile(), b.Policy().Rules()
	branch := apigen.Branch{
		Id:         b.ID().UUID(),
		BusinessId: b.BusinessID().UUID(),
		Name:       httpx.APIText(p.Name),
		CityCode:   string(p.City),
		Address:    p.Address,
		Location:   apigen.GeoPoint{Latitude: p.Location.Lat(), Longitude: p.Location.Lng()},
		Timezone:   p.Timezone,
		BookingPolicy: apigen.BookingPolicy{
			MinLeadMinutes:            minutes(r.MinLead),
			HorizonDays:               r.HorizonDays,
			SlotIntervalMinutes:       apigen.BookingPolicySlotIntervalMinutes(minutes(r.SlotInterval)),
			BufferMinutes:             minutes(r.Buffer),
			CancellationWindowMinutes: minutes(r.CancellationWindow),
			AutoConfirm:               r.AutoConfirm,
			PendingExpiryMinutes:      minutes(r.PendingExpiry),
			MaxActiveBookings:         r.MaxActiveBookings,
		},
		Status:    apigen.BranchStatus(b.Status()),
		Version:   b.Version(),
		CreatedAt: b.CreatedAt(),
		UpdatedAt: b.UpdatedAt(),
	}
	if p.District != "" {
		branch.District = &p.District
	}
	if !p.Phone.IsZero() {
		branch.Phone = new(p.Phone.String())
	}
	return branch
}

func toPolicyRules(p apigen.BookingPolicy) domain.PolicyRules {
	return domain.PolicyRules{
		MinLead:            time.Duration(p.MinLeadMinutes) * time.Minute,
		HorizonDays:        p.HorizonDays,
		SlotInterval:       time.Duration(p.SlotIntervalMinutes) * time.Minute,
		Buffer:             time.Duration(p.BufferMinutes) * time.Minute,
		CancellationWindow: time.Duration(p.CancellationWindowMinutes) * time.Minute,
		AutoConfirm:        p.AutoConfirm,
		PendingExpiry:      time.Duration(p.PendingExpiryMinutes) * time.Minute,
		MaxActiveBookings:  p.MaxActiveBookings,
	}
}

func minutes(d time.Duration) int { return int(d / time.Minute) }

// PublishBranch handles POST …/branches/{branch_id}/publish.
func (h *Handlers) PublishBranch(ctx context.Context, req apigen.PublishBranchRequestObject) (apigen.PublishBranchResponseObject, error) {
	fail := func(err error) (apigen.PublishBranchResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.PublishBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	cmd, err := statusChange(ctx, req.BusinessId, req.BranchId, req.Params.IfMatch)
	if err != nil {
		return fail(err)
	}
	b, err := h.uc.Branches.Publish(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.PublishBranch200JSONResponse(toAPIBranch(b)), nil
}

// UnpublishBranch handles POST …/branches/{branch_id}/unpublish.
func (h *Handlers) UnpublishBranch(ctx context.Context, req apigen.UnpublishBranchRequestObject) (apigen.UnpublishBranchResponseObject, error) {
	fail := func(err error) (apigen.UnpublishBranchResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.UnpublishBranchdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	cmd, err := statusChange(ctx, req.BusinessId, req.BranchId, req.Params.IfMatch)
	if err != nil {
		return fail(err)
	}
	b, err := h.uc.Branches.Unpublish(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.UnpublishBranch200JSONResponse(toAPIBranch(b)), nil
}

// statusChange reads the caller, the branch and If-Match of a publish or
// unpublish request.
func statusChange(ctx context.Context, business, branch apigen.BusinessID, ifMatch string) (app.BranchStatusChange, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return app.BranchStatusChange{}, httpx.ErrNoPrincipal
	}
	version, err := httpx.ParseIfMatch(ifMatch, 1)
	if err != nil {
		return app.BranchStatusChange{}, err
	}
	return app.BranchStatusChange{
		BranchQuery: app.BranchQuery{
			Actor:      p.UserID,
			BusinessID: shared.IDFromUUID[shared.BusinessTag](business),
			BranchID:   shared.IDFromUUID[shared.BranchTag](branch),
		},
		ExpectedVersion: version,
	}, nil
}
