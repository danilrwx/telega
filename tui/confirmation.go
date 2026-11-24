package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/rivo/uniseg"
)

// Confirmation describes a question and its actions.
type Confirmation struct {
	Prompt, ShortPrompt, Action string
}

// Footer returns the status and key hints for a confirmation.
func (c Confirmation) Footer(width int) (status, keys string) {
	prompt := c.Prompt
	if c.ShortPrompt != "" && uniseg.StringWidth(prompt) > width {
		prompt = c.ShortPrompt
	}

	keys = "y " + c.Action + "  Esc cancel"
	if uniseg.StringWidth(keys) > width {
		keys = "y confirm  Esc cancel"
	}

	return Clip(prompt, width), Clip(keys, width)
}

// Render lays out a confirmation question and its keyboard actions.
func (c Confirmation) Render(width, _ int, row func(string, bool, bool, bool)) {
	_, keys := c.Footer(width)
	for _, line := range strings.Split(ansi.Wrap(c.Prompt, width, ""), "\n") {
		row(line, true, false, false)
	}

	if width < 21 {
		keys = "y yes  Esc no"
	}

	row("", false, false, false)
	row(keys, false, false, true)
}

// DeleteConfirmation builds the common prompt for deleting messages.
func DeleteConfirmation(count int, everyone bool) Confirmation {
	scope := "for you"
	if everyone {
		scope = "for everyone"
	}

	subject := fmt.Sprintf("%d messages", count)
	if count == 1 {
		subject = "message"
	}

	return Confirmation{
		Prompt:      "Delete " + subject + " " + scope + "?",
		ShortPrompt: "Delete " + scope + "?",
		Action:      "confirm deletion",
	}
}
