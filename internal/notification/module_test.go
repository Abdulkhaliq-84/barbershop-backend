package notification_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// A misconfigured push provider stops the server at startup, without
// quoting the key.
func TestNewChecksPushProvider(t *testing.T) {
	t.Parallel()
	deps := func(p notification.Push) notification.Deps {
		bus, err := outbox.New(nil, slog.New(slog.DiscardHandler)) // never run: nothing is sent
		if err != nil {
			t.Fatal(err)
		}
		return notification.Deps{Clock: clock.System{}, Logger: slog.New(slog.DiscardHandler), Push: p, Events: bus}
	}
	for _, p := range []notification.Push{{}, {Provider: "console"}} {
		if _, err := notification.New(deps(p)); err != nil {
			t.Errorf("provider %q: %v", p.Provider, err)
		}
	}
	secret := `{"type":"service_account","project_id":"p","client_email":"e","token_uri":"https://oauth2.googleapis.com/token","private_key":"-----BEGIN PRIVATE KEY-----\nc2VjcmV0LWtleS1tYXRlcmlhbA==\n-----END PRIVATE KEY-----\n"}`
	for name, p := range map[string]notification.Push{
		"unknown provider":   {Provider: "pigeon"},
		"fcm with no key":    {Provider: "fcm", Timeout: time.Second},
		"fcm with a bad key": {Provider: "fcm", Credentials: []byte(secret), Timeout: time.Second},
	} {
		_, err := notification.New(deps(p))
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "c2VjcmV0") {
			t.Errorf("%s: the key is quoted: %v", name, err)
		}
	}
}

// logs collects the JSON log lines; the console sender writes pushes there.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// pushed returns the kinds of the development pushes logged so far.
func (l *logs) pushed() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var kinds []string
	sc := bufio.NewScanner(bytes.NewReader(l.buf.Bytes()))
	for sc.Scan() {
		var line struct{ Msg, Kind string }
		if json.Unmarshal(sc.Bytes(), &line) == nil && strings.HasPrefix(line.Msg, "DEVELOPMENT PUSH") {
			kinds = append(kinds, line.Kind)
		}
	}
	return kinds
}

// The whole reminder path, as the worker runs it: a booking confirmed a day
// ago that starts in 40 minutes is reminded once, however often the task
// runs; one that was cancelled isn't.
func TestReminders(t *testing.T) {
	pool := dbtest.NewDatabase(t)
	ctx := t.Context()
	var out logs
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	if err := database.Migrate(ctx, pool, logger); err != nil {
		t.Fatal(err)
	}
	bus, err := outbox.New(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	m, err := notification.New(notification.Deps{Pool: pool, Clock: clock.NewFake(now), Logger: logger, Events: bus})
	if err != nil {
		t.Fatal(err)
	}
	branch, customer := shared.NewID[shared.BranchTag](), shared.NewID[shared.UserTag]()
	name, _ := shared.NewLocalizedText("صالون الأناقة", "Elegance Salon")
	if err := m.KeepBranch(ctx, notification.Branch{BranchID: branch, Version: 1, Name: name, Timezone: "Asia/Riyadh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notification.devices VALUES ($1, $2, $3, 'android', 'ar', $4, $4)`,
		uuid.New(), customer.UUID(), "fcm-token-0123456789-abcdefghijklmnopqrstuvwxyz", now); err != nil {
		t.Fatal(err)
	}
	starts := now.Add(40 * time.Minute)
	booked := func(status string, at time.Time) notification.AppointmentChanged {
		return notification.AppointmentChanged{
			EventID: uuid.New(), AppointmentID: shared.NewID[shared.AppointmentTag](), BranchID: branch, CustomerID: customer,
			What: "booked", Status: status, StartsAt: starts, At: at,
		}
	}
	kept, dropped := booked("confirmed", now.Add(-24*time.Hour)), booked("confirmed", now.Add(-24*time.Hour))
	for _, e := range []notification.AppointmentChanged{kept, dropped} {
		if err := m.AppointmentChanged(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	cancel := dropped
	cancel.EventID, cancel.What, cancel.Status, cancel.CancelledBy = uuid.New(), "cancelled", "cancelled", "customer"
	if err := m.AppointmentChanged(ctx, cancel); err != nil {
		t.Fatal(err)
	}

	// The worker runs the task as it starts, then delivers what it queued.
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bus.Run(runCtx, 5*time.Second) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	reminded := func() int {
		n := 0
		for _, k := range out.pushed() {
			if k == "booking_reminder" {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(15 * time.Second)
	for reminded() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// Run the task again: nothing new is queued.
	if err := m.RemindDue(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification.deliveries WHERE kind = 'booking_reminder' AND appointment_id = $1`,
		kept.AppointmentID.UUID()).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if got := reminded(); got != 1 || rows != 1 {
		t.Errorf("%d reminders pushed, %d logged (pushes: %v); want one, for the booking still on", got, rows, out.pushed())
	}
}
