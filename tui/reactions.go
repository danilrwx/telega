package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
)

// ReactionOption supplies an emoji and its text label for the reaction picker.
type ReactionOption struct{ Emoji, Label string }

// ReactionOptions builds the common quick picker with a service-specific laugh emoji.
func ReactionOptions(laugh string, extra ...ReactionOption) []ReactionOption {
	return append([]ReactionOption{
		{"👍", "Like"},
		{"❤", "Heart"},
		{"🔥", "Fire"},
		{laugh, "Laugh"},
		{"🎉", "Party"},
		{"👎", "No"},
	}, extra...)
}

// QuickReaction returns the emoji assigned to a digit in the reaction picker.
func QuickReaction(key string, options []ReactionOption) string {
	if len(key) != 1 || key[0] < '1' || int(key[0]-'1') >= len(options) {
		return ""
	}

	return options[key[0]-'1'].Emoji
}

// ReactionFooter lays out the quick reactions in the two conversation footer rows.
func ReactionFooter(status string, width int, textOnly bool, options []ReactionOption) (string, string) {
	var choices []string

	for i, choice := range options {
		caption := choice.Emoji
		if textOnly {
			caption = choice.Label
		}

		choices = append(choices, fmt.Sprintf("%d %s", i+1, caption))
	}

	keys := strings.Join(choices, "  ") + "  Esc cancel"
	if uniseg.StringWidth(keys) <= width {
		return Clip(status, width), keys
	}

	mid := len(choices) / 2
	first := strings.Join(choices[:mid], " ")

	last := strings.Join(choices[mid:], " ")
	if !textOnly && uniseg.StringWidth(last+" Esc") > width {
		for i := mid; i < len(choices); i++ {
			choices[i] = strings.Replace(choices[i], " ", "", 1)
		}

		last = strings.Join(choices[mid:], " ")
	}

	if uniseg.StringWidth(last+" Esc") <= width {
		last += " Esc"
	}

	return Clip(first, width), Clip(last, width)
}

// ReactionSuggestion shows how to join an existing reaction without changing the digit shortcuts.
func ReactionSuggestion(label string, count, index, total, width int) string {
	if total == 0 {
		return ""
	}

	hint := ""
	if total > 1 {
		hint = fmt.Sprintf("  ←/→ %d/%d", index+1, total)
	}

	suffix := fmt.Sprintf(" %d%s", count, hint)

	return Clip("Enter "+Clip(label, max(1, width-6-uniseg.StringWidth(suffix)))+suffix, width)
}

// ReactionLine fits a reaction summary into one row inside a message.
func ReactionLine(summary string, width int, textOnly bool) string {
	prefix := "↳ "
	if textOnly {
		prefix = "  "
	}

	return Clip(prefix+summary, width)
}
