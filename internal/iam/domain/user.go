package domain

import (
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// UserStatus says whether a user may sign in.
type UserStatus string

// User statuses.
const (
	UserActive  UserStatus = "active"
	UserBlocked UserStatus = "blocked"
)

// PlatformRole is a user's role on the platform itself (not in a business:
// business roles come from staff membership and are checked per request).
type PlatformRole string

// Platform roles (iam.users.platform_role).
const (
	PlatformRoleNone  PlatformRole = "none"
	PlatformRoleAdmin PlatformRole = "admin" // approves businesses, blocks users
)

// User is a person who signs in with their phone number. Any user can book
// as a customer; business roles come from staff membership (business module).
type User struct {
	id        shared.UserID
	phone     shared.PhoneNumber
	name      string
	locale    shared.Language
	status    UserStatus
	role      PlatformRole
	createdAt time.Time
}

// NewUser registers a phone number. Name is collected later, in onboarding.
func NewUser(id shared.UserID, phone shared.PhoneNumber, locale shared.Language, now time.Time) *User {
	return &User{id: id, phone: phone, locale: locale, status: UserActive, role: PlatformRoleNone, createdAt: now}
}

// RehydrateUser rebuilds a user loaded from storage.
func RehydrateUser(id shared.UserID, phone shared.PhoneNumber, name string, locale shared.Language, status UserStatus, role PlatformRole, createdAt time.Time) *User {
	return &User{id: id, phone: phone, name: name, locale: locale, status: status, role: role, createdAt: createdAt}
}

// ID returns the user ID.
func (u *User) ID() shared.UserID { return u.id }

// Phone returns the phone number the user signs in with.
func (u *User) Phone() shared.PhoneNumber { return u.phone }

// Name returns the display name, "" until onboarding sets it.
func (u *User) Name() string { return u.name }

// Locale returns the preferred language.
func (u *User) Locale() shared.Language { return u.locale }

// PlatformRole returns the user's platform role.
func (u *User) PlatformRole() PlatformRole { return u.role }

// IsBlocked reports whether the platform blocked this user.
func (u *User) IsBlocked() bool { return u.status == UserBlocked }

// Status returns the account status.
func (u *User) Status() UserStatus { return u.status }

// CreatedAt returns when the user registered.
func (u *User) CreatedAt() time.Time { return u.createdAt }
