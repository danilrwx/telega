package telega

import (
	"fmt"
	"strconv"

	"github.com/danilrwx/telega/tui"
)

type terminalPalette struct {
	heading, muted, selected, date, unread, outgoing, attachment, codeSGR string
	rail, railMuted, badge, reaction                                      string
	draftRow                                                              string
}

var standardPalette = terminalPalette{
	heading: "\x1b[0m", muted: "\x1b[0m",
	selected: "\x1b[7m", date: "\x1b[36m", unread: "\x1b[1m",
	outgoing: "\x1b[1;36m", attachment: "\x1b[36m", codeSGR: "36",
	rail: "\x1b[36m", railMuted: "\x1b[2m", badge: "\x1b[36m", reaction: "\x1b[33m",
}

var ansiDarkPalette = terminalPalette{
	heading: "\x1b[1;35m", muted: "\x1b[2m",
	selected: "\x1b[7m", date: "\x1b[1;36m", unread: "\x1b[1;33m",
	outgoing: "\x1b[1;32m", attachment: "\x1b[35m", codeSGR: "36",
	rail: "\x1b[36m", railMuted: "\x1b[2m", badge: "\x1b[1;33m", reaction: "\x1b[33m",
}

var (
	lightPalette = rgbPalette(false)
	darkPalette  = rgbPalette(true)
)

func rgbPalette(dark bool) terminalPalette {
	theme := tui.ThemeColors(dark)
	sgr := func(params string) string { return "\x1b[" + params + "m" }

	return terminalPalette{
		heading:    sgr("1;" + theme.Heading.SGR(false)),
		muted:      sgr(theme.Muted.SGR(false)),
		selected:   sgr(theme.SelectionBackground.SGR(true) + ";" + theme.SelectionText.SGR(false)),
		date:       sgr(theme.Date.SGR(false)),
		unread:     sgr("1;" + theme.Unread.SGR(false)),
		outgoing:   sgr("1;" + theme.Accent.SGR(false)),
		attachment: sgr(theme.Heading.SGR(false)),
		codeSGR:    theme.Date.SGR(false),
		rail:       sgr(theme.Accent.SGR(false)),
		railMuted:  sgr(theme.Muted.SGR(false)),
		badge:      sgr("1;" + theme.Unread.SGR(false)),
		reaction:   sgr(theme.Unread.SGR(false)),
		draftRow:   sgr(theme.DraftBackground.SGR(true)),
	}
}

func (a *app) palette() *terminalPalette {
	if a.colors != nil {
		return a.colors
	}

	return &standardPalette
}

func choosePalette(dark bool, colorTerm string) *terminalPalette {
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		if dark {
			return &darkPalette
		}

		return &lightPalette
	}

	if dark {
		return &ansiDarkPalette
	}

	return &standardPalette
}

func (a *app) nicknameStyle(current chat, msg message) string {
	peer := msg.author
	if peer.id == 0 && current.key.kind == 'u' {
		peer = current.key
	}

	identity := senderColorKey(peer, msg.authorName)
	if identity == "" {
		return "\x1b[1m"
	}

	color := tui.ColorForNick(identity)

	if !a.console {
		switch a.palette() {
		case &darkPalette:
			return "\x1b[1;" + color.Dark.SGR(false) + "m"
		case &lightPalette:
			return "\x1b[1;" + color.Light.SGR(false) + "m"
		}
	}

	return fmt.Sprintf("\x1b[1;%dm", color.ANSI)
}

func (a *app) avatarStyle(peer peerKey, name string) string {
	color := tui.ColorForNick(senderColorKey(peer, name))

	if !a.console {
		switch a.palette() {
		case &darkPalette:
			return "\x1b[1;30;" + color.Dark.SGR(true) + "m"
		case &lightPalette:
			return "\x1b[1;97;" + color.Light.SGR(true) + "m"
		}
	}

	return fmt.Sprintf("\x1b[1;30;%dm", color.ANSI+70)
}

func senderColorKey(peer peerKey, name string) string {
	if peer.id != 0 {
		return string(peer.kind) + ":" + strconv.FormatInt(peer.id, 10)
	}

	if name != "" {
		return "byline:" + name
	}

	return ""
}
