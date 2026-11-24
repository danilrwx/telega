package telega

import (
	"fmt"
	"strings"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
	"github.com/rivo/uniseg"
)

func (a *app) paintConversation(b *strings.Builder, label func(int, int, int, string), left, cols, statusRow int) {
	current := a.currentChat()
	colors := a.palette()
	title := threadTitle(current)

	if len(a.selection) > 0 {
		title = fmt.Sprintf("[%d selected] %s", len(a.selection), title)
	}

	b.WriteString(colors.heading)
	label(left+1, 1, cols-left-1, title)
	b.WriteString("\x1b[0m")

	broadcast := current.key.kind == 'c' && !current.group
	body := left + 8
	bodyWidth := cols - body + 1
	bottom := statusRow

	const firstMessageRow = 2

	var (
		sawUnread bool
		newerDay  time.Time
	)

	showSeparator := func(text, style string) {
		text = "── " + text + " ──"
		width := uniseg.StringWidth(text)

		b.WriteString(style)
		label(left+(cols-left+1-width)/2, bottom, width, text)
		b.WriteString("\x1b[0m")
	}

	for i, msg := range a.log[a.offset:] {
		author := a.authorLabel(msg)

		style := "\x1b[1m"
		if !msg.out && !msg.service {
			style = a.nicknameStyle(current, msg)
		}

		switch {
		case current.key.kind == 'u' && msg.out:
			author, style = "→ You", colors.outgoing
		case current.key.kind == 'u':
			author = "← " + shortSender(current.title, bodyWidth)
		case broadcast && author == current.title:
			author = ""
		case !broadcast && author == "":
			author = current.title
		}

		if colors != &standardPalette && msg.out {
			style = colors.outgoing
		}

		var metadata []string

		if msg.out && msg.id > 0 && !msg.service {
			tick := "✓"
			if a.console {
				tick = "+"
			}

			_, saved := current.input.(*tg.InputPeerSelf)
			if !broadcast && !saved && msg.id <= current.readOutboxID {
				tick += tick
			}

			metadata = append(metadata, tick)
		}

		if msg.edited {
			metadata = append(metadata, "edited")
		}

		hasHeader := author != "" || len(metadata) > 0

		minRows := 1
		if hasHeader {
			minRows++
		}

		if bottom <= firstMessageRow || i > 0 && bottom-firstMessageRow <= minRows {
			break
		}

		day := msg.date.UTC().Truncate(24 * time.Hour)
		dateBoundary := !day.IsZero() && !newerDay.IsZero() && !day.Equal(newerDay)

		dateRows := 0
		if dateBoundary {
			dateRows = 1
		}

		unreadBoundary := sawUnread && msg.id <= current.unread.inboxID

		separatorRows := dateRows
		if unreadBoundary {
			separatorRows++
		}

		padDate := dateBoundary && bottom-firstMessageRow-minRows >= separatorRows+2
		if padDate {
			bottom--
		}

		if unreadBoundary {
			if bottom-firstMessageRow > minRows+dateRows {
				bottom--

				showSeparator("Unread", colors.unread)
			}

			sawUnread = false
		}

		if dateBoundary {
			if bottom-firstMessageRow > minRows {
				bottom--

				showSeparator(newerDay.Format(time.DateOnly), colors.date)
			}

			if padDate {
				bottom--
			}
		} else if i > 0 && !unreadBoundary {
			bottom--
		}

		if current.unread.count > 0 && msg.id > current.unread.inboxID && !msg.out {
			sawUnread = true
		}

		limit := bottom - firstMessageRow

		header := hasHeader && limit > 1
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

		b.WriteString(colors.muted)
		label(left, bottom, 5, msg.date.UTC().Format("15:04"))
		b.WriteString("\x1b[0m")

		if header {
			width := bodyWidth + 2
			mark := strings.Join(metadata, " · ")

			if author != "" {
				if mark != "" {
					mark = " · " + mark
				}

				author = tui.Wrap(author, max(1, width-uniseg.StringWidth(mark)), 1)[0]

				b.WriteString(style)
				label(left+6, bottom, width, author)
				b.WriteString("\x1b[0m")
			}

			if mark != "" {
				authorWidth := uniseg.StringWidth(author)

				b.WriteString(colors.muted)
				label(left+6+authorWidth, bottom, width-authorWidth, mark)
				b.WriteString("\x1b[0m")
			}
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

		railStyle := colors.railMuted
		if i == 0 {
			railStyle = colors.rail
		}

		for row, line := range lines {
			fmt.Fprintf(b, "\x1b[%d;%dH%s│\x1b[0m", first+row, body-2, railStyle)
			fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[0m", first+row, body, line)
		}

		newerDay = day
	}

	if !newerDay.IsZero() && bottom > firstMessageRow {
		bottom--

		showSeparator(newerDay.Format(time.DateOnly), colors.date)
	}

	if room := bottom - firstMessageRow; room >= (statusRow-firstMessageRow)/2 {
		a.historyRoom = room
	}

	if len(a.log) == 0 && a.pending == "" {
		label(left, firstMessageRow, cols-left, "No messages")
	}
}
