package postgres_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return pool
}

// device is a new registration of token for user, at.
func device(t *testing.T, user shared.UserID, token string, at time.Time) domain.Device {
	t.Helper()
	tok, err := domain.ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Device{
		ID: shared.NewID[domain.DeviceTag](), User: user, Token: tok, Platform: domain.Android, Locale: shared.Arabic,
		CreatedAt: at, UpdatedAt: at,
	}
}

func tokenN(i int) string { return fmt.Sprintf("fcm-token-%04d-abcdefghijklmnopqrstuvwxyz", i) }

func TestSaveDevice(t *testing.T) {
	s := postgres.NewStore(migrated(t))
	ctx := t.Context()
	alice, bob := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()

	first, err := s.SaveDevice(ctx, device(t, alice, tokenN(1), t0))
	if err != nil {
		t.Fatal(err)
	}
	if first.User != alice || first.Token.Reveal() != tokenN(1) || first.Platform != domain.Android ||
		first.Locale != shared.Arabic || !first.CreatedAt.Equal(t0) || !first.UpdatedAt.Equal(t0) {
		t.Errorf("saved %+v", first)
	}

	// Alice's phone registers again (the app restarted, in English now): the
	// same device, refreshed.
	again := device(t, alice, tokenN(1), t0.Add(time.Hour))
	again.Platform, again.Locale = domain.IOS, shared.English
	got, err := s.SaveDevice(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) ||
		got.Platform != domain.IOS || got.Locale != shared.English {
		t.Errorf("registered again: %+v; want the same ID and created_at, the rest refreshed", got)
	}

	// Bob signs in on Alice's phone: the phone is his now, as a new device.
	moved, err := s.SaveDevice(ctx, device(t, bob, tokenN(1), t0.Add(2*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID == first.ID || moved.User != bob || !moved.CreatedAt.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("moved to Bob: %+v; want a new ID and created_at", moved)
	}
	if ds, err := s.Devices(ctx, alice); err != nil || len(ds) != 0 {
		t.Errorf("Alice's devices after Bob took the phone: %d, %v; want none", len(ds), err)
	}
	ds, err := s.Devices(ctx, bob)
	if err != nil || len(ds) != 1 || ds[0].ID != moved.ID || ds[0].Token.Reveal() != tokenN(1) {
		t.Errorf("Bob's devices: %+v, %v", ds, err)
	}
}

// A user keeps only their newest MaxDevicesPerUser devices; nobody else's
// are touched.
func TestSaveDeviceKeepsNewest(t *testing.T) {
	s := postgres.NewStore(migrated(t))
	ctx := t.Context()
	alice, bob := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	if _, err := s.SaveDevice(ctx, device(t, bob, tokenN(99), t0)); err != nil {
		t.Fatal(err)
	}
	var saved []domain.Device
	for i := range domain.MaxDevicesPerUser + 2 {
		d, err := s.SaveDevice(ctx, device(t, alice, tokenN(i), t0.Add(time.Duration(i)*time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, d)
	}
	ds, err := s.Devices(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != domain.MaxDevicesPerUser {
		t.Fatalf("Alice has %d devices, want %d", len(ds), domain.MaxDevicesPerUser)
	}
	for i, d := range ds { // newest first: the last saved
		if want := saved[len(saved)-1-i]; d.ID != want.ID {
			t.Errorf("device %d = %s, want %s", i, d.ID, want.ID)
		}
	}
	if bobs, err := s.Devices(ctx, bob); err != nil || len(bobs) != 1 {
		t.Errorf("Bob's devices: %d, %v; want his one kept", len(bobs), err)
	}

	// A dropped device registering again comes back as the newest, and the
	// oldest left goes.
	if _, err := s.SaveDevice(ctx, device(t, alice, tokenN(0), t0.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	ds, _ = s.Devices(ctx, alice)
	if len(ds) != domain.MaxDevicesPerUser || ds[0].Token.Reveal() != tokenN(0) || ds[len(ds)-1].Token.Reveal() != tokenN(3) {
		t.Errorf("after the oldest came back: %d devices, newest %s, oldest %s", len(ds), ds[0].Token.Reveal(), ds[len(ds)-1].Token.Reveal())
	}
}

func TestRemoveDevice(t *testing.T) {
	s := postgres.NewStore(migrated(t))
	ctx := t.Context()
	alice, bob := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	d, err := s.SaveDevice(ctx, device(t, alice, tokenN(1), t0))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDevice(ctx, bob, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Bob removing Alice's device: %v, want ErrNotFound", err)
	}
	if err := s.RemoveDevice(ctx, alice, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDevice(ctx, alice, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("removed twice: %v, want ErrNotFound", err)
	}
	if ds, err := s.Devices(ctx, alice); err != nil || len(ds) != 0 {
		t.Errorf("devices after removing: %d, %v", len(ds), err)
	}
}

func branch(t *testing.T, id shared.BranchID, version int, ar, en, tz string) domain.Branch {
	t.Helper()
	name, err := shared.NewLocalizedText(ar, en)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Branch{ID: id, Version: version, Name: name, Timezone: tz}
}

func TestKeepBranch(t *testing.T) {
	s := postgres.NewStore(migrated(t))
	ctx := t.Context()
	id := shared.NewID[shared.BranchTag]()
	if _, err := s.Branch(ctx, id); !errors.Is(err, domain.ErrUnknownBranch) {
		t.Errorf("before any event: %v, want ErrUnknownBranch", err)
	}
	for _, c := range []struct {
		b    domain.Branch
		kept bool
	}{
		{branch(t, id, 2, "صالون الأناقة", "Elegance", "Asia/Riyadh"), true},
		{branch(t, id, 2, "صالون قديم", "", "Asia/Dubai"), false}, // the same version again
		{branch(t, id, 1, "صالون أقدم", "", "Asia/Dubai"), false}, // an older one, late
		{branch(t, id, 3, "صالون الأناقة الجديد", "New Elegance", "Asia/Riyadh"), true},
	} {
		kept, err := s.KeepBranch(ctx, c.b)
		if err != nil || kept != c.kept {
			t.Errorf("KeepBranch(v%d) = %v, %v; want %v", c.b.Version, kept, err, c.kept)
		}
	}
	got, err := s.Branch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || got.Name.Ar() != "صالون الأناقة الجديد" || got.Name.En() != "New Elegance" || got.Timezone != "Asia/Riyadh" {
		t.Errorf("kept %+v, want version 3", got)
	}
	// 1<<32 + 5 would wrap to version 5 in the int4 column.
	for _, v := range []int{0, -1, 1<<32 + 5} {
		if _, err := s.KeepBranch(ctx, branch(t, id, v, "صالون", "", "Asia/Riyadh")); err == nil {
			t.Errorf("version %d: want an error", v)
		}
	}
}

func TestDeliveries(t *testing.T) {
	pool := migrated(t)
	s := postgres.NewStore(pool)
	ctx := t.Context()
	user := shared.NewID[shared.UserTag]()
	event, phone, tablet := uuid.New(), shared.NewID[domain.DeviceTag](), shared.NewID[domain.DeviceTag]()
	d := domain.Delivery{
		Event: event, Device: phone, User: user, Kind: domain.BookingConfirmed,
		Appointment: shared.NewID[shared.AppointmentTag](), Outcome: domain.Sent, SentAt: t0,
	}
	if done, err := s.Delivered(ctx, event, phone); err != nil || done {
		t.Errorf("before sending: %v, %v", done, err)
	}
	if err := s.RecordDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDelivery(ctx, d); err != nil { // a retry recording it again
		t.Errorf("recorded twice: %v", err)
	}
	if done, err := s.Delivered(ctx, event, phone); err != nil || !done {
		t.Errorf("after sending: %v, %v", done, err)
	}
	if done, err := s.Delivered(ctx, event, tablet); err != nil || done {
		t.Errorf("another device: %v, %v; want not delivered", done, err)
	}
	if done, err := s.Delivered(ctx, uuid.New(), phone); err != nil || done {
		t.Errorf("another event: %v, %v; want not delivered", done, err)
	}
	// A notice about no appointment (later kinds) has none to record.
	if err := s.RecordDelivery(ctx, domain.Delivery{Event: uuid.New(), Device: phone, User: user, Kind: "welcome", Outcome: domain.Sent, SentAt: t0}); err != nil {
		t.Errorf("no appointment: %v", err)
	}

	// A push the service refused counts as done too: it isn't tried again.
	refused := domain.Delivery{Event: uuid.New(), Device: tablet, User: user, Kind: domain.BookingConfirmed, Outcome: domain.Rejected, SentAt: t0}
	if err := s.RecordDelivery(ctx, refused); err != nil {
		t.Fatal(err)
	}
	if done, err := s.Delivered(ctx, refused.Event, tablet); err != nil || !done {
		t.Errorf("after a refusal: %v, %v; want done", done, err)
	}
	outcomes := map[uuid.UUID]string{}
	rows, err := pool.Query(ctx, `SELECT event_id, outcome FROM notification.deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var outcome string
		if err := rows.Scan(&id, &outcome); err != nil {
			t.Fatal(err)
		}
		outcomes[id] = outcome
	}
	if rows.Err() != nil || outcomes[event] != "sent" || outcomes[refused.Event] != "rejected" {
		t.Errorf("outcomes %v, %v", outcomes, rows.Err())
	}
	// Only the outcomes the code knows.
	if err := s.RecordDelivery(ctx, domain.Delivery{Event: uuid.New(), Device: phone, User: user, Kind: "x", Outcome: "lost", SentAt: t0}); err == nil {
		t.Error("an unknown outcome was stored")
	}
}

// A device the push service no longer knows is forgotten, whoever's it is.
func TestForgetDevice(t *testing.T) {
	s := postgres.NewStore(migrated(t))
	ctx := t.Context()
	alice := shared.NewID[shared.UserTag]()
	gone, err := s.SaveDevice(ctx, device(t, alice, tokenN(1), t0))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := s.SaveDevice(ctx, device(t, alice, tokenN(2), t0))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // forgetting twice is harmless
		if err := s.ForgetDevice(ctx, gone.ID); err != nil {
			t.Fatal(err)
		}
	}
	if ds, err := s.Devices(ctx, alice); err != nil || len(ds) != 1 || ds[0].ID != kept.ID {
		t.Errorf("devices after forgetting one: %+v, %v", ds, err)
	}
	// The same phone registering again later is a new device.
	again, err := s.SaveDevice(ctx, device(t, alice, tokenN(1), t0.Add(time.Hour)))
	if err != nil || again.ID == gone.ID {
		t.Errorf("registered again: %+v, %v; want a new device", again, err)
	}
}
