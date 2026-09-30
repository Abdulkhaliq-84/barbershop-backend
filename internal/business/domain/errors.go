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
	// ErrBranchNotReady: the branch can't take a booking yet (see
	// NotReadyError for what's missing).
	ErrBranchNotReady = errors.New("branch: not ready to publish")
	// ErrBusinessNotActive: only an approved, active business trades.
	ErrBusinessNotActive = errors.New("business: not active")

	ErrUnknownDocumentKind  = errors.New("verification: unknown document kind")
	ErrDocumentLimitReached = errors.New("verification: too many documents")
	ErrEmptyFile            = errors.New("verification: the file is empty")
	ErrFileTooLarge         = errors.New("verification: the file is too large")
	ErrUnsupportedFile      = errors.New("verification: only PDF, JPEG and PNG files are accepted")

	ErrCRDocumentRequired      = errors.New("review: upload the CR certificate before submitting")
	ErrBranchRequired          = errors.New("review: add a branch before submitting")
	ErrRejectionReasonRequired = errors.New("review: a rejection needs a reason")
	ErrCRNumberClaimed         = errors.New("review: another business already uses this CR number")
	ErrNotPlatformAdmin        = errors.New("platform admins only")
	ErrSelfReview              = errors.New("review: nobody reviews their own business")

	ErrInvalidInviteRole   = errors.New("staff: invite a manager or a barber")
	ErrStaffNameRequired   = errors.New("staff: a name is required")
	ErrStaffBranchRequired = errors.New("staff: choose 1 to 50 branches")
	ErrUnknownBranch       = errors.New("staff: a branch is not in this business")
	ErrInvitationInvalid   = errors.New("staff: invitation invalid, expired or already used")
	ErrAlreadyStaff        = errors.New("staff: already works at this business")
	ErrInvitationClosed    = errors.New("staff: invitation already accepted or revoked")
	ErrBranchLimitReached  = errors.New("business: the plan's branch limit is reached")
	ErrStaffLimitReached   = errors.New("staff: the plan's staff limit is reached")
	ErrTooManyInvitations  = errors.New("staff: too many invitations; try again later")
	// ErrTooManyRegistrations: the user has MaxOpenRegistrations businesses
	// not yet approved.
	ErrTooManyRegistrations = errors.New("business: finish an earlier registration first")
)
