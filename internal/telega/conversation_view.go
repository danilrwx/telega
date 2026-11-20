package telega

import (
	"fmt"
	"strings"
)

func (a *app) paintConversation(b *strings.Builder, label func(int, int, int, string), left, cols, statusRow int) {
	current := a.currentChat()
	title := threadTitle(current)

	if len(a.selection) > 0 {
		title = fmt.Sprintf("[%d selected] %s", len(a.selection), title)
	}

	label(left, 2, cols-left, title)

	broadcast := current.key.kind == 'c' && !current.group
	body := left + 8
	bodyWidth := min(90, cols-body+1)
	bottom := statusRow

	var sawUnread bool

	for i, msg := range a.log[a.offset:] {
		author := a.authorLabel(msg)
		style := "\x1b[1m"

		switch {
		case current.key.kind == 'u' && msg.out:
			author, style = "→ You", "\x1b[1;36m"
		case current.key.kind == 'u':
			author = "← " + shortSender(current.title, bodyWidth)
		case broadcast && author == current.title:
			author = ""
		case !broadcast && author == "":
			author = current.title
		}

		minRows := 1
		if author != "" {
			minRows++
		}

		if bottom <= 3 || i > 0 && bottom-3 <= minRows {
			break
		}

		unreadBoundary := sawUnread && msg.id <= current.unread.inboxID
		if unreadBoundary {
			if bottom-3 > minRows {
				bottom--

				b.WriteString("\x1b[1m")
				label(body, bottom, bodyWidth, "──────── Unread ────────")
				b.WriteString("\x1b[0m")
			}

			sawUnread = false
		} else if i > 0 {
			bottom--
		}

		if current.unread.count > 0 && msg.id > current.unread.inboxID && !msg.out {
			sawUnread = true
		}

		limit := bottom - 3

		header := author != "" && limit > 1
		if header {
			limit--
		}

		lines := a.messageLines(msg, bodyWidth, limit)
		bottom -= len(lines)
		first := bottom

		if header {
			bottom--
		}

		a.visibleMessages++

		label(left, bottom, 5, msg.date.UTC().Format("15:04"))

		if header {
			b.WriteString(style)
			label(left+6, bottom, bodyWidth+2, author)
			b.WriteString("\x1b[0m")
		}

		if _, checked := a.selection[msg.id]; checked {
			if i == 0 {
				b.WriteString("\x1b[7m")
			}

			label(left-1, bottom, 1, "*")

			if i == 0 {
				b.WriteString("\x1b[0m")
			}
		} else if i == 0 {
			label(left-1, bottom, 1, "›")
		}

		railStyle := "\x1b[2m"
		if i == 0 {
			railStyle = "\x1b[36m"
		}

		for row, line := range lines {
			fmt.Fprintf(b, "\x1b[%d;%dH%s│\x1b[0m", first+row, body-2, railStyle)
			fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[0m", first+row, body, line)
		}
	}

	if bottom-3 >= (statusRow-2)/2 {
		a.historyRoom = bottom - 3
	}

	if len(a.log) == 0 && a.pending == "" {
		label(left, 3, cols-left, "No messages")
	}
}
