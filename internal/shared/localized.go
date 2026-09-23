package shared

import (
	"errors"
	"strings"
)

// ErrArabicRequired reports localized text without an Arabic value.
var ErrArabicRequired = errors.New("arabic text is required")

// Language is a supported UI language.
type Language string

// Supported languages. Arabic is the default and the source language.
const (
	Arabic  Language = "ar"
	English Language = "en"
)

// ParseLanguage reads a language tag such as "ar", "ar-SA" or "en-US".
// Anything else falls back to Arabic, the app's default language.
func ParseLanguage(tag string) Language {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	if primary == string(English) {
		return English
	}
	return Arabic
}

// LocalizedText is text shown to users in Arabic and, optionally, English —
// branch names, service names, category names.
type LocalizedText struct {
	ar string
	en string
}

// NewLocalizedText trims both values. Arabic is required because it is the
// app's default language; English may be empty.
func NewLocalizedText(ar, en string) (LocalizedText, error) {
	ar, en = strings.TrimSpace(ar), strings.TrimSpace(en)
	if ar == "" {
		return LocalizedText{}, ErrArabicRequired
	}
	return LocalizedText{ar: ar, en: en}, nil
}

// Ar returns the Arabic text.
func (t LocalizedText) Ar() string { return t.ar }

// En returns the English text, which may be empty.
func (t LocalizedText) En() string { return t.en }

// In returns the text in lang, falling back to Arabic when there is no English.
func (t LocalizedText) In(lang Language) string {
	if lang == English && t.en != "" {
		return t.en
	}
	return t.ar
}
