package tui

import (
	"fmt"

	"github.com/rivo/uniseg"
)

// Confirmation describes a question and its actions in the conversation footer.
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
