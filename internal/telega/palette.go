package telega

import (
	"fmt"
	"strconv"

	"github.com/danilrwx/telega/tui"
)

type terminalPalette struct {
	heading, muted, selected, date, unread, outgoing, attachment, codeSGR string
	rail, railMuted, badge, reaction                                      string
}

// ANSI colors remain references to the terminal palette, including after a theme change.
var standardPalette = terminalPalette{
	heading: "\x1b[1;35m", muted: "\x1b[2m",
	selected: "\x1b[7m", date: "\x1b[36m", unread: "\x1b[1;33m",
	outgoing: "\x1b[1;32m", attachment: "\x1b[35m", codeSGR: "36",
	rail: "\x1b[36m", railMuted: "\x1b[2m", badge: "\x1b[33m", reaction: "\x1b[33m",
}

func (a *app) palette() *terminalPalette { return &standardPalette }

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

	return fmt.Sprintf("\x1b[1;%dm", color.ANSI)
}

func (a *app) avatarStyle(peer peerKey, name string) string {
	color := tui.ColorForNick(senderColorKey(peer, name))

	return fmt.Sprintf("\x1b[7;%dm", color.ANSI)
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
