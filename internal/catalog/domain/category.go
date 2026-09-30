// Package domain holds catalog's rules: service categories and the services
// a branch sells (docs/architecture/domain-model.md §3.3).
package domain

import (
	"slices"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// CategoryCode names a category. Stored with each service and used by
// customer search filters (M6).
type CategoryCode string

// Category is platform reference data: what kind of service something is.
type Category struct {
	Code CategoryCode
	Name shared.LocalizedText
	Icon string // an icon name the app knows
}

// Categories, in the order the app shows them. Platform admins will manage
// them later; for now they live in code, like plans.
var categories = []Category{
	{Code: "haircut", Name: mustText("قص الشعر", "Haircut"), Icon: "scissors"},
	{Code: "beard", Name: mustText("اللحية", "Beard"), Icon: "beard"},
	{Code: "shave", Name: mustText("الحلاقة", "Shave"), Icon: "razor"},
	{Code: "kids", Name: mustText("الأطفال", "Kids"), Icon: "child"},
	{Code: "skincare", Name: mustText("العناية بالبشرة", "Skin care"), Icon: "face"},
	{Code: "colour", Name: mustText("الصبغة", "Colour"), Icon: "palette"},
	{Code: "packages", Name: mustText("الباقات", "Packages"), Icon: "package"},
}

// Categories returns every category, in display order.
func Categories() []Category { return slices.Clone(categories) }

// ParseCategory checks that code names a category.
func ParseCategory(code string) (CategoryCode, error) {
	for _, c := range categories {
		if string(c.Code) == code {
			return c.Code, nil
		}
	}
	return "", ErrUnknownCategory
}

func mustText(ar, en string) shared.LocalizedText {
	t, err := shared.NewLocalizedText(ar, en)
	if err != nil {
		panic(err)
	}
	return t
}
