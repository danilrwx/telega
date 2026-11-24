package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type PopupRole uint8

const (
	PopupText PopupRole = iota
	PopupBorder
	PopupTitle
	PopupSelected
	PopupMuted
)

const (
	ListPopupWidth = 112
	FormPopupWidth = 64
)

type DialogPopup struct {
	X, Y  int
	Lines []string
}

// FloatingDialog centers a bordered dialog in terminal cells.
func FloatingDialog(cols, rows, maxWidth int, content func(int, int, func(string, bool, bool, bool)), style func(string, PopupRole) string) DialogPopup {
	width := min(maxWidth, cols-min(10, max(4, cols/12))) - 4
	height := min(28, max(7, rows*3/4)) - 2
	compact := height < 6

	var lines []string

	content(width, height, func(text string, title, selected, muted bool) {
		if compact && text == "" {
			return
		}

		role := PopupText

		switch {
		case title:
			role = PopupTitle
		case selected:
			role = PopupSelected
		case muted:
			role = PopupMuted
		}

		pad := strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
		lines = append(lines, style("│ ", PopupBorder)+style(text, role)+style(pad+" │", PopupBorder))
	})

	top := style("╭"+strings.Repeat("─", width+2)+"╮", PopupBorder)
	bottom := style("╰"+strings.Repeat("─", width+2)+"╯", PopupBorder)
	lines = append(append([]string{top}, lines...), bottom)

	return DialogPopup{X: (cols - width - 4) / 2, Y: max(0, (rows-len(lines)+1)/2), Lines: lines}
}

// Overlay places a floating dialog over a frame rendered as newline-separated text.
func (p DialogPopup) Overlay(background string, cols, rows int) string {
	if len(p.Lines) == 0 {
		return background
	}

	lines := strings.Split(WithoutInlineImages(background), "\n")
	for len(lines) < rows {
		lines = append(lines, "")
	}

	for i, popup := range p.Lines {
		y := p.Y + i
		if y >= rows {
			break
		}

		line := ansi.Truncate(lines[y], cols, "")
		right := p.X + ansi.StringWidth(popup)
		leftText := ansi.Cut(line, 0, p.X)
		rightText := ansi.Cut(line, right, cols)

		room := max(0, ansi.StringWidth(line)-right)
		if extra := ansi.StringWidth(rightText) - room; extra > 0 {
			// The suffix starts inside a wide character covered by the popup.
			rightText = ansi.Cut(line, right+extra, cols)
		}

		leftPad := max(0, p.X-ansi.StringWidth(leftText))
		rightPad := max(0, room-ansi.StringWidth(rightText))
		lines[y] = leftText + strings.Repeat(" ", leftPad) + popup + strings.Repeat(" ", rightPad) + rightText
	}

	return strings.Join(lines[:rows], "\n")
}
