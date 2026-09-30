package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 123456789, time.UTC)

func TestPlans(t *testing.T) {
	t.Parallel()
	free, err := domain.PlanByCode(domain.PlanFree)
	if err != nil || free.MaxBranches != 1 || free.MaxStaff != 3 || free.Name.En() != "Free" {
		t.Errorf("free = %+v, %v", free, err)
	}
	pro, err := domain.PlanByCode(domain.PlanPro)
	if err != nil || pro.MaxBranches != 5 || pro.MaxStaff != 30 || pro.Name.Ar() == "" {
		t.Errorf("pro = %+v, %v", pro, err)
	}
	if _, err := domain.PlanByCode("gold"); !errors.Is(err, domain.ErrUnknownPlan) {
		t.Errorf("unknown plan: %v", err)
	}
}

func TestTrial(t *testing.T) {
	t.Parallel()
	s := domain.StartTrial(shared.NewID[shared.BusinessTag](), t0)
	end := t0.Truncate(time.Microsecond).Add(30 * 24 * time.Hour)
	if s.Plan() != domain.PlanPro || !s.PeriodEnd().Equal(end) || !s.CreatedAt().Equal(t0.Truncate(time.Microsecond)) {
		t.Fatalf("trial = %+v", s)
	}

	during := s.StandingAt(end.Add(-time.Nanosecond))
	if during.Status != domain.StatusTrialing || during.Plan.Code != domain.PlanPro || !during.TrialEndsAt.Equal(end) {
		t.Errorf("during the trial = %+v", during)
	}
	after := s.StandingAt(end)
	if after.Status != domain.StatusFree || after.Plan.Code != domain.PlanFree || !after.TrialEndsAt.Equal(end) {
		t.Errorf("after the trial = %+v", after)
	}

	setup := domain.SetupStanding()
	if setup.Status != domain.StatusSetup || setup.Plan.Code != domain.TrialPlan || setup.TrialEndsAt != nil {
		t.Errorf("setup = %+v", setup)
	}
}
