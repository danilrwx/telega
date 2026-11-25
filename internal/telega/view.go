package telega

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/danilrwx/telega/tui"
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

func shortSender(title string, width int) string {
	names := strings.Fields(title)
	if len(names) == 0 {
		return "Peer"
	}

	return tui.Clip(names[0], width)
}

const minTerminalCols, minTerminalRows = 24, 7

// screenPainter remembers the last frame that can be updated in place.
type screenPainter struct {
	base       string
	cols, rows int
}

func (p *screenPainter) paint(w io.Writer, a *app, cols, rows int) error {
	previous := *p
	*p = screenPainter{}

	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J")

	a.historyRoom, a.visibleMessages = 0, 0
	a.noticeStatusVisible = false

	if a.imagePreview != nil {
		if _, err := io.WriteString(w, "\x1b[?25l"); err != nil {
			return err
		}

		return a.imagePreview.WritePreview(w, a.imagePreviewTitle, cols, rows, a.imagePreviewPath != "")
	}

	if cols < minTerminalCols || rows < minTerminalRows {
		if a.attachmentGallery != nil {
			a.attachmentGallery.columns = 0
		}

		_, err := io.WriteString(w, b.String()+"\x1b[HResize your terminal")

		return err
	}

	label := func(x, y, width int, value string) {
		fmt.Fprintf(&b, "\x1b[%d;%dH%s", y, x, tui.Wrap(value, width, 1)[0])
	}

	if a.attachmentGallery != nil {
		a.paintAttachmentGallery(&b, label, cols, rows)
		paintDialogPopup(&b, a.dialogPopup(cols, rows))

		_, err := io.WriteString(w, b.String())

		return err
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
		keys = "jk move  Ctrl+N/P chats  i write  a attach  z react  Z authors  r reply  V gallery  ? messages"
		if a.thread != nil {
			keys = "jk move  h back  i write  a attach  z react  Z authors  r reply  V gallery  ? messages"
			if a.thread.threadKind == commentThread {
				keys = "i comment  r reply  h back  jk move  a attach  z react  Z authors  V gallery  ? messages"
			}
		} else if a.display.HideChatListWhenOpen {
			keys = "h chats  " + keys
		}

		if !a.demo && !a.composing {
			keys = "Alt+V voice  " + keys

			if len(a.log) > 0 && a.log[a.offset].attachment != nil && a.log[a.offset].attachment.kind == "Voice" {
				keys = "o play  Alt+R transcript  " + keys
			}
		}

		if a.pending == "" && len(a.log) > 0 && a.log[a.offset].editable() {
			keys = "e edit  " + keys
		}

		if !a.demo && a.thread == nil && len(a.log) > 0 && a.log[a.offset].comments {
			keys = "c comments  " + keys
		}
	}

	if a.ready && !a.open {
		keys = "x actions  " + keys + "  Alt+S settings"
	}

	if a.open && !a.demo && a.thread == nil {
		keys += "  x actions"
	}

	if a.searching {
		keys = "Enter select   Esc cancel"
	}

	if len(a.linkChoices) > 0 {
		keys = "jk choose link  Enter open  Esc cancel"
	}

	retryingSend := a.composing && a.retrySend != nil

	if a.composing {
		keys = "Enter send  Ctrl+X editor  Ctrl+J newline  Esc keep draft"
		if a.editID != 0 {
			keys = "Enter save edit  Ctrl+X editor  Ctrl+J newline  Esc keep draft"
		} else if a.staged == nil && !a.demo {
			keys = "Enter send  Ctrl+X editor  Ctrl+J newline  Ctrl+V image  Esc keep draft"
		} else if a.staged != nil && a.staged.format != "" && a.staged.format != "gif" {
			keys = "Enter send  Ctrl+X editor  Ctrl+B photo/file  Ctrl+J newline  Esc keep draft"
		}

		if retryingSend {
			keys = "Enter retry" + strings.TrimPrefix(keys, "Enter send")
		}

		if a.editID == 0 {
			keys = strings.Replace(keys, "Ctrl+X editor", "Alt+V voice  Alt+A attach  Alt+T text file  Ctrl+O draft files  Ctrl+X editor", 1)
		}

		if a.replyID != 0 {
			keys = "Ctrl+G cancel reply  " + keys
		}
	}

	reactionPrompt := ""

	reactionSuggestion := a.reactionSuggestionRow(cols - 2)
	if a.pickingReaction {
		reactionPrompt = "Choose a reaction · select it again to remove"

		if len(a.log) > 0 && a.log[a.offset].myReaction != "" {
			chosen := a.log[a.offset].myReaction
			if a.textReactions {
				chosen = reactionName(chosen)
			}

			reactionPrompt = "Your reaction: " + chosen + " · select it again to remove"
		}

		if a.status != "" && a.status != "Reaction updated" {
			reactionPrompt = a.status
		}

		if a.pending == "react" {
			reactionPrompt = "Saving reactions…"
		}

		if a.reactionPickerOpen {
			keys = "↑↓ Enter Tab Esc"
			if cols >= 55 && tui.ImageGraphics() {
				keys += "  Ctrl+O preview icon"
			}
		} else if cols >= 55 {
			reactionPrompt, keys = tui.ReactionFooter(reactionPrompt, cols-12, a.textReactions, telegramQuickReactions)
			keys += "  Tab more"
		} else {
			reactionPrompt, keys = tui.ReactionFooter(reactionPrompt, cols-2, a.textReactions, telegramQuickReactions)
		}
	}

	if a.pending == "send" || a.pending == "forward" || a.pending == "edit" {
		keys = "Waiting for Telegram…  Ctrl+C quit"
	}

	if a.voice != nil {
		keys = "Enter stop and keep  Esc discard"
	}

	colors := a.palette()
	popup := a.dialogPopup(cols, rows)

	b.WriteString(colors.muted)
	label(2, rows, cols-2, keys)
	b.WriteString("\x1b[0m")

	statusRow := rows - 2
	historyBottom := statusRow

	sidebarWidth := 0
	if a.open && !a.display.HideChatListWhenOpen {
		sidebarWidth = tui.SidebarWidth(cols, a.layoutSettings.SidebarWidth)
	}

	composerLeft := 1
	if sidebarWidth > 0 {
		composerLeft = sidebarWidth + 2
	}

	composerWidth := cols - composerLeft + 1
	draftX := composerLeft + tui.ComposerIndent
	draftWidth := tui.ComposerTextWidth(composerWidth)

	var draftLines []string

	composerLabel, draftTop := "Message", 0
	attachmentLine, attachmentRows := "", 0
	cursorRow, cursorX := 0, 0
	emptyDraft := false
	showComposer := a.ready && a.open && !a.searching

	if showComposer {
		saved := a.composerDraft()
		if !a.composing {
			if paused, ok := a.drafts[a.draftPosition()]; ok {
				saved = paused
			}
		}

		if saved.editID != 0 {
			composerLabel = "Edit"
		} else if saved.upload != nil {
			composerLabel = "Caption"
		} else if saved.replyID != 0 {
			composerLabel = "Reply"
		} else if a.thread != nil {
			composerLabel = "Reply"
			if a.thread.threadKind == commentThread {
				composerLabel = "Comment"
			}
		}

		if saved.upload != nil {
			attachmentLine = saved.attachmentLabel() + "  [Ctrl+O]"
			attachmentRows = 1
		}

		draft := strings.ReplaceAll(saved.text, "\t", "    ")

		height := max(1, min(tui.ComposerLineLimit(rows), rows-6)-attachmentRows)

		if a.composing {
			draftLines = tui.Wrap(draft, draftWidth, 0)
			before := strings.ReplaceAll(string(a.draft[:a.draftCursor]), "\t", "    ")
			cursorLines := tui.Wrap(before, draftWidth, 0)
			cursorLine := len(cursorLines) - 1
			start := max(0, min(cursorLine, len(draftLines)-height))
			draftLines = draftLines[start:min(len(draftLines), start+height)]
			cursorRow = cursorLine - start
			cursorX = draftX + uniseg.StringWidth(cursorLines[cursorLine])
		} else {
			if draft == "" {
				emptyDraft = true
				draft = "i write"
			}

			draftLines = tui.Wrap(draft, draftWidth, height)
		}

		statusRow = rows - 1
		if reactionSuggestion != "" {
			statusRow--
		}

		draftTop = statusRow - len(draftLines)
		historyBottom = draftTop - 1 - attachmentRows
		cursorRow += draftTop
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
				for i, line := range tui.Wrap("Enlarge the terminal for the QR code, or run telega --phone.", cols-2, rows-5) {
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
		wide := sidebarWidth > 0

		sidebar := cols - 1
		if wide {
			sidebar = sidebarWidth + 1

			b.WriteString(colors.muted)

			for y := 1; y <= rows-2; y++ {
				label(sidebar, y, 1, "│")
			}

			b.WriteString("\x1b[0m")
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
				badgeX := sidebar - 1 - badgeWidth

				width := sidebar - 4
				if badgeWidth > 0 {
					width = badgeX - 5 // Leave a blank column before the badge.
				}

				name := tui.Clip(c.title, width)

				label(2, y, 1, marker)
				label(4, y, width, name)

				if badge != "" {
					if i != a.selected {
						b.WriteString(colors.badge)
					}

					label(badgeX, y, badgeWidth, badge)
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
			a.paintConversation(&b, label, composerLeft+1, cols, historyBottom)
		}
	}

	status := a.status

	if reactionPrompt != "" {
		status = reactionPrompt
	}

	if a.noticeStatus != "" && !retryingSend && !a.pickingReaction && a.pending == "" && len(a.confirmDelete) == 0 {
		status = a.noticeStatus
		a.noticeStatusVisible = true
	}

	if a.composing && a.replyID != 0 {
		reply := "Replying to: " + a.replyPreview
		if status == "" {
			status = reply
		}
	}

	if a.voice != nil {
		status = "Recording voice…"
		if a.voice.discard || a.voice.stopping {
			status = "Finishing voice recording…"
		}
	}

	if status == "" {
		if a.playback != nil {
			status = a.playback.hint()
		}

		switch a.pending {
		case "":
			if status == "" && a.open && !a.composing && !a.searching {
				status = a.messageJumpHints()
			}
		case "connect", "reconnect":
			status = "Connecting to Telegram…"
		case "login":
			status = "Waiting for Telegram…"
		case "chats", "archiveChat", "topics", "discussion", "threadParent", "history", "older", "newer", "latest", "jump", "jumpBack", "unread":
			status = "Loading…"
		case "send":
			status = "Sending…"
		case "download":
			status = "Opening attachment…"
		case "clipboard", "screenshot":
			status = "Preparing attachment…"
		case "deleteMessages":
			status = "Deleting messages…"
		case "markRead":
			status = "Marking messages as read…"
		}
	}

	if !a.ready && a.qr == "" && a.pending == "" {
		for i, line := range tui.Wrap(status, cols-4, rows-5) {
			label(2, i+4, cols-4, line)
		}
	} else {
		label(2, statusRow, cols-2, status)

		if reactionSuggestion != "" {
			label(2, rows-1, cols-2, reactionSuggestion)
		}
	}

	prompt, masked := "", false
	if a.searching {
		prompt = "/"
	} else if a.pending == "" {
		prompt, masked = a.prompt, a.masked
	}

	if showComposer {
		b.WriteString(colors.muted)
		label(composerLeft, historyBottom, composerWidth, tui.ComposerRule(composerLabel, composerWidth))
		b.WriteString("\x1b[0m")

		if attachmentLine != "" {
			b.WriteString(colors.attachment)
			label(draftX, draftTop-1, draftWidth, attachmentLine)
			b.WriteString("\x1b[0m")
		}

		base := b.Len()

		for i, line := range draftLines {
			y := draftTop + i

			if a.composing {
				// Overwrite the whole draft row without erasing the sidebar or
				// leaving old characters or the old cursor row's background.
				style := "\x1b[0m"
				if y == cursorRow && colors.draftRow != "" {
					style = colors.draftRow
				}

				fmt.Fprintf(&b, "\x1b[%d;%dH%s%s", y, composerLeft, style, strings.Repeat(" ", tui.ComposerIndent))
			} else if emptyDraft {
				b.WriteString(colors.muted)
			}

			label(draftX, y, draftWidth, line)

			if a.composing {
				b.WriteString(strings.Repeat(" ", max(0, draftWidth-uniseg.StringWidth(line))))
			}

			b.WriteString("\x1b[0m")
		}

		if a.composing {
			fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", cursorRow, cursorX)

			if len(popup.Lines) == 0 {
				frame := b.String()

				next := screenPainter{base: frame[:base], cols: cols, rows: rows}
				if next == previous {
					frame = frame[base:]
				}

				_, err := io.WriteString(w, frame)
				if err == nil {
					*p = next
				}

				return err
			}
		}
	} else if prompt != "" {
		prefix := tui.Wrap(prompt+" > ", cols-4, 1)[0]
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

	paintDialogPopup(&b, popup)

	_, err := io.WriteString(w, b.String())

	return err
}

func paintDialogPopup(b *strings.Builder, popup tui.DialogPopup) {
	if len(popup.Lines) != 0 {
		frame := tui.WithoutInlineImages(b.String())
		b.Reset()
		b.WriteString(frame)
		b.WriteString("\x1b[?25l")

		for i, line := range popup.Lines {
			fmt.Fprintf(b, "\x1b[%d;%dH%s", popup.Y+i+1, popup.X+1, line)
		}
	}
}

func (a *app) dialogPopup(cols, rows int) tui.DialogPopup {
	if a.attachmentGallery != nil && len(a.linkChoices) == 0 && a.transcript == nil {
		return tui.DialogPopup{}
	}

	if a.transcript == nil && !a.settingsOpen && a.filePicker == nil && a.textAttachmentPrompt == nil && a.attachmentTitle == "" && a.reactionAuthors == nil && len(a.confirmDelete) == 0 && a.forwardIDs == nil && a.messageSearch == nil && !a.chatPicker && !a.reactionPickerOpen && len(a.linkChoices) == 0 && a.chatActions == nil && a.pins == nil {
		return tui.DialogPopup{}
	}

	colors := a.palette()
	dialogTitle, dialogHint, dialogStatus := "", "", ""
	dialogIndex := 0

	var choices []string

	if a.settingsOpen {
		dialogTitle, dialogHint, dialogStatus = "Settings", "jk choose  Enter change  Esc back", a.status
		choices, dialogIndex = a.display.rows(), a.settingsCursor
	} else if dialog := a.reactionAuthors; dialog != nil {
		dialogTitle, dialogHint = tui.ReactionAuthorsTitle, tui.ReactionAuthorsHint
		choices, dialogIndex, dialogStatus = dialog.Rows, dialog.Selected, dialog.Status
	} else if a.attachmentTitle != "" {
		dialogTitle, dialogHint, dialogIndex, dialogStatus = a.attachmentTitle, tui.OpenAttachmentHint, a.attachmentIndex, a.status
		if dialogTitle == tui.DraftAttachmentsTitle {
			dialogHint = tui.DraftAttachmentListHint(len(a.attachmentChoices))
		} else if a.pending == "download" {
			dialogStatus = "Opening…"
		}

		if dialogStatus == "" && a.playback != nil {
			dialogStatus = a.playback.hint()
		}

		choices = make([]string, len(a.attachmentChoices))
		for i, item := range a.attachmentChoices {
			choices[i] = item.label
		}
	} else if len(a.linkChoices) > 0 {
		dialogTitle, dialogHint, dialogIndex = "Open link", tui.OpenAttachmentHint, a.linkIndex

		dialogStatus = a.linkChoices[a.linkIndex].url
		for _, link := range a.linkChoices {
			caption := link.label
			if caption == "" {
				caption = link.url
			}

			choices = append(choices, caption)
		}
	}

	content := func(width, height int, row func(string, bool, bool, bool)) {
		switch {
		case a.pins != nil:
			a.drawPins(width, height, row)
		case a.chatActions != nil:
			a.chatActions.render(width, height, row)
		case a.transcript != nil:
			a.transcript.render(width, height, row)
		case a.messageSearch != nil && a.attachmentGallery == nil:
			a.messageSearch.Render(width, height, row)
		case a.chatPicker:
			a.renderChatPicker(width, height, row)
		case a.reactionPickerOpen:
			a.renderEmojiCatalog(width, height, row)
		case a.forwardIDs != nil:
			a.renderForward(width, height, row)
		case len(a.confirmDelete) > 0:
			tui.DeleteConfirmation(len(a.confirmDelete), a.deleteForEveryone).Render(width, height, row)
		case a.textAttachmentPrompt != nil:
			a.textAttachmentPrompt.Render(width, row)
		case a.filePicker != nil:
			a.filePicker.Render(width, height, row)
		default:
			tui.ListDialog(dialogTitle, choices, dialogIndex, width, height, dialogStatus, dialogHint, row)
		}
	}
	style := func(text string, role tui.PopupRole) string {
		var prefix string

		switch role {
		case tui.PopupTitle:
			prefix = colors.heading
		case tui.PopupBorder, tui.PopupMuted:
			prefix = colors.muted
		case tui.PopupSelected:
			return colors.selected + text + "\x1b[0m"
		}

		return "\x1b[0m" + prefix + text + "\x1b[0m"
	}

	maxWidth := tui.ListPopupWidth
	if a.settingsOpen || a.textAttachmentPrompt != nil || len(a.confirmDelete) > 0 || a.forwardConfirm {
		maxWidth = tui.FormPopupWidth
	}

	return tui.FloatingDialog(cols, rows, maxWidth, content, style)
}
