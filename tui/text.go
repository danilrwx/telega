// Package tui provides shared terminal text and color primitives.
package tui

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// Wrap wraps plain text by terminal cells without splitting graphemes.
// It returns at most limit lines. A nonpositive limit removes the line cap.
func Wrap(text string, width, limit int) []string {
	if width <= 0 {
		return []string{""}
	}

	lines := make([]string, 0, 4)

	var line strings.Builder

	cells := 0

	chars := uniseg.NewGraphemes(text)
	for chars.Next() {
		word, span := chars.Str(), chars.Width()

		newline := word == "\n" || word == "\r\n"
		if !newline {
			word, span = printable(word, span)
			if span > width {
				word, span = "…", 1
			}
		}

		if newline || span+cells > width {
			lines = append(lines, line.String())
			if limit > 0 && len(lines) == limit {
				return lines
			}

			line.Reset()

			cells = 0
		}

		if !newline {
			line.WriteString(word)

			cells += span
		}
	}

	return append(lines, line.String())
}

// Clip returns the first line, shortened to width cells with an ellipsis if needed.
func Clip(text string, width int) string {
	if width <= 0 {
		return ""
	}

	var line strings.Builder

	cells, ellipsisAt := 0, 0

	chars := uniseg.NewGraphemes(text)
	for chars.Next() {
		word, span := chars.Str(), chars.Width()
		if word == "\n" || word == "\r\n" {
			break
		}

		word, span = printable(word, span)
		if cells+span > width {
			return line.String()[:ellipsisAt] + "…"
		}

		line.WriteString(word)

		cells += span
		if cells < width {
			ellipsisAt = line.Len()
		}
	}

	return line.String()
}

func printable(glyph string, span int) (string, int) {
	if strings.ContainsFunc(glyph, unicode.IsControl) {
		return " ", 1
	}

	return glyph, span
}
