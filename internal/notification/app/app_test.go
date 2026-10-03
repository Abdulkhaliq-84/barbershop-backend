package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// now is the fake clock's time in every test.
var now = time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC)

// store is an in-memory domain.Store.
type store struct {
	devices    []domain.Device
	branches   map[shared.BranchID]domain.Branch
	deliveries []domain.Delivery
	saved      *domain.Device // the last SaveDevice call's
	err        error          // what every call fails with
}

func (s *store) SaveDevice(_ context.Context, d domain.Device) (domain.Device, error) {
	s.saved = &d
	return d, s.err
}

func (s *store) RemoveDevice(_ context.Context, user shared.UserID, id domain.DeviceID) error {
	if s.err != nil {
		return s.err
	}
	for i, d := range s.devices {
		if d.ID == id && d.User == user {
			s.devices = slices.Delete(s.devices, i, i+1)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *store) Devices(_ context.Context, user shared.UserID) ([]domain.Device, error) {
	var out []domain.Device
	for _, d := range s.devices {
		if d.User == user {
			out = append(out, d)
		}
	}
	return out, s.err
}

func (s *store) KeepBranch(_ context.Context, b domain.Branch) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if old, ok := s.branches[b.ID]; ok && old.Version >= b.Version {
		return false, nil
	}
	s.branches[b.ID] = b
	return true, nil
}

func (s *store) Branch(_ context.Context, id shared.BranchID) (domain.Branch, error) {
	if s.err != nil {
		return domain.Branch{}, s.err
	}
	b, ok := s.branches[id]
	if !ok {
		return domain.Branch{}, domain.ErrUnknownBranch
	}
	return b, nil
}

func (s *store) Delivered(_ context.Context, event uuid.UUID, device domain.DeviceID) (bool, error) {
	return slices.ContainsFunc(s.deliveries, func(d domain.Delivery) bool {
		return d.Event == event && d.Device == device
	}), s.err
}

func (s *store) RecordDelivery(_ context.Context, d domain.Delivery) error {
	if s.err == nil {
		s.deliveries = append(s.deliveries, d)
	}
	return s.err
}

// pusher records pushes, failing for the devices in fail.
type pusher struct {
	sent []app.Push
	fail map[domain.DeviceID]bool
}

func (p *pusher) Send(_ context.Context, push app.Push) error {
	if p.fail[push.Device.ID] {
		return errors.New("push service unavailable")
	}
	p.sent = append(p.sent, push)
	return nil
}

func setup(t *testing.T) (*app.Handlers, *store, *pusher) {
	t.Helper()
	s := &store{branches: map[shared.BranchID]domain.Branch{}}
	p := &pusher{fail: map[domain.DeviceID]bool{}}
	return app.NewHandlers(s, p, clock.NewFake(now)), s, p
}

const token = "fcm-token-0123456789-abcdefghijklmnopqrstuvwxyz"

func device(t *testing.T, user shared.UserID, lang shared.Language) domain.Device {
	t.Helper()
	tok, err := domain.ParseToken(token + "-" + shared.NewID[domain.DeviceTag]().String())
	if err != nil {
		t.Fatal(err)
	}
	return domain.Device{
		ID: shared.NewID[domain.DeviceTag](), User: user, Token: tok, Platform: domain.IOS, Locale: lang,
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestRegisterDevice(t *testing.T) {
	h, s, _ := setup(t)
	user := shared.NewID[shared.UserTag]()
	d, err := h.RegisterDevice(t.Context(), app.RegisterDevice{User: user, Token: token, Platform: "android", Locale: shared.English})
	if err != nil {
		t.Fatal(err)
	}
	if s.saved == nil || d.ID.IsZero() || d.User != user || d.Token.Reveal() != token || d.Platform != domain.Android ||
		d.Locale != shared.English || !d.CreatedAt.Equal(now) || !d.UpdatedAt.Equal(now) {
		t.Errorf("registered %+v", d)
	}

	for _, c := range []struct {
		cmd  app.RegisterDevice
		want error
	}{
		{app.RegisterDevice{User: user, Token: "short", Platform: "ios"}, domain.ErrBadToken},
		{app.RegisterDevice{User: user, Token: token, Platform: "web"}, domain.ErrBadPlatform},
	} {
		s.saved = nil
		if _, err := h.RegisterDevice(t.Context(), c.cmd); !errors.Is(err, c.want) || s.saved != nil {
			t.Errorf("RegisterDevice(%q, %q) = %v (saved: %v), want %v", c.cmd.Token, c.cmd.Platform, err, s.saved != nil, c.want)
		}
	}
}

func TestRemoveDevice(t *testing.T) {
	h, s, _ := setup(t)
	alice, bob := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	d := device(t, alice, shared.Arabic)
	s.devices = []domain.Device{d}
	if err := h.RemoveDevice(t.Context(), bob, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("someone else's device: %v, want ErrNotFound", err)
	}
	if err := h.RemoveDevice(t.Context(), alice, d.ID); err != nil || len(s.devices) != 0 {
		t.Errorf("own device: %v, %d left", err, len(s.devices))
	}
}

func riyadh(t *testing.T) domain.Branch {
	t.Helper()
	name, err := shared.NewLocalizedText("صالون الأناقة", "Elegance Salon")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Branch{ID: shared.NewID[shared.BranchTag](), Version: 3, Name: name, Timezone: "Asia/Riyadh"}
}

func TestKeepBranch(t *testing.T) {
	h, s, _ := setup(t)
	b := riyadh(t)
	if err := h.KeepBranch(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	older := b
	older.Version, older.Timezone = 2, "Asia/Dubai"
	if err := h.KeepBranch(t.Context(), older); err != nil || s.branches[b.ID].Timezone != "Asia/Riyadh" {
		t.Errorf("an older version: %v, kept %+v", err, s.branches[b.ID])
	}
	s.err = errors.New("database down")
	if err := h.KeepBranch(t.Context(), b); err == nil {
		t.Error("a store error: want it returned, so the outbox retries")
	}
}

// change is a booking confirmed by the shop, at a Riyadh branch.
func change(customer shared.UserID, b domain.Branch) app.AppointmentChange {
	return app.AppointmentChange{
		Event: uuid.New(), Appointment: shared.NewID[shared.AppointmentTag](), Branch: b.ID, Customer: customer,
		What: "confirmed", Status: "confirmed", StartsAt: time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC),
	}
}

func TestNotify(t *testing.T) {
	h, s, p := setup(t)
	b := riyadh(t)
	s.branches[b.ID] = b
	customer, other := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	phone, tablet := device(t, customer, shared.Arabic), device(t, customer, shared.English)
	s.devices = []domain.Device{phone, device(t, other, shared.Arabic), tablet}

	c := change(customer, b)
	if err := h.Notify(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 2 {
		t.Fatalf("sent %d pushes, want one to each of the customer's 2 devices", len(p.sent))
	}
	byDevice := map[domain.DeviceID]app.Push{}
	for _, push := range p.sent {
		byDevice[push.Device.ID] = push
	}
	ar, en := byDevice[phone.ID], byDevice[tablet.ID]
	if ar.Title != "تم تأكيد حجزك" || !strings.Contains(ar.Body, "صالون الأناقة") || !strings.Contains(ar.Body, "16:30") {
		t.Errorf("the Arabic phone got %q, %q", ar.Title, ar.Body)
	}
	if en.Title != "Booking confirmed" || en.Body != "Elegance Salon, Thu 1 Oct, 16:30. See you there." {
		t.Errorf("the English tablet got %q, %q", en.Title, en.Body)
	}
	for _, push := range p.sent {
		if push.Data["kind"] != "booking_confirmed" || push.Data["appointment_id"] != c.Appointment.String() || len(push.Data) != 2 {
			t.Errorf("push data %v", push.Data)
		}
	}
	if len(s.deliveries) != 2 {
		t.Fatalf("logged %d deliveries, want 2", len(s.deliveries))
	}
	for _, d := range s.deliveries {
		if d.Event != c.Event || d.User != customer || d.Kind != domain.BookingConfirmed ||
			d.Appointment != c.Appointment || !d.SentAt.Equal(now) {
			t.Errorf("delivery %+v", d)
		}
	}

	// The outbox delivers the event again: nobody is pushed twice.
	if err := h.Notify(t.Context(), c); err != nil || len(p.sent) != 2 || len(s.deliveries) != 2 {
		t.Errorf("the same event again: %v, %d pushes, %d deliveries; want nothing new", err, len(p.sent), len(s.deliveries))
	}
	// Another event about the same appointment is news.
	c.Event, c.What, c.Status, c.CancelledBy = uuid.New(), "cancelled", "cancelled", "staff"
	if err := h.Notify(t.Context(), c); err != nil || len(p.sent) != 4 || p.sent[3].Data["kind"] != "booking_cancelled" {
		t.Errorf("a cancellation: %v, %d pushes", err, len(p.sent))
	}
}

func TestNotifyNothingToSay(t *testing.T) {
	h, s, p := setup(t)
	b := riyadh(t)
	s.branches[b.ID] = b
	customer := shared.NewID[shared.UserTag]()
	s.devices = []domain.Device{device(t, customer, shared.Arabic)}

	walkIn := change(shared.UserID{}, b)
	cancelledByThem := change(customer, b)
	cancelledByThem.What, cancelledByThem.Status, cancelledByThem.CancelledBy = "cancelled", "cancelled", "customer"
	completed := change(customer, b)
	completed.What, completed.Status = "completed", "completed"
	for name, c := range map[string]app.AppointmentChange{
		"walk-in": walkIn, "cancelled by the customer": cancelledByThem, "completed": completed,
	} {
		if err := h.Notify(t.Context(), c); err != nil || len(p.sent) != 0 || len(s.deliveries) != 0 {
			t.Errorf("%s: %v, %d pushes; want none", name, err, len(p.sent))
		}
	}

	// A customer with no devices: nothing to do, and nothing to retry.
	if err := h.Notify(t.Context(), change(shared.NewID[shared.UserTag](), b)); err != nil || len(p.sent) != 0 {
		t.Errorf("no devices: %v, %d pushes", err, len(p.sent))
	}
}

// The branch's event hasn't arrived yet: the change is retried later, not
// dropped.
func TestNotifyUnknownBranch(t *testing.T) {
	h, s, p := setup(t)
	customer := shared.NewID[shared.UserTag]()
	s.devices = []domain.Device{device(t, customer, shared.Arabic)}
	if err := h.Notify(t.Context(), change(customer, riyadh(t))); !errors.Is(err, domain.ErrUnknownBranch) || len(p.sent) != 0 {
		t.Errorf("unknown branch: %v, %d pushes; want ErrUnknownBranch and none", err, len(p.sent))
	}
}

// One device failing doesn't stop the others; the retry reaches only it.
func TestNotifyPartialFailure(t *testing.T) {
	h, s, p := setup(t)
	b := riyadh(t)
	s.branches[b.ID] = b
	customer := shared.NewID[shared.UserTag]()
	broken, fine := device(t, customer, shared.Arabic), device(t, customer, shared.English)
	s.devices = []domain.Device{broken, fine}
	p.fail[broken.ID] = true

	c := change(customer, b)
	err := h.Notify(t.Context(), c)
	if err == nil || len(p.sent) != 1 || p.sent[0].Device.ID != fine.ID || len(s.deliveries) != 1 {
		t.Fatalf("one device down: %v, %d pushes, %d deliveries; want an error and the other reached", err, len(p.sent), len(s.deliveries))
	}
	if strings.Contains(err.Error(), broken.Token.Reveal()) {
		t.Errorf("the error shows the token: %v", err)
	}

	p.fail[broken.ID] = false
	if err := h.Notify(t.Context(), c); err != nil || len(p.sent) != 2 || p.sent[1].Device.ID != broken.ID {
		t.Errorf("the retry: %v, %d pushes; want only the device missed", err, len(p.sent))
	}
}

func TestNotifyStoreDown(t *testing.T) {
	h, s, p := setup(t)
	b := riyadh(t)
	s.branches[b.ID] = b
	customer := shared.NewID[shared.UserTag]()
	s.devices = []domain.Device{device(t, customer, shared.Arabic)}
	s.err = errors.New("database down")
	if err := h.Notify(t.Context(), change(customer, b)); err == nil || len(p.sent) != 0 {
		t.Errorf("store down: %v, %d pushes; want an error and none", err, len(p.sent))
	}
}
