package shared

import (
	"errors"
	"slices"
)

// ErrUnknownCategory reports a category code that isn't on the list.
var ErrUnknownCategory = errors.New("unknown category")

// Category is a kind of service: a haircut, a beard trim, a package.
// catalog files each service under one and discovery finds branches by
// them, so both need exactly the same codes: reference data in code, like
// cities. Platform admins may manage them later.
type Category struct {
	code string
	name LocalizedText
	icon string
}

// Code is the category's stable identifier, e.g. "haircut". Stored and
// sent in the API; never renamed.
func (c Category) Code() string { return c.code }

// Name is the category's name in Arabic and English.
func (c Category) Name() LocalizedText { return c.name }

// Icon is the name of an icon the app knows.
func (c Category) Icon() string { return c.icon }

// categories, in the order the app shows them. Add new ones at the end;
// never change a code.
var categories = []Category{
	category("haircut", "قص الشعر", "Haircut", "scissors"),
	category("beard", "اللحية", "Beard", "beard"),
	category("shave", "الحلاقة", "Shave", "razor"),
	category("kids", "الأطفال", "Kids", "child"),
	category("skincare", "العناية بالبشرة", "Skin care", "face"),
	category("colour", "الصبغة", "Colour", "palette"),
	category("packages", "الباقات", "Packages", "package"),
}

func category(code, ar, en, icon string) Category {
	return Category{code: code, name: LocalizedText{ar: ar, en: en}, icon: icon}
}

// Categories returns every category, in display order.
func Categories() []Category { return slices.Clone(categories) }

// ParseCategory returns the category with this code.
func ParseCategory(code string) (Category, error) {
	i := slices.IndexFunc(categories, func(c Category) bool { return c.code == code })
	if i < 0 {
		return Category{}, ErrUnknownCategory
	}
	return categories[i], nil
}
