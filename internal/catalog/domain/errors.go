package domain

import "errors"

// Errors. Adapters map them to HTTP answers.
var (
	ErrNotFound        = errors.New("catalog: not found")
	ErrForbidden       = errors.New("catalog: not allowed")
	ErrVersionConflict = errors.New("catalog: changed since it was read")

	ErrUnknownCategory = errors.New("catalog: unknown category")
	ErrNameTooLong     = errors.New("catalog: name is too long")
	ErrTextTooLong     = errors.New("catalog: description is too long")
	ErrInvalidDuration = errors.New("catalog: duration must be 5 to 480 minutes, in steps of 5")
	ErrInvalidPrice    = errors.New("catalog: price must be 0 to 100,000 SAR")
	ErrInvalidSort     = errors.New("catalog: sort order must be 0 to 1000")
)
