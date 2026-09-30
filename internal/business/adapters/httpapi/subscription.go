package httpapi

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// GetSubscription handles GET /v1/businesses/{business_id}/subscription.
func (h *Handlers) GetSubscription(ctx context.Context, req apigen.GetSubscriptionRequestObject) (apigen.GetSubscriptionResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.GetSubscriptiondefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	s, err := h.uc.Plan.Handle(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId))
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetSubscriptiondefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.GetSubscription200JSONResponse{
		Plan:        apigen.Plan{Code: apigen.PlanCode(s.Plan), Name: toAPIText(s.PlanName)},
		Status:      apigen.SubscriptionStatus(s.Status),
		TrialEndsAt: s.TrialEndsAt,
		MaxBranches: s.Limits.MaxBranches,
		MaxStaff:    s.Limits.MaxStaff,
	}, nil
}
