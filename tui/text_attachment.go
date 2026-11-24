package tui

import "strings"

const defaultTextAttachmentName = "message.txt"

type TextAttachmentPrompt struct {
	fileInput
	status string
}

func (p *TextAttachmentPrompt) Key(name, text string) (action, filename string) {
	switch name {
	case "escape", "esc":
		return "close", ""
	case "enter":
		filename := strings.TrimSpace(string(p.input))
		if filename == "" {
			filename = defaultTextAttachmentName
		}

		if filename != AttachmentName(filename) || strings.ContainsAny(filename, "/\\") {
			p.status = "Enter a file name without a directory"
			return "", ""
		}

		if len(filename) > 255 {
			p.status = "File name is too long"
			return "", ""
		}

		return "edit", filename
	default:
		if p.key(name, text) {
			p.status = ""
		}
	}

	return "", ""
}

func (p *TextAttachmentPrompt) Render(width int, render func(string, bool, bool, bool)) {
	render(Clip("Text attachment name", width), true, false, false)
	p.render(width, defaultTextAttachmentName, render)
	render("", false, false, false)

	if p.status != "" {
		render(Clip(p.status, width), false, false, false)
	}

	render(Clip("Enter edit in Vim  Esc back", width), false, false, true)
}
