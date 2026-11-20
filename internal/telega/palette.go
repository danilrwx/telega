package telega

type terminalPalette struct {
	heading, muted, selected, date, unread, outgoing, attachment, codeSGR string
}

var standardPalette = terminalPalette{
	heading: "\x1b[0m", muted: "\x1b[0m",
	selected: "\x1b[7m", date: "\x1b[36m", unread: "\x1b[1m",
	outgoing: "\x1b[1;36m", attachment: "\x1b[36m", codeSGR: "36",
}

var ansiDarkPalette = terminalPalette{
	heading: "\x1b[1;35m", muted: "\x1b[2m",
	selected: "\x1b[7m", date: "\x1b[1;36m", unread: "\x1b[1;33m",
	outgoing: "\x1b[1;32m", attachment: "\x1b[35m", codeSGR: "36",
}

var pastelPalette = terminalPalette{
	heading:    "\x1b[1;38;2;212;182;229m",
	muted:      "\x1b[38;2;152;151;174m",
	selected:   "\x1b[48;2;57;46;79;38;2;242;229;255m",
	date:       "\x1b[38;2;168;186;242m",
	unread:     "\x1b[1;38;2;232;205;154m",
	outgoing:   "\x1b[1;38;2;161;216;207m",
	attachment: "\x1b[38;2;212;182;229m",
	codeSGR:    "38;2;168;186;242",
}

func (a *app) palette() *terminalPalette {
	if a.colors != nil {
		return a.colors
	}

	return &standardPalette
}

func choosePalette(theme, colorTerm string) *terminalPalette {
	if theme != "dark" {
		return &standardPalette
	}

	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return &pastelPalette
	}

	return &ansiDarkPalette
}
