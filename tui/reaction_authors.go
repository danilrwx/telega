package tui

const (
	ReactionAuthorsTitle = "Reaction authors"
	ReactionAuthorsHint  = "jk scroll  Esc back"
)

type ReactionAuthors struct {
	Rows     []string
	Status   string
	Selected int
}

func (a *ReactionAuthors) Key(name string) (close bool) {
	switch name {
	case "escape", "esc", "h", "left", "q", "Z":
		return true
	case "up", "k":
		a.Selected = max(0, a.Selected-1)
	case "down", "j":
		a.Selected = min(max(0, len(a.Rows)-1), a.Selected+1)
	case "home", "g":
		a.Selected = 0
	case "end", "G":
		a.Selected = max(0, len(a.Rows)-1)
	}

	return false
}

func (a *ReactionAuthors) SetRows(rows []string, status string) {
	a.Rows, a.Status = rows, status
	if len(rows) == 0 && status == "" {
		a.Status = "No reactions on this message"
	}
}
