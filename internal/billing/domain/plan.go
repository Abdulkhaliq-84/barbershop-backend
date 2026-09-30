// Package domain holds billing's rules: plans, what they allow, and each
// business's subscription (docs/architecture/domain-model.md §3.7).
package domain

import (
	"errors"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// PlanCode names a plan. Stored, and shown to clients.
type PlanCode string

// Plans. Prices and payment arrive later; plans differ only in limits now.
const (
	PlanFree PlanCode = "free"
	PlanPro  PlanCode = "pro"
)

// Plan is reference data: what a business on it may have.
type Plan struct {
	Code        PlanCode
	Name        shared.LocalizedText
	MaxBranches int
	MaxStaff    int // managers and barbers; the owner is free
}

// Trial rules: an approved business tries the top plan for 30 days.
const (
	TrialPlan   = PlanPro
	TrialLength = 30 * 24 * time.Hour
)

// ErrUnknownPlan reports a plan code that doesn't exist.
var ErrUnknownPlan = errors.New("billing: unknown plan")

var plans = map[PlanCode]Plan{
	PlanFree: {Code: PlanFree, Name: mustText("المجانية", "Free"), MaxBranches: 1, MaxStaff: 3},
	PlanPro:  {Code: PlanPro, Name: mustText("الاحترافية", "Pro"), MaxBranches: 5, MaxStaff: 30},
}

// PlanByCode returns the plan with code.
func PlanByCode(code PlanCode) (Plan, error) {
	p, ok := plans[code]
	if !ok {
		return Plan{}, ErrUnknownPlan
	}
	return p, nil
}

func mustText(ar, en string) shared.LocalizedText {
	t, err := shared.NewLocalizedText(ar, en)
	if err != nil {
		panic(err)
	}
	return t
}
