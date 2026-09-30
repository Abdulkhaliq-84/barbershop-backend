// Package domain holds the business module's rules: businesses (tenants),
// their lifecycle and their staff. It is pure Go — no database, no HTTP —
// so every rule is tested with plain unit tests.
package domain

import "errors"

// Domain errors. The HTTP adapter maps each one to a stable API error code.
var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("your role does not allow this")
	ErrInvalidCRNumber   = errors.New("cr number: must be 10 digits")
	ErrLegalNameRequired = errors.New("legal name is required")
	ErrTextTooLong       = errors.New("text is too long")
	ErrAlreadyRegistered = errors.New("business: this owner already registered this cr number")
	ErrOwnerRequired     = errors.New("business: an owner is required")
	ErrUnknownRole       = errors.New("staff: unknown role")
	ErrUnknownStatus     = errors.New("business: unknown status")

	ErrInvalidStateTransition = errors.New("business: not allowed in its current status")
	ErrVersionConflict        = errors.New("changed since you read it")

	ErrInvalidCityCode = errors.New("branch: unknown city code")
	ErrAddressRequired = errors.New("branch: address is required")
	ErrInvalidTimezone = errors.New("branch: not an IANA time zone")

	ErrUnknownDocumentKind  = errors.New("verification: unknown document kind")
	ErrDocumentLimitReached = errors.New("verification: too many documents")
	ErrEmptyFile            = errors.New("verification: the file is empty")
	ErrFileTooLarge         = errors.New("verification: the file is too large")
	ErrUnsupportedFile      = errors.New("verification: only PDF, JPEG and PNG files are accepted")
)
