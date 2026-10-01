// Package domain holds catalog's rules: the services a branch sells and
// who performs them (docs/architecture/domain-model.md §3.3).
package domain

import "github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"

// CategoryCode names a category: one of shared.Categories, which discovery
// shares to find branches by them. Stored with each service.
type CategoryCode string

// ParseCategory checks that code names a category.
func ParseCategory(code string) (CategoryCode, error) {
	if _, err := shared.ParseCategory(code); err != nil {
		return "", ErrUnknownCategory
	}
	return CategoryCode(code), nil
}
