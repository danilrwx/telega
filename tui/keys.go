package tui

import "unicode"

// RussianCommandRune maps a Russian keyboard position to its US layout equivalent.
func RussianCommandRune(r rune) rune {
	switch r {
	case '.':
		return '/'
	case ',':
		return '?'
	}

	const (
		lower = "qwertyuiop[]asdfghjkl;'zxcvbnm,.`"
		upper = "QWERTYUIOP{}ASDFGHJKL:\"ZXCVBNM<>~"
	)

	for i, candidate := range []rune("йцукенгшщзхъфывапролджэячсмитьбюё") {
		if unicode.ToLower(r) == candidate {
			if unicode.IsUpper(r) {
				return rune(upper[i])
			}

			return rune(lower[i])
		}
	}

	return r
}
