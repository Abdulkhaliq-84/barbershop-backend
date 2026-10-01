package shared

import (
	"errors"
	"slices"
)

// ErrUnknownCity reports a city code that isn't on the list.
var ErrUnknownCity = errors.New("unknown city")

// City is a city the app serves. A branch is filed under one and customers
// browse by them, so cities come from this fixed list (reference data in
// code, like catalog's categories) rather than free text that would spell
// Riyadh five ways. business checks a branch's city against it; discovery
// lists branches by it.
type City struct {
	code string
	name LocalizedText
}

// Code is the city's stable identifier, e.g. "riyadh". Stored and sent in
// the API; never renamed.
func (c City) Code() string { return c.code }

// Name is the city's name in Arabic and English.
func (c City) Name() LocalizedText { return c.name }

// cities is the list the app shows: the largest cities first. Add cities at
// the end of their region; never change a code.
var cities = []City{
	city("riyadh", "الرياض", "Riyadh"),
	city("jeddah", "جدة", "Jeddah"),
	city("makkah", "مكة المكرمة", "Makkah"),
	city("madinah", "المدينة المنورة", "Madinah"),
	city("dammam", "الدمام", "Dammam"),
	city("al_khobar", "الخبر", "Al Khobar"),
	city("dhahran", "الظهران", "Dhahran"),
	city("al_ahsa", "الأحساء", "Al Ahsa"),
	city("qatif", "القطيف", "Qatif"),
	city("jubail", "الجبيل", "Jubail"),
	city("taif", "الطائف", "Taif"),
	city("tabuk", "تبوك", "Tabuk"),
	city("buraydah", "بريدة", "Buraydah"),
	city("unaizah", "عنيزة", "Unaizah"),
	city("hail", "حائل", "Hail"),
	city("abha", "أبها", "Abha"),
	city("khamis_mushait", "خميس مشيط", "Khamis Mushait"),
	city("jazan", "جازان", "Jazan"),
	city("najran", "نجران", "Najran"),
	city("al_baha", "الباحة", "Al Baha"),
	city("yanbu", "ينبع", "Yanbu"),
	city("al_kharj", "الخرج", "Al Kharj"),
	city("arar", "عرعر", "Arar"),
	city("sakaka", "سكاكا", "Sakaka"),
}

func city(code, ar, en string) City {
	return City{code: code, name: LocalizedText{ar: ar, en: en}}
}

// Cities returns every city, in the order the app lists them.
func Cities() []City { return slices.Clone(cities) }

// ParseCity returns the city with this code.
func ParseCity(code string) (City, error) {
	i := slices.IndexFunc(cities, func(c City) bool { return c.code == code })
	if i < 0 {
		return City{}, ErrUnknownCity
	}
	return cities[i], nil
}
