package telega

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
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

	direct := current.key.kind == 'u'
	firstMessageRow := 2
	titleColumn := left + 1

	if a.showDirectAvatar(current) {
		rows, offset := a.renderAvatar(a.directAvatarFile(current), current.key, current.title)
		fmt.Fprintf(b, "\x1b[1;%dH%s\x1b[2;%dH%s", titleColumn, rows[0], titleColumn, rows[1])
		titleColumn += offset
		firstMessageRow++
	}

	width := cols - titleColumn + 1

	b.WriteString(colors.heading)
	label(titleColumn, 1, width, title)
	b.WriteString("\x1b[0m")

	if presence := presenceText(current.presence, time.Now()); presence != "" {
		firstMessageRow = 3

		b.WriteString(colors.muted)
		label(titleColumn, 2, width, presence)
		b.WriteString("\x1b[0m")
	}

	broadcast := current.key.kind == 'c' && !current.group
	body := left + 8
	bodyWidth := cols - body + 1
	bottom := statusRow
	messageAvatars := a.showMessageAvatars(current) && bodyWidth > 12

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
		senderName := a.authorLabel(msg)
		author := senderName

		style := "\x1b[1m"
		if !msg.out && !msg.service {
			style = a.nicknameStyle(current, msg)
		}

		switch {
		case direct && msg.out:
			author, style = "→", colors.outgoing
			if !a.display.HideDMNames {
				author += " You"
			}
		case direct:
			author = "←"
			if !a.display.HideDMNames {
				author += " " + shortSender(current.title, bodyWidth)
			}
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

		avatarPeer := a.avatarAuthor(msg)
		if direct && !msg.out && avatarPeer.id == 0 {
			avatarPeer = current.key
		}

		showAvatar := messageAvatars && !msg.service && (avatarPeer.id != 0 || senderName != "" || msg.out || msg.avatar != nil)
		hasHeader := author != "" || len(metadata) > 0 || showAvatar

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

		right := direct && a.display.DMRight && msg.out && !msg.service
		messageWidth := bodyWidth

		if right {
			messageWidth = min(bodyWidth, max(18, bodyWidth*2/3))
		}

		lines := a.messageLines(msg, messageWidth, limit)
		bottom -= len(lines)
		first := bottom

		if header {
			bottom--
		}

		a.visibleMessages++

		mark := strings.Join(metadata, " · ")
		if author != "" && mark != "" {
			mark = " · " + mark
		}

		markWidth := uniseg.StringWidth(mark)

		var avatarRows [2]string

		avatarOffset := 0

		if header && len(lines) > 0 && showAvatar && messageWidth+2-avatarGutter > markWidth {
			avatarName := senderName
			if avatarName == "" {
				avatarName = current.title
			}

			if msg.out {
				avatarName = a.accountName
				if avatarName == "" {
					avatarName = "You"
				}
			}

			avatarRows, avatarOffset = a.renderAvatar(a.avatarFile(msg, current), avatarPeer, avatarName)
		}

		headerWidth := messageWidth + 2 - avatarOffset
		author = tui.Wrap(author, max(1, headerWidth-markWidth), 1)[0]
		authorWidth := uniseg.StringWidth(author)

		blockLeft := left

		if right {
			extent := 5 + avatarOffset
			if header {
				extent = max(extent, 6+avatarOffset+min(headerWidth, authorWidth+uniseg.StringWidth(mark)))
			}

			for _, line := range lines {
				extent = max(extent, body-left+ansi.StringWidth(line))
			}

			blockLeft = max(left, cols-extent)
		}

		if avatarOffset > 0 {
			fmt.Fprintf(b, "\x1b[%d;%dH%s", bottom, blockLeft, avatarRows[0])
			fmt.Fprintf(b, "\x1b[%d;%dH%s", first, blockLeft, avatarRows[1])
		}

		timeColumn := blockLeft + avatarOffset

		b.WriteString(colors.muted)
		label(timeColumn, bottom, 5, msg.date.UTC().Format("15:04"))
		b.WriteString("\x1b[0m")

		if header {
			x := timeColumn + 6

			if author != "" {
				b.WriteString(style)
				label(x, bottom, headerWidth, author)
				b.WriteString("\x1b[0m")
			}

			if mark != "" {
				b.WriteString(colors.muted)
				label(x+authorWidth, bottom, headerWidth-authorWidth, mark)
				b.WriteString("\x1b[0m")
			}
		}

		if _, checked := a.selection[msg.id]; checked {
			if i == 0 {
				b.WriteString("\x1b[7m")
			}

			label(blockLeft-1, bottom, 1, "*")

			if i == 0 {
				b.WriteString("\x1b[0m")
			}
		} else if i == 0 {
			label(blockLeft-1, bottom, 1, "›")
		}

		railStyle := colors.railMuted
		if i == 0 {
			railStyle = colors.rail
		}

		blockBody := body + blockLeft - left
		for row, line := range lines {
			fmt.Fprintf(b, "\x1b[%d;%dH%s│\x1b[0m", first+row, blockBody-2, railStyle)
			fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[0m", first+row, blockBody, line)
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

func placeholderInitials(name string) string {
	var initials []rune

	for _, part := range strings.Fields(name) {
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				initials = append(initials, unicode.ToUpper(r))
				break
			}
		}

		if len(initials) == 2 {
			break
		}
	}

	if len(initials) == 0 {
		initials = []rune{'·'}
	}

	text := tui.Clip(string(initials), 4)
	width := uniseg.StringWidth(text)
	left := (4 - width) / 2

	return strings.Repeat(" ", left) + text + strings.Repeat(" ", 4-width-left)
}
