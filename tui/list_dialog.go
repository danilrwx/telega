package tui

// ListDialog lays out a title, a scrollable choice list, and its actions.
func ListDialog(title string, choices []string, selected, width, height int, status, hint string, render func(row string, title, selected, muted bool)) {
	footer := 2
	if status != "" {
		footer++
	}

	room := max(1, height-2-footer)
	start := max(0, min(selected-room+1, len(choices)-room))

	render(Clip(title, width), true, false, false)
	render("", false, false, false)

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

	render(Clip(hint, width), false, false, true)
}

const (
	DraftAttachmentsTitle   = "Draft attachments"
	HistoryAttachmentsTitle = "Attachments in loaded messages"
	OpenAttachmentHint      = "jk choose  Enter open  Esc back"
)
