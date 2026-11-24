package tui

// ListDialog lays out a title, a scrollable choice list, and its actions.
func ListDialog(title string, choices []string, selected, width, height int, status, hint string, render func(row string, title, selected, muted bool)) {
	listDialog(title, "", choices, selected, width, height, status, hint, render)
}

// SearchListDialog shows an active search field above a scrollable choice list.
func SearchListDialog(title, query string, choices []string, selected, width, height int, status, hint string, render func(string, bool, bool, bool)) {
	listDialog(title, query, choices, selected, width, height, status, hint, render)
}

func listDialog(title, query string, choices []string, selected, width, height int, status, hint string, render func(string, bool, bool, bool)) {
	footer := 2
	if status != "" {
		footer++
	}

	room := max(1, height-2-footer)
	start := max(0, min(selected-room+1, len(choices)-room))

	render(Clip(title, width), true, false, false)
	render(Clip(query, width), false, false, false)

	for i, choice := range choices[start:min(len(choices), start+room)] {
		prefix := "  "

		active := start+i == selected
		if active {
			prefix = "› "
		}

		render(Clip(prefix+choice, width), false, active, false)
	}

	render("", false, false, false)

	if status != "" {
		render(Clip(status, width), false, false, false)
	}

	if width < 28 {
		switch hint {
		case OpenAttachmentHint:
			hint = "jk  Enter  Esc"
		case ReactionAuthorsHint:
			hint = "jk scroll  Esc"
		case DraftAttachmentListHint(0):
			hint = "a attach  Esc"
		case DraftAttachmentListHint(1):
			hint = "a/e/d Enter Esc"
		}
	}

	render(Clip(hint, width), false, false, true)
}

const (
	DraftAttachmentsTitle   = "Draft attachments"
	HistoryAttachmentsTitle = "Attachments in loaded messages"
	OpenAttachmentHint      = "jk choose  Enter open  Esc back"
)

// ChatSearchList renders keyboard-driven recipient and chat searches.
func ChatSearchList(title, query string, choices []string, selected, width, height int, status string, row func(string, bool, bool, bool)) {
	field := "/ " + query + "▏"
	if query == "" {
		field += " Search chat or @username"
	}

	if status == "" && len(choices) == 0 {
		status = "No matching chats"
	}

	hint := "↑↓ choose  Enter select  Esc back"
	if width < 34 {
		hint = "↑↓ Enter Esc"
	}

	SearchListDialog(title, field, choices, selected, width, height, status, hint, row)
}
