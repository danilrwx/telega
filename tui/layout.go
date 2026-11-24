package tui

import (
	"strings"

	"github.com/rivo/uniseg"
)

// SidebarWidth is the chat list width in cells, excluding its divider.
// A zero width means the chat list and conversation take turns.
func SidebarWidth(columns, preferred int) int {
	if columns < 64 {
		return 0
	}

	if preferred <= 0 {
		return min(columns/3, 30)
	}

	return min(max(20, preferred), 60, columns-33)
}

// LayoutSettingsName names the configuration shared by the terminal clients.
const LayoutSettingsName = "chat-tui"

type LayoutSettings struct {
	SidebarWidth int `json:"sidebar_width"`
}

// ComposerIndent is the number of cells before draft text inside its pane.
const ComposerIndent = 2

// ComposerTextWidth leaves the composer indent and a trailing cell free.
func ComposerTextWidth(width int) int {
	return max(1, width-ComposerIndent-1)
}

// ComposerRule draws the separator with the name of the current editor.
func ComposerRule(label string, width int) string {
	prefix := "─ " + label + " "
	return prefix + strings.Repeat("─", max(0, width-uniseg.StringWidth(prefix)))
}
