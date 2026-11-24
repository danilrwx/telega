package tui

import "strings"

// MessageSearch manages the shared query and result controls. The app owns the
// messages and its server pagination cursor.
type MessageSearch struct {
	input                 fileInput
	Scope                 string
	Global                bool
	Rows, FilterNames     []string
	FilterIndex           int
	Selected              int
	Submitted, Busy, More bool
	Status                string
	Revision              uint64
}

func (s *MessageSearch) Query() string {
	return strings.TrimSpace(string(s.input.input))
}

func (s *MessageSearch) Key(name, text string) string {
	switch name {
	case "escape", "esc", "ctrl+f", "messageSearch":
		return "close"
	case "tab":
		if s.Scope != "" {
			s.Global = !s.Global
			s.clearResults()
		}
	case "shift+tab":
		if len(s.FilterNames) > 0 {
			s.FilterIndex = (s.FilterIndex + 1) % len(s.FilterNames)
			s.clearResults()
		}
	case "up", "ctrl+p", "previousChat":
		s.Selected = max(0, s.Selected-1)
	case "down", "ctrl+n", "nextChat":
		s.Selected = min(max(0, len(s.Rows)-1), s.Selected+1)
	case "pageup", "pgup":
		s.Selected = max(0, s.Selected-8)
	case "pagedown", "pgdown":
		if s.More && !s.Busy {
			s.Busy = true
			return "more"
		}
	case "enter":
		switch {
		case s.Busy:
		case s.Submitted && len(s.Rows) > 0:
			return "open"
		case s.Query() != "" || s.FilterIndex > 0:
			s.Busy = true
			return "search"
		}
	default:
		if s.input.key(name, text) {
			s.clearResults()
		}
	}

	return ""
}

func (s *MessageSearch) clearResults() {
	s.Rows, s.Status, s.Selected = nil, "", 0
	s.Submitted, s.Busy, s.More = false, false, false
	s.Revision++
}

func (s *MessageSearch) SetResults(rows []string, more, appendPage bool, status string) {
	s.Busy, s.More, s.Status = false, more, status

	s.Submitted = status == "" || appendPage
	if status != "" {
		return
	}

	if appendPage {
		if len(rows) > 0 {
			s.Selected = len(s.Rows)
		}

		s.Rows = append(s.Rows, rows...)
	} else {
		s.Rows, s.Selected = rows, 0
	}
}

func (s *MessageSearch) Render(width, height int, row func(string, bool, bool, bool)) {
	scope := s.Scope
	if s.Global || scope == "" {
		scope = "All chats"
	}

	var field string

	title := "Search messages"
	placeholder := "Type a message query"

	if s.FilterIndex > 0 {
		title = "Search " + strings.ToLower(s.FilterNames[s.FilterIndex])
		placeholder = "Type to filter or press Enter"
	}

	s.input.render(width-2, placeholder, func(text string, _, _, _ bool) {
		field = "/ " + text
	})

	status := s.Status
	switch {
	case s.Busy:
		status = "Searching…"
	case status == "" && s.Submitted && len(s.Rows) == 0:
		status = "No matching messages"
	}

	hint := "↑↓ choose  Enter search/open"
	if s.More {
		hint += "  PgDn more"
	}

	if s.Scope != "" {
		hint += "  Tab scope"
	}

	if len(s.FilterNames) > 0 {
		hint += "  Shift+Tab type"
	}

	hint += "  Esc back"
	if width < 55 || len(s.FilterNames) > 0 && width < 75 {
		hint = "↑↓ Enter"
		if s.More {
			hint = "↑↓ ↵ PgDn"
		}

		if s.Scope != "" {
			hint += " ⇥"
		}

		if len(s.FilterNames) > 0 {
			hint += " Shift+Tab"
		}

		hint += " Esc"
	}

	SearchListDialog(title+" · "+scope, field, s.Rows, s.Selected, width, height, status, hint, row)
}
