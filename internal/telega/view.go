package telega

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

func limitText(text string, limit int) string {
	for index := range text {
		if limit == 0 {
			return text[:index]
		}

		limit--
	}

	return text
}

func wrap(text string, width, limit int) []string {
	lines := make([]string, 0, min(limit, 4))

	var line strings.Builder

	chars := uniseg.NewGraphemes(text)
	cells := 0

	for chars.Next() {
		word, span := chars.Str(), chars.Width()

		newline := word == "\n" || word == "\r\n"
		if !newline && strings.ContainsFunc(word, unicode.IsControl) {
			word, span = " ", 1
		}

		if newline || span+cells > width {
			lines = append(lines, line.String())
			if len(lines) == limit {
				return lines
			}

			line.Reset()

			cells = 0
		}

		if !newline && span <= width {
			line.WriteString(word)

			cells += span
		}
	}

	return append(lines, line.String())
}

const minTerminalCols, minTerminalRows = 24, 7

func paint(w io.Writer, a *app, cols, rows int) error {
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J")

	a.historyRoom = 0

	if cols < minTerminalCols || rows < minTerminalRows {
		_, err := io.WriteString(w, b.String()+"\x1b[HResize your terminal")
		return err
	}

	label := func(x, y, width int, value string) {
		fmt.Fprintf(&b, "\x1b[%d;%dH%s", y, x, wrap(value, width, 1)[0])
	}
	bar := func(y int, text string) {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[7m%s", y, strings.Repeat(" ", cols))
		label(2, y, cols-2, text)
		b.WriteString("\x1b[0m")
	}

	if a.forwardIDs != nil {
		if a.forwardConfirm {
			bar(1, fmt.Sprintf("Forward %d message(s)?", len(a.forwardIDs)))
			bar(rows, "y send  Esc change recipient")

			for i, line := range wrap("To: "+a.forwardTo.title, cols-2, rows-2) {
				label(2, i+2, cols-2, line)
			}
		} else {
			bar(1, fmt.Sprintf("Forward %d message(s). Type a chat name", len(a.forwardIDs)))
			bar(rows, "↑↓ choose  Enter select  Esc cancel")
			label(2, 2, cols-2, "/ "+a.forwardQuery)
			indexes := a.forwardChats()

			start := max(0, a.forwardIndex-rows+4)
			for row, i := range indexes[start:min(len(indexes), start+rows-3)] {
				if start+row == a.forwardIndex {
					label(2, row+3, 1, "›")
				}

				label(4, row+3, cols-4, a.chats[i].title)
			}

			if len(indexes) == 0 {
				label(2, 3, cols-2, "No matching chats")
			}
		}

		_, err := io.WriteString(w, b.String())

		return err
	}

	if a.serverResults != nil {
		bar(1, "Search history / "+a.serverQuery)
		bar(rows, "jk choose  Enter jump  n/N pages  Esc close")

		start := max(0, a.serverIndex-rows+4)
		for row, msg := range a.serverResults[start:min(len(a.serverResults), start+rows-3)] {
			if start+row == a.serverIndex {
				label(2, row+2, 1, "›")
			}

			label(4, row+2, cols-4, searchResultLabel(msg))
		}

		status := a.status
		if a.pending == "searchHistory" {
			status = "Searching…"
		} else if status == "" {
			status = fmt.Sprintf("%d/%d on this page", a.serverIndex+1, len(a.serverResults))
			if !a.serverEnd {
				status += " · n for more"
			}
		}

		label(2, rows-1, cols-2, status)

		_, err := io.WriteString(w, b.String())

		return err
	}

	title, keys := "telega", "Esc quit"
	if a.demo {
		title += "   /   Demo"
	}

	if a.ready {
		keys = "jk move   l open   / filter   u/U unread   Esc quit"
	}

	if a.open {
		keys = "jk move  Ctrl+N/P chats  i write  r reply  ? search  u unread"
	}

	if a.searching {
		keys = "Enter select   Esc cancel"
	}

	if a.imagePath {
		keys = "Enter attach  Esc cancel"
	}

	if len(a.linkChoices) > 0 {
		keys = "jk choose link  Enter open  Esc cancel"
	}

	if len(a.confirmDelete) > 0 {
		keys = "y confirm deletion  Esc cancel"
	}

	if a.composing {
		keys = "Enter send  Ctrl+J newline  Esc cancel"
		if a.editID != 0 {
			keys = "Enter save edit  Ctrl+J newline  Esc cancel"
		} else if a.staged == nil && !a.demo {
			keys = "Enter send  Ctrl+J newline  Ctrl+V image  Esc cancel"
		}
	}

	if a.pending == "send" || a.pending == "forward" || a.pending == "edit" {
		keys = "Waiting for Telegram…  Ctrl+C quit"
	}

	bar(1, title)
	bar(rows, keys)
	statusRow := rows - 2

	var draftLines []string

	draftPrompt, draftX := "", 0
	cursorRow, cursorX := 0, 0

	if a.composing {
		draftPrompt = "Message > "
		if a.editID != 0 {
			draftPrompt = "Edit > "
		} else if a.staged != nil {
			draftPrompt = "Caption > "
		}

		draftX = 2 + len(draftPrompt)
		draft := strings.ReplaceAll(string(a.draft), "\t", "    ")
		draftLines = wrap(draft, cols-draftX, len(draft)+1)
		before := strings.ReplaceAll(string(a.draft[:a.draftCursor]), "\t", "    ")
		cursorLines := wrap(before, cols-draftX, len(before)+1)
		cursorLine := len(cursorLines) - 1
		height := min(6, rows/3)
		start := max(0, min(cursorLine, len(draftLines)-height))
		draftLines = draftLines[start:min(len(draftLines), start+height)]
		statusRow = rows - 1 - len(draftLines)
		cursorRow = statusRow + 1 + cursorLine - start
		cursorX = draftX + uniseg.StringWidth(cursorLines[cursorLine])
	}

	if !a.ready {
		if a.qr == "" {
			label(3, 3, cols-4, "Sign in to Telegram")
		} else {
			lines := strings.Split(strings.TrimSuffix(a.qr, "\n"), "\n")

			width := uniseg.StringWidth(lines[0])
			if width > cols-2 || len(lines) > rows-3 {
				label(2, 4, cols-2, "Enlarge the terminal to display the QR code.")
			} else {
				label(2, 2, cols-2, "Telegram on your phone: Settings > Devices > Link Desktop Device")
				b.WriteString("\x1b[30;47m")

				for i, line := range lines {
					label((cols-width)/2+1, i+3, width, line)
				}

				b.WriteString("\x1b[0m")
			}
		}
	} else {
		wide := cols >= 64

		sidebar := cols - 1
		if wide {
			sidebar = min(cols/3, 30)
			for y := 2; y < rows-2; y++ {
				label(sidebar, y, 1, "│")
			}
		}

		if wide || !a.open {
			label(2, 2, sidebar-3, "Chats")

			indexes := a.visibleChats()

			start := max(0, slices.Index(indexes, a.selected)-rows+6)
			for row, i := range indexes[start:min(len(indexes), start+rows-5)] {
				if i == a.selected {
					label(2, row+3, 1, "›")
				}

				c := a.chats[i]
				badge := c.unread.badge()

				width := sidebar - 4
				if badge != "" {
					width -= uniseg.StringWidth(badge) + 1

					b.WriteString("\x1b[1m")
				}

				label(4, row+3, width, c.title)

				if badge != "" {
					label(5+width, row+3, sidebar-5-width, badge)
					b.WriteString("\x1b[0m")
				}
			}

			if len(indexes) == 0 && a.pending == "" {
				label(2, 3, sidebar-3, "No matching chats")
			}
		}

		if a.open {
			left := 2
			if wide {
				left = sidebar + 2
			}

			chatTitle := a.chats[a.selected].title
			if len(a.selection) > 0 {
				chatTitle = fmt.Sprintf("[%d selected] %s", len(a.selection), chatTitle)
			}

			label(left, 2, cols-left, chatTitle)

			bottom := statusRow
			current := a.chats[a.selected]
			unread := current.unread

			gutter := 9
			if current.key.kind == 'u' {
				gutter = min(22, max(12, (cols-left)/3))
			}

			body := left + gutter
			bodyWidth := cols - body + 1

			var sawUnread bool

			for i, msg := range a.log[a.offset:] {
				if bottom <= 3 {
					break
				}

				if sawUnread && msg.id <= unread.inboxID && bottom > 3 {
					bottom--

					b.WriteString("\x1b[1m")
					label(body, bottom, bodyWidth, "──────── Unread ────────")
					b.WriteString("\x1b[0m")

					sawUnread = false
				}

				if bottom <= 3 {
					break
				}

				if unread.count > 0 && msg.id > unread.inboxID && !msg.out {
					sawUnread = true
				}

				lines := a.messageLines(msg, bodyWidth, bottom-3)
				bottom -= len(lines)

				stamp := msg.date.UTC().Format("15:04")
				if current.key.kind == 'u' {
					label(left, bottom, 5, stamp)

					direction, person := "←", current.title
					if msg.out {
						direction, person = "→", "You"

						b.WriteString("\x1b[36m")
					}

					b.WriteString("\x1b[1m")
					label(left+6, bottom, gutter-7, direction+" "+person)
					b.WriteString("\x1b[0m")
				} else {
					mark := " <"
					if msg.out {
						mark = " >"
					}

					label(left, bottom, 8, stamp+mark)
				}

				if _, checked := a.selection[msg.id]; checked {
					if i == 0 {
						b.WriteString("\x1b[7m")
					}

					label(body-1, bottom, 1, "*")

					if i == 0 {
						b.WriteString("\x1b[0m")
					}
				} else if i == 0 {
					label(body-1, bottom, 1, "›")
				}

				for i, line := range lines {
					fmt.Fprintf(&b, "\x1b[%d;%dH%s\x1b[0m", bottom+i, body, line)
				}
			}

			if bottom-3 >= (statusRow-2)/2 {
				a.historyRoom = bottom - 3
			}

			if len(a.log) == 0 && a.pending == "" {
				label(left, 3, cols-left, "No messages")
			}
		}
	}

	status := a.status
	if len(a.linkChoices) > 0 {
		b.WriteString("\x1b[2J")
		bar(1, "Open link")
		bar(rows, keys)

		start := max(0, a.linkIndex-rows+3)
		for i, target := range a.linkChoices[start:min(len(a.linkChoices), start+rows-2)] {
			prefix := "  "
			if start+i == a.linkIndex {
				prefix = "› "
			}

			label(2, i+2, cols-2, prefix+target)
		}

		_, err := io.WriteString(w, b.String())

		return err
	}

	if len(a.confirmDelete) > 0 {
		scope := "for you"
		if a.deleteForEveryone {
			scope = "for everyone"
		}

		status = fmt.Sprintf("Delete %d message(s) %s? [y/Esc]", len(a.confirmDelete), scope)
	}

	if (a.composing || a.imagePath) && a.replyID != 0 {
		reply := "Replying to: " + a.replyPreview
		if status == "" {
			status = reply
		}
	}

	if a.composing && a.staged != nil {
		name := filepath.Base(a.staged.path)
		if a.staged.temporary {
			name = "Screenshot.png"
		}

		if a.status == "" {
			if status != "" {
				name += " / " + status
			}

			status = "Attached: " + name
		}
	}

	if status == "" {
		switch a.pending {
		case "":
			if a.open && !a.composing && !a.imagePath && !a.searching {
				status = a.messageJumpHints()
			}
		case "connect":
			status = "Connecting to Telegram…"
		case "login":
			status = "Waiting for Telegram…"
		case "chats", "history", "older", "newer", "latest", "jump", "jumpBack", "unread":
			status = "Loading…"
		case "searchHistory":
			status = "Searching…"
		case "download":
			status = "Downloading attachment…"
		case "image", "clipboard", "screenshot":
			status = "Preparing image…"
		case "deleteMessages":
			status = "Deleting messages…"
		case "markRead":
			status = "Marking messages as read…"
		}
	}

	if !a.ready && a.qr == "" && a.pending == "" {
		for i, line := range wrap(status, cols-4, rows-5) {
			label(2, i+4, cols-4, line)
		}
	} else {
		label(2, statusRow, cols-2, status)
	}

	prompt, masked := "", false
	if a.searching {
		prompt = "/"
		if a.historySearch {
			prompt = "Search history"
		}
	} else if a.imagePath {
		prompt = "Image path"
	} else if a.pending == "" {
		prompt, masked = a.prompt, a.masked
	}

	if a.composing {
		label(2, statusRow+1, cols-4, draftPrompt)

		for i, line := range draftLines {
			label(draftX, statusRow+1+i, cols-draftX, line)
		}

		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", cursorRow, cursorX)
	} else if prompt != "" {
		prefix := wrap(prompt+" > ", cols-4, 1)[0]
		label(2, rows-1, cols-4, prefix)
		x := 2 + uniseg.StringWidth(prefix)
		space := cols - x

		text := string(a.draft)
		if a.searching {
			text = string(a.search)
		}

		if masked {
			text = strings.Repeat("*", min(len(a.draft), space))
		} else {
			text = text[strings.LastIndexByte(text, '\n')+1:]
			chars := uniseg.NewGraphemes(text)
			width := uniseg.StringWidth(text)

			start := 0
			for width > space && chars.Next() {
				_, start = chars.Positions()
				width -= chars.Width()
			}

			text = text[start:]
		}

		label(x, rows-1, space, text)
		b.WriteString("\x1b[?25h")
	}

	_, err := io.WriteString(w, b.String())

	return err
}
