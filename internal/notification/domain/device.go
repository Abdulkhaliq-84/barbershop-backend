// Package domain is notification's model: the devices that receive pushes,
// what to tell whom when something happens, and how to say it in Arabic and
// English (docs/architecture/domain-model.md §3.8, ADR-0033).
package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Errors.
var (
	ErrNotFound      = errors.New("notification: not found")
	ErrBadToken      = errors.New("notification: not a push token")
	ErrBadPlatform   = errors.New("notification: platform must be ios or android")
	ErrUnknownBranch = errors.New("notification: branch not heard of yet")
)

// MaxDevicesPerUser is how many devices one user keeps: registering another
// drops the one registered longest ago, so a user can't make one event fan
// out to thousands of pushes.
const MaxDevicesPerUser = 10

// DeviceTag marks device IDs (only notification uses them).
type DeviceTag struct{}

// DeviceID identifies a registered device.
type DeviceID = shared.ID[DeviceTag]

// Platform is the device's operating system.
type Platform string

// Platforms.
const (
	IOS     Platform = "ios"
	Android Platform = "android"
)

// ParsePlatform checks p names a platform.
func ParsePlatform(p string) (Platform, error) {
	switch Platform(p) {
	case IOS, Android:
		return Platform(p), nil
	}
	return "", ErrBadPlatform
}

// Token is a push service's registration token for one app install. It lets
// anyone holding it push to that phone, so it is treated as a credential:
// never logged, never sent back to clients.
type Token struct{ value string }

// tokenChars is what FCM and APNs tokens are made of: URL-safe characters.
// (Go's regexp can't count to 4096, so the length is checked apart.)
var tokenChars = regexp.MustCompile(`^[A-Za-z0-9:_\-.]+$`)

// Token lengths, in bytes (the characters allowed are all one byte).
const (
	minTokenLen = 32
	maxTokenLen = 4096
)

// ParseToken checks s looks like a push token.
func ParseToken(s string) (Token, error) {
	if len(s) < minTokenLen || len(s) > maxTokenLen || !tokenChars.MatchString(s) {
		return Token{}, ErrBadToken
	}
	return Token{value: s}, nil
}

// Reveal returns the token itself: for storing it and for the push service,
// nothing else.
func (t Token) Reveal() string { return t.value }

const redacted = "[redacted]"

// LogValue keeps the token out of logs, even if a whole Device is logged.
func (t Token) LogValue() slog.Value { return slog.StringValue(redacted) }

// Format keeps the token out of fmt output and wrapped errors, including %+v
// of a whole Device.
func (t Token) Format(s fmt.State, verb rune) {
	if verb == 'q' {
		_, _ = fmt.Fprintf(s, "%q", redacted)
		return
	}
	_, _ = fmt.Fprint(s, redacted)
}

// Device is an app install that can receive pushes, for one user at a time.
// Locale is the app's language on it: what its pushes are written in.
type Device struct {
	ID        DeviceID
	User      shared.UserID
	Token     Token
	Platform  Platform
	Locale    shared.Language
	CreatedAt time.Time
	UpdatedAt time.Time // when it last registered
}

// Branch is notification's copy of what a message says about a branch, as
// of Version (the branch's).
type Branch struct {
	ID       shared.BranchID
	Version  int
	Name     shared.LocalizedText
	Timezone string // IANA; a message gives times in it
}

// Outcome is what the push service did with a push.
type Outcome string

// Outcomes.
const (
	Sent     Outcome = "sent"     // accepted for delivery
	Rejected Outcome = "rejected" // refused for good: not tried again
)

// Delivery records one event pushed to one device, and what came of it.
type Delivery struct {
	Event       uuid.UUID
	Device      DeviceID
	User        shared.UserID
	Kind        Kind
	Appointment shared.AppointmentID
	Outcome     Outcome
	SentAt      time.Time
}

// Store keeps devices, branches and deliveries.
type Store interface {
	// SaveDevice registers d: a new device, or the one with its token,
	// moved to d.User if another user had it. It returns the device as
	// stored, and drops the user's devices beyond the newest
	// MaxDevicesPerUser.
	SaveDevice(ctx context.Context, d Device) (Device, error)
	// RemoveDevice unregisters the user's device; ErrNotFound if the user
	// has no such device.
	RemoveDevice(ctx context.Context, user shared.UserID, id DeviceID) error
	// ForgetDevice drops a device the push service no longer knows,
	// whoever's it is. Forgetting one already gone is harmless.
	ForgetDevice(ctx context.Context, id DeviceID) error
	// Devices returns the user's devices, newest first.
	Devices(ctx context.Context, user shared.UserID) ([]Device, error)
	// KeepBranch saves b unless the copy is of the same or a newer version.
	KeepBranch(ctx context.Context, b Branch) (bool, error)
	// Branch returns the copy; ErrUnknownBranch if none yet.
	Branch(ctx context.Context, id shared.BranchID) (Branch, error)
	// KeepAppointment applies a booking event to the copy of an
	// appointment: a status only moves forward.
	KeepAppointment(ctx context.Context, a Appointment) error
	// Appointment returns the copy; ErrNotFound if none.
	Appointment(ctx context.Context, id shared.AppointmentID) (Appointment, error)
	// ClaimReminders finds up to limit appointments due their reminder at
	// now, marks them reminded and queues one reminder each (the outbox),
	// all in one transaction, and returns how many.
	ClaimReminders(ctx context.Context, now time.Time, limit int) (int, error)
	// Delivered reports whether event was already pushed to device, or
	// refused by the push service for it.
	Delivered(ctx context.Context, event uuid.UUID, device DeviceID) (bool, error)
	// RecordDelivery logs a push's outcome; recording one twice is harmless.
	RecordDelivery(ctx context.Context, d Delivery) error
}
