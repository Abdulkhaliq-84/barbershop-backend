package billing_test

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Through the module's public API, on a real database: the whole billing
// module as other modules and event handlers see it.
func TestModule(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	approvedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clk := clock.NewFake(approvedAt.Add(time.Hour))
	m := billing.New(billing.Deps{Pool: pool, Clock: clk, Logger: slog.New(slog.DiscardHandler)})
	biz := shared.NewID[shared.BusinessTag]()

	// Not approved yet: sets up under the trial plan's limits.
	s, err := m.Standing(ctx, biz)
	if err != nil || s.Status != billing.StatusSetup || s.Plan != "pro" || s.MaxBranches != 5 || s.TrialEndsAt != nil {
		t.Fatalf("before approval = %+v, %v", s, err)
	}

	// The approval event may arrive more than once, even at the same time:
	// one trial, counted from the approval.
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := m.StartTrial(ctx, biz, approvedAt); err != nil {
				t.Errorf("StartTrial: %v", err)
			}
		})
	}
	wg.Wait()
	if err := m.StartTrial(ctx, biz, approvedAt.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	end := approvedAt.Add(30 * 24 * time.Hour)
	s, err = m.Standing(ctx, biz)
	if err != nil || s.Status != billing.StatusTrialing || s.Plan != "pro" || s.PlanName.En() != "Pro" || !s.TrialEndsAt.Equal(end) || s.MaxStaff != 30 {
		t.Fatalf("trialing = %+v, %v", s, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing.subscriptions`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("subscriptions = %d, %v", rows, err)
	}

	// Thirty days later: Free, with no job needed.
	clk.Set(end)
	s, err = m.Standing(ctx, biz)
	if err != nil || s.Status != billing.StatusFree || s.Plan != "free" || s.MaxBranches != 1 || s.MaxStaff != 3 {
		t.Fatalf("after the trial = %+v, %v", s, err)
	}
}
