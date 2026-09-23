package shared

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrInvalidID reports an ID that is not a non-nil UUID.
var ErrInvalidID = errors.New("invalid id")

// ID identifies an entity. T is a marker type that exists only at compile
// time: ID[UserTag] and ID[BranchTag] are different types, so passing a
// BranchID where a UserID is expected fails to compile instead of corrupting
// data in production. (In TypeScript you'd fake this with "branded" types.)
type ID[T any] struct {
	value uuid.UUID
}

// Markers for the IDs several modules share. Modules declare their own
// markers for IDs nobody else needs (e.g. booking's appointments).
type (
	UserTag     struct{}
	BusinessTag struct{}
	BranchTag   struct{}
	StaffTag    struct{}
)

// Cross-module IDs.
type (
	UserID     = ID[UserTag]
	BusinessID = ID[BusinessTag]
	BranchID   = ID[BranchTag]
	StaffID    = ID[StaffTag]
)

// NewID returns a new UUIDv7 ID. Version 7 UUIDs start with a timestamp, so
// new rows land at the end of B-tree indexes and IDs sort by creation time.
//
//	id := shared.NewID[shared.UserTag]() // a UserID
func NewID[T any]() ID[T] {
	// NewV7 fails only if the OS random source fails, which Go treats as fatal.
	return ID[T]{value: uuid.Must(uuid.NewV7())}
}

// ParseID parses the text form of an ID, e.g. from a URL path.
func ParseID[T any](s string) (ID[T], error) {
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return ID[T]{}, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return ID[T]{value: u}, nil
}

// IDFromUUID wraps a UUID read from a trusted source such as the database.
func IDFromUUID[T any](u uuid.UUID) ID[T] {
	return ID[T]{value: u}
}

// UUID returns the underlying UUID, for adapters (database, API).
func (id ID[T]) UUID() uuid.UUID { return id.value }

// String returns the canonical text form.
func (id ID[T]) String() string { return id.value.String() }

// IsZero reports whether the ID was never set.
func (id ID[T]) IsZero() bool { return id.value == uuid.Nil }

// MarshalText makes IDs encode as plain strings in JSON and logs.
func (id ID[T]) MarshalText() ([]byte, error) { return id.value.MarshalText() }

// UnmarshalText parses the text form; the nil UUID is rejected.
func (id *ID[T]) UnmarshalText(b []byte) error {
	parsed, err := ParseID[T](string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
