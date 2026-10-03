package domain_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestCustomerNotice(t *testing.T) {
	cases := []struct {
		what, status, cancelledBy string
		want                      domain.Kind
		ok                        bool
	}{
		{"booked", "pending", "", domain.BookingRequested, true},
		{"booked", "confirmed", "", domain.BookingConfirmed, true}, // confirmed at once, or booked by the shop
		{"booked", "", "", "", false},
		{"confirmed", "confirmed", "", domain.BookingConfirmed, true},
		{"rejected", "rejected", "", domain.BookingDeclined, true},
		{"expired", "expired", "", domain.BookingExpired, true},
		{"cancelled", "cancelled", "staff", domain.BookingCancelled, true},
		{"cancelled", "cancelled", "customer", "", false}, // they did it themselves
		{"completed", "completed", "", "", false},
		{"no_show", "no_show", "", "", false},
		{"rescheduled", "confirmed", "", "", false}, // a kind of event it doesn't know
	}
	for _, c := range cases {
		got, ok := domain.CustomerNotice(c.what, c.status, c.cancelledBy)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("CustomerNotice(%q, %q, %q) = %q, %v; want %q, %v", c.what, c.status, c.cancelledBy, got, ok, c.want, c.ok)
		}
	}
}

func branch(t *testing.T, ar, en, tz string) domain.Branch {
	t.Helper()
	name, err := shared.NewLocalizedText(ar, en)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Branch{ID: shared.NewID[shared.BranchTag](), Version: 1, Name: name, Timezone: tz}
}

func TestMessage(t *testing.T) {
	riyadh := branch(t, "صالون الأناقة", "Elegance Salon", "Asia/Riyadh")
	start := time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC) // Thursday 16:30 in Riyadh
	cases := []struct {
		name  string
		kind  domain.Kind
		lang  shared.Language
		b     domain.Branch
		title string
		body  string
	}{
		{
			"confirmed, English", domain.BookingConfirmed, shared.English, riyadh,
			"Booking confirmed", "Elegance Salon, Thu 1 Oct, 16:30. See you there.",
		},
		{
			"confirmed, Arabic", domain.BookingConfirmed, shared.Arabic, riyadh,
			"تم تأكيد حجزك", "صالون الأناقة، الخميس 1 أكتوبر، 16:30. نراك قريبًا.",
		},
		{
			"requested, English", domain.BookingRequested, shared.English, riyadh,
			"Booking request sent", "Elegance Salon, Thu 1 Oct, 16:30. We'll let you know when the shop answers.",
		},
		{
			"declined, Arabic", domain.BookingDeclined, shared.Arabic, riyadh,
			"لم يُقبل طلب الحجز", "تعذّر على صالون الأناقة قبول حجزك الخميس 1 أكتوبر، 16:30. جرّب وقتًا آخر.",
		},
		{
			"expired, English", domain.BookingExpired, shared.English, riyadh,
			"Booking request expired", "Elegance Salon didn't answer your request for Thu 1 Oct, 16:30 in time. Try another time.",
		},
		{
			"cancelled, English", domain.BookingCancelled, shared.English, riyadh,
			"Booking cancelled", "Elegance Salon cancelled your booking for Thu 1 Oct, 16:30.",
		},
		{
			"reminder, English", domain.BookingReminder, shared.English, riyadh,
			"Your appointment is coming up", "Elegance Salon, Thu 1 Oct, 16:30. See you soon.",
		},
		{
			"reminder, Arabic", domain.BookingReminder, shared.Arabic, riyadh,
			"موعدك قريب", "صالون الأناقة، الخميس 1 أكتوبر، 16:30. نراك قريبًا.",
		},
		// No English name: the Arabic one, in an English sentence.
		{
			"no English name", domain.BookingCancelled, shared.English, branch(t, "حلاق الحي", "", "Asia/Riyadh"),
			"Booking cancelled", "حلاق الحي cancelled your booking for Thu 1 Oct, 16:30.",
		},
		// The branch's own time zone, even across midnight: 13:30 UTC is
		// 02:30 on Friday in Auckland (+13 in October).
		{
			"branch's time zone", domain.BookingConfirmed, shared.Arabic, branch(t, "صالون أوكلاند", "", "Pacific/Auckland"),
			"تم تأكيد حجزك", "صالون أوكلاند، الجمعة 2 أكتوبر، 02:30. نراك قريبًا.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body, err := domain.Message(c.kind, c.lang, c.b, start)
			if err != nil {
				t.Fatal(err)
			}
			if title != c.title || body != c.body {
				t.Errorf("Message = %q, %q\nwant       %q, %q", title, body, c.title, c.body)
			}
		})
	}
}

