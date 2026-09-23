package shared

// WesternDigit converts a digit typed on an Arabic or Persian keyboard to its
// Western form: '٥' (Arabic-Indic) and '۵' (Eastern Arabic-Indic) become '5'.
// Western digits pass through; ok is false for anything that isn't a digit.
func WesternDigit(r rune) (digit rune, ok bool) {
	switch {
	case r >= '0' && r <= '9':
		return r, true
	case r >= '٠' && r <= '٩': // U+0660..U+0669 Arabic-Indic
		return '0' + (r - '٠'), true
	case r >= '۰' && r <= '۹': // U+06F0..U+06F9 Eastern Arabic-Indic
		return '0' + (r - '۰'), true
	default:
		return 0, false
	}
}
