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

func shortSender(title string, width int) string {
	names := strings.Fields(title)
	if len(names) == 0 {
		return "Peer"
	}

	name := names[0]
	if uniseg.StringWidth(name) > width {
		return wrap(name, width-1, 1)[0] + "…"
	}

	return name
}

const minTerminalCols, minTerminalRows = 24, 7

func paint(w io.Writer, a *app, cols, rows int) error {
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J")

	a.historyRoom, a.visibleMessages = 0, 0
	a.noticeStatusVisible = false

	if cols < minTerminalCols || rows < minTerminalRows {
		_, err := io.WriteString(w, b.String()+"\x1b[HResize your terminal")
		return err
	}

	label := func(x, y, width int, value string) {
		fmt.Fprintf(&b, "\x1b[%d;%dH%s", y, x, wrap(value, width, 1)[0])
	}
	if len(a.topicEntries) > 0 {
		label(2, 1, cols-2, a.currentChat().title+" · Topics")
		label(2, rows, cols-2, "jk choose  Enter open  [/] pages  Esc back")

		start := max(0, a.topicIndex-rows+4)
		for i, entry := range a.topicEntries[start:min(len(a.topicEntries), start+rows-3)] {
			if start+i == a.topicIndex {
				label(2, i+2, 1, "›")
			}

			label(4, i+2, cols-4, topicLabel(entry))
		}

		status := a.status
		if a.pending == "topics" {
			status = "Loading topics…"
		}

		label(2, rows-1, cols-2, status)

		_, err := io.WriteString(w, b.String())

		return err
	}

	if a.forwardIDs != nil {
		if a.forwardConfirm {
			label(2, 1, cols-2, fmt.Sprintf("Forward %d message(s)?", len(a.forwardIDs)))
			label(2, rows, cols-2, "y send  Esc change recipient")

			for i, line := range wrap("To: "+a.forwardTo.title, cols-2, rows-2) {
				label(2, i+2, cols-2, line)
			}
		} else {
			label(2, 1, cols-2, fmt.Sprintf("Forward %d message(s). Type a chat name", len(a.forwardIDs)))
			label(2, rows, cols-2, "↑↓ choose  Enter select  Esc cancel")
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
		label(2, 1, cols-2, "Search history / "+a.serverQuery)
		label(2, rows, cols-2, "jk choose  Enter jump  n/N pages  Esc close")

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

	keys := "Esc quit"

	if a.disconnected {
		keys = "r reconnect  Esc quit"
	}

	if a.ready {
		keys = "f unread  jk move  l open  Tab archive  [/] pages  / filter  u/U unread"
		if a.unreadOnly {
			keys = "f all  jk move  l open  Tab archive  [/] pages  / filter  u/U unread"
		}
	}

	if a.open {
		keys = "jk move  Ctrl+N/P chats  i write  r reply  z react  ? search"
		if a.thread != nil {
			keys = "jk move  h back  i write  r reply  z react  ? search"
		}

		if a.pending == "" && len(a.log) > 0 && a.log[a.offset].editable() {
			keys = "e edit  " + keys
		}
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
		} else if a.staged != nil && a.staged.format != "" && a.staged.format != "gif" {
			keys = "Enter send  Ctrl+B photo/file  Ctrl+J newline  Esc cancel"
		}
	}

	if a.pickingReaction {
		keys = "1 👍  2 ❤️  3 🔥  4 😂  5 🎉  6 👎  Esc cancel"

		if a.textReactions {
			first, second := "1 Like 2 Heart 3 Fire", "4 Laugh 5 Party 6 No"

			keys = first + "  " + second + "  Esc cancel"
			if len(keys) > cols-2 {
				label(2, rows-1, cols-2, first)

				keys = second
			}
		}
	}

	if a.pending == "send" || a.pending == "forward" || a.pending == "edit" {
		keys = "Waiting for Telegram…  Ctrl+C quit"
	}

	colors := a.palette()
	b.WriteString(colors.muted)
	label(2, rows, cols-2, keys)
	b.WriteString("\x1b[0m")

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
		if a.disconnected {
			label(3, 3, cols-4, "Disconnected from Telegram")
		} else if a.qr == "" {
			label(3, 3, cols-4, "Sign in to Telegram")
		} else {
			lines := strings.Split(strings.TrimSuffix(a.qr, "\n"), "\n")

			width := uniseg.StringWidth(lines[0])
			if width > cols-2 || len(lines) > rows-3 {
				for i, line := range wrap("Enlarge the terminal for the QR code, or run telega --phone.", cols-2, rows-5) {
					label(2, i+3, cols-2, line)
				}
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
			for y := 1; y < rows-2; y++ {
				label(sidebar, y, 1, "│")
			}
		}

		if wide || !a.open {
			title, shortcut := a.dialogTitle(), "Tab Archive"
			if a.dialogPage.folder == 1 {
				shortcut = "Tab Chats"
			}

			b.WriteString(colors.heading)
			label(2, 1, sidebar-3, title)
			b.WriteString("\x1b[0m")

			if uniseg.StringWidth(title)+len(shortcut)+1 <= sidebar-3 {
				label(sidebar-1-len(shortcut), 1, len(shortcut), shortcut)
			}

			indexes := a.visibleChats()

			start := max(0, slices.Index(indexes, a.selected)-rows+5)
			for row, i := range indexes[start:min(len(indexes), start+rows-4)] {
				y, marker := row+2, "╵"
				if i == a.selected {
					marker = "›"

					fmt.Fprintf(&b, "\x1b[%d;2H%s%s", y, colors.selected, strings.Repeat(" ", sidebar-2))
				}

				c := a.chats[i]
				badge := c.unread.badge()
				badgeWidth := uniseg.StringWidth(badge)
				width := sidebar - 11 // Space for the mark, padding and a badge up to 999+@.

				name := c.title
				if uniseg.StringWidth(name) > width {
					name = wrap(name, width-1, 1)[0] + "…"
				}

				label(2, y, 1, marker)
				label(4, y, width, name)

				if badge != "" {
					if i != a.selected {
						b.WriteString("\x1b[36m")
					}

					label(sidebar-1-badgeWidth, y, badgeWidth, badge)
				}

				if i == a.selected || badge != "" {
					b.WriteString("\x1b[0m")
				}
			}

			if len(indexes) == 0 && a.pending == "" {
				empty := "No matching chats"
				if a.unreadOnly && a.chatQuery == "" {
					empty = "No unread on this page"
				}

				label(2, 2, sidebar-3, empty)
			}
		}

		if a.open {
			left := 2
			if wide {
				left = sidebar + 2
			}

			a.paintConversation(&b, label, left, cols, statusRow)
		}
	}

	status := a.status
	if len(a.linkChoices) > 0 {
		b.WriteString("\x1b[2J")
		label(2, 1, cols-2, "Open link")
		label(2, rows, cols-2, keys)

		start := max(0, a.linkIndex-rows+4)
		for i, link := range a.linkChoices[start:min(len(a.linkChoices), start+rows-3)] {
			prefix := "  "
			if start+i == a.linkIndex {
				prefix = "› "
			}

			caption := link.label
			if caption == "" {
				caption = link.url
			}

			label(2, i+2, cols-2, prefix+caption)
		}

		label(2, rows-1, cols-2, a.linkChoices[a.linkIndex].url)

		_, err := io.WriteString(w, b.String())

		return err
	}

	if a.pickingReaction {
		status = "Choose a reaction · select it again to remove"

		if len(a.log) > 0 && a.log[a.offset].myReaction != "" {
			chosen := a.log[a.offset].myReaction
			if a.textReactions {
				chosen = reactionName(chosen)
			}

			status = "Your reaction: " + chosen + " · select it again to remove"
		}
	}

	if a.noticeStatus != "" && !a.pickingReaction && a.pending == "" && len(a.confirmDelete) == 0 {
		status = a.noticeStatus
		a.noticeStatusVisible = true
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

			status = "Photo: " + name
			if a.asFile || a.staged.format == "" || a.staged.format == "gif" {
				status = "File: " + name
			}
		}
	}

	if status == "" {
		switch a.pending {
		case "":
			if a.open && !a.composing && !a.imagePath && !a.searching {
				status = a.messageJumpHints()
			}
		case "connect", "reconnect":
			status = "Connecting to Telegram…"
		case "login":
			status = "Waiting for Telegram…"
		case "chats", "archiveChat", "topics", "discussion", "threadParent", "history", "older", "newer", "latest", "jump", "jumpBack", "unread":
			status = "Loading…"
		case "searchHistory":
			status = "Searching…"
		case "download":
			status = "Downloading attachment…"
		case "image", "file", "clipboard", "screenshot":
			status = "Preparing attachment…"
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
		if a.filePath {
			prompt = "File path"
		}
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