// Every kind has a title and a body in both languages, and each body names
// the branch and the time.
func TestMessagesComplete(t *testing.T) {
	b := branch(t, "صالون", "Salon", "Asia/Riyadh")
	start := time.Date(2026, 12, 6, 6, 5, 0, 0, time.UTC) // Sunday 09:05 in Riyadh
	kinds := []domain.Kind{
		domain.BookingRequested, domain.BookingConfirmed, domain.BookingDeclined, domain.BookingExpired, domain.BookingCancelled,
		domain.BookingReminder,
	}
	for _, k := range kinds {
		for _, lang := range []shared.Language{shared.Arabic, shared.English} {
			title, body, err := domain.Message(k, lang, b, start)
			if err != nil {
				t.Fatalf("%s %s: %v", k, lang, err)
			}
			when := "Sun 6 Dec, 09:05"
			name := "Salon"
			if lang == shared.Arabic {
				when, name = "الأحد 6 ديسمبر، 09:05", "صالون"
			}
			if title == "" || !strings.Contains(body, name) || !strings.Contains(body, when) || strings.Contains(body, "%!") {
				t.Errorf("%s %s: %q, %q; want the name %q and %q", k, lang, title, body, name, when)
			}
		}
	}
}

func TestMessageErrors(t *testing.T) {
	start := time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC)
	if _, _, err := domain.Message("booking_moved", shared.English, branch(t, "صالون", "", "Asia/Riyadh"), start); err == nil {
		t.Error("a kind with no message: want an error")
	}
	if _, _, err := domain.Message(domain.BookingConfirmed, shared.English, branch(t, "صالون", "", "Mars/Olympus"), start); err == nil {
		t.Error("an unknown time zone: want an error")
	}
}

const fcmToken = "dQw4w9WgXcQ:APA91bHun4MxP5egoKMwt2KZFBaFUH-1RYqx_Ln5L6Zc.2x"

func TestParseToken(t *testing.T) {
	if _, err := domain.ParseToken(fcmToken); err != nil {
		t.Errorf("an FCM token: %v", err)
	}
	if _, err := domain.ParseToken(strings.Repeat("a", 4096)); err != nil {
		t.Errorf("4096 characters: %v", err)
	}
	for _, bad := range []string{
		"", strings.Repeat("a", 31), strings.Repeat("a", 4097),
		strings.Repeat("a", 31) + " ", strings.Repeat("a", 31) + "/", strings.Repeat("a", 31) + "\n",
		strings.Repeat("ب", 40),
	} {
		if _, err := domain.ParseToken(bad); !errors.Is(err, domain.ErrBadToken) {
			t.Errorf("ParseToken(%q) = %v, want ErrBadToken", bad, err)
		}
	}
}

// A token is a credential: logging or printing a device never shows it.
func TestTokenNeverPrinted(t *testing.T) {
	token, err := domain.ParseToken(fcmToken)
	if err != nil {
		t.Fatal(err)
	}
	if token.Reveal() != fcmToken {
		t.Errorf("Reveal = %q", token.Reveal())
	}
	d := domain.Device{ID: shared.NewID[domain.DeviceTag](), Token: token, Platform: domain.Android, Locale: shared.Arabic}
	var logs bytes.Buffer
	for _, h := range []slog.Handler{slog.NewJSONHandler(&logs, nil), slog.NewTextHandler(&logs, nil)} {
		slog.New(h).Info("device", slog.Any("token", token), slog.Any("device", d))
	}
	printed := []string{
		logs.String(),
		fmt.Sprint(token), fmt.Sprintf("%v %+v %s %q", token, token, token, token),
		fmt.Sprintf("%v %+v", d, d), fmt.Errorf("push to %+v: %w", d, errors.New("failed")).Error(),
	}
	for _, s := range printed {
		if strings.Contains(s, fcmToken) || strings.Contains(s, fcmToken[:20]) {
			t.Errorf("token printed: %s", s)
		}
	}
	if !strings.Contains(fmt.Sprint(token), "[redacted]") {
		t.Errorf("fmt.Sprint(token) = %q, want [redacted]", fmt.Sprint(token))
	}
}

func TestParsePlatform(t *testing.T) {
	for _, p := range []string{"ios", "android"} {
		if got, err := domain.ParsePlatform(p); err != nil || string(got) != p {
			t.Errorf("ParsePlatform(%q) = %q, %v", p, got, err)
		}
	}
	for _, p := range []string{"", "iOS", "web", "windows"} {
		if _, err := domain.ParsePlatform(p); !errors.Is(err, domain.ErrBadPlatform) {
			t.Errorf("ParsePlatform(%q) = %v, want ErrBadPlatform", p, err)
		}
	}
}

func TestAppointmentStatusOf(t *testing.T) {
	for booking, want := range map[string]domain.AppointmentStatus{
		"pending": domain.AppointmentPending, "confirmed": domain.AppointmentConfirmed,
		"rejected": domain.AppointmentClosed, "cancelled": domain.AppointmentClosed, "expired": domain.AppointmentClosed,
		"completed": domain.AppointmentClosed, "no_show": domain.AppointmentClosed,
	} {
		if got, ok := domain.AppointmentStatusOf(booking); !ok || got != want {
			t.Errorf("AppointmentStatusOf(%q) = %q, %v; want %q", booking, got, ok, want)
		}
	}
	for _, unknown := range []string{"", "rescheduled", "Confirmed"} {
		if got, ok := domain.AppointmentStatusOf(unknown); ok {
			t.Errorf("AppointmentStatusOf(%q) = %q, want unknown", unknown, got)
		}
	}
}
