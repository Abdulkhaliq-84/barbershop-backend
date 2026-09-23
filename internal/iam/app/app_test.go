package app_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// In-memory fakes of the ports. Application tests run in microseconds and
// need no database; the postgres adapter has its own integration tests.

type fakeChallenges struct {
	mu   sync.Mutex
	list []*domain.OTPChallenge
}

func (f *fakeChallenges) Add(_ context.Context, c *domain.OTPChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = append(f.list, c)
	return nil
}

func (f *fakeChallenges) latest(phone shared.PhoneNumber) *domain.OTPChallenge {
	for _, c := range slices.Backward(f.list) {
		if c.Phone() == phone {
			return c
		}
	}
	return nil
}

func (f *fakeChallenges) Latest(_ context.Context, phone shared.PhoneNumber) (*domain.OTPChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.latest(phone); c != nil {
		return c, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeChallenges) CountSince(_ context.Context, phone shared.PhoneNumber, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.list {
		if c.Phone() == phone && !c.CreatedAt().Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeChallenges) UpdateLatest(_ context.Context, phone shared.PhoneNumber, fn func(*domain.OTPChallenge) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.latest(phone)
	if c == nil {
		return domain.ErrNotFound
	}
	return fn(c) // the fake "saves" by keeping the same pointer
}

type fakeUsers struct {
	mu      sync.Mutex
	byPhone map[shared.PhoneNumber]*domain.User
}

func (f *fakeUsers) Register(_ context.Context, u *domain.User) (*domain.User, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.byPhone[u.Phone()]; ok {
		return existing, false, nil
	}
	f.byPhone[u.Phone()] = u
	return u, true, nil
}

type fixedCode struct{ code string }

func (f fixedCode) NewCode() (domain.OTPCode, error) { return domain.ParseOTPCode(f.code) }

type plainHasher struct{}

func (plainHasher) Hash(c domain.OTPCode) []byte { return []byte("h:" + c.Digits()) }

type captureSender struct {
	mu   sync.Mutex
	sent []string
}

func (s *captureSender) SendOTP(_ context.Context, to shared.PhoneNumber, code domain.OTPCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, to.String()+"="+code.Digits())
	return nil
}

type fixture struct {
	clock      *clock.Fake
	challenges *fakeChallenges
	users      *fakeUsers
	sender     *captureSender
	request    *app.RequestOTPHandler
	verify     *app.VerifyOTPHandler
}

func newFixture() *fixture {
	f := &fixture{
		clock:      clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)),
		challenges: &fakeChallenges{},
		users:      &fakeUsers{byPhone: map[shared.PhoneNumber]*domain.User{}},
		sender:     &captureSender{},
	}
	policy := domain.DefaultOTPPolicy()
	f.request = app.NewRequestOTPHandler(f.challenges, fixedCode{"482193"}, plainHasher{}, f.sender, f.clock, policy)
	f.verify = app.NewVerifyOTPHandler(f.challenges, f.users, plainHasher{}, f.clock, policy)
	return f
}

func TestRequestOTP(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()

	res, err := f.request.Handle(ctx, app.RequestOTP{Phone: "055 123 4567"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if res.ExpiresIn != 5*time.Minute || res.ResendAfter != time.Minute {
		t.Errorf("result = %+v", res)
	}
	if want := []string{"+966551234567=482193"}; !slices.Equal(f.sender.sent, want) {
		t.Errorf("sent = %v, want %v", f.sender.sent, want)
	}
	if string(f.challenges.list[0].CodeHash()) == "482193" {
		t.Error("the code must be stored hashed, not in plain text")
	}

	// A second request inside the cooldown is refused, with a wait time.
	f.clock.Advance(20 * time.Second)
	_, err = f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"})
	var later *domain.RetryLaterError
	if !errors.Is(err, domain.ErrOTPCooldown) || !errors.As(err, &later) || later.After != 40*time.Second {
		t.Fatalf("second request error = %v, want cooldown with 40s wait", err)
	}

	// After the cooldown it works again — until the hourly cap.
	for i := 2; i <= 5; i++ {
		f.clock.Advance(time.Minute)
		if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	f.clock.Advance(time.Minute)
	if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"}); !errors.Is(err, domain.ErrOTPRateLimited) {
		t.Fatalf("6th request in an hour: error = %v, want ErrOTPRateLimited", err)
	}

	if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0112345678"}); !errors.Is(err, shared.ErrInvalidPhoneNumber) {
		t.Errorf("landline: error = %v, want ErrInvalidPhoneNumber", err)
	}
}

func TestVerifyOTP(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()

	if _, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "482193"}); !errors.Is(err, domain.ErrOTPInvalid) {
		t.Fatalf("verify before any request: error = %v, want ErrOTPInvalid", err)
	}
	if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "000000"}); !errors.Is(err, domain.ErrOTPInvalid) {
		t.Fatalf("wrong code: error = %v, want ErrOTPInvalid", err)
	}
	if got := f.challenges.list[0].Attempts(); got != 1 {
		t.Fatalf("a wrong guess must be counted: attempts = %d, want 1", got)
	}

	login, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "٤٨٢١٩٣", Locale: shared.English})
	if err != nil {
		t.Fatalf("right code (Arabic digits): %v", err)
	}
	if !login.IsNewUser || login.User.Phone().String() != "+966551234567" || login.User.Locale() != shared.English {
		t.Errorf("login = %+v", login)
	}

	// The same code can't be used twice.
	if _, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "482193"}); !errors.Is(err, domain.ErrOTPInvalid) {
		t.Fatalf("reused code: error = %v, want ErrOTPInvalid", err)
	}

	// Next login with a new code finds the same user.
	f.clock.Advance(2 * time.Minute)
	if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"}); err != nil {
		t.Fatal(err)
	}
	again, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "482193"})
	if err != nil || again.IsNewUser || again.User.ID() != login.User.ID() {
		t.Fatalf("second login = %+v, %v; want the same, existing user", again, err)
	}
}
