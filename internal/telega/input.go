package telega

import (
	"slices"
	"strings"
	"unicode"

	"github.com/danilrwx/telega/tui"
)

func (a *app) clearDraft() {
	clear(a.draft)
	a.draft = a.draft[:0]
	a.draftCursor = 0
}

func (a *app) edit(k key) {
	cursor := len(a.draft)
	if a.composing {
		cursor = a.draftCursor

		switch k.name {
		case "left":
			a.draftCursor = max(0, cursor-1)
			return
		case "right":
			a.draftCursor = min(len(a.draft), cursor+1)
			return
		case "home", "end", "up", "down":
			a.moveDraftCursor(k.name)
			return
		case "ctrl+a":
			a.moveDraftCursor("home")
			return
		case "ctrl+e":
			a.moveDraftCursor("end")
			return
		case "ctrl+w":
			start := cursor
			for start > 0 && a.draft[start-1] != '\n' && unicode.IsSpace(a.draft[start-1]) {
				start--
			}

			for start > 0 && !unicode.IsSpace(a.draft[start-1]) {
				start--
			}

			a.draft = slices.Delete(a.draft, start, cursor)
			a.draftCursor = start

			return
		case "deleteRune":
			if cursor < len(a.draft) {
				a.draft = slices.Delete(a.draft, cursor, cursor+1)
			}

			return
		}
	}

	if k.name == "erase" {
		if cursor > 0 {
			a.draft = slices.Delete(a.draft, cursor-1, cursor)
			a.draftCursor = cursor - 1
		}

		return
	}

	limit := 256
	if a.ready {
		limit = inputLimit
	}

	var insert []rune
	for _, ch := range k.text {
		if len(a.draft)+len(insert) >= limit {
			break
		}

		if !unicode.IsControl(ch) || ch == '\n' && a.ready || ch == '\t' && a.composing {
			insert = append(insert, ch)
		}
	}

	a.draft = slices.Insert(a.draft, cursor, insert...)
	a.draftCursor = cursor + len(insert)
}

func (a *app) moveDraftCursor(direction string) {
	cursor := a.draftCursor
	start := lastLineStart(a.draft[:cursor])

	end := len(a.draft)
	if next := slices.Index(a.draft[cursor:], '\n'); next >= 0 {
		end = cursor + next
	}

	column := cursor - start

	switch direction {
	case "home":
		a.draftCursor = start
	case "end":
		a.draftCursor = end
	case "up":
		if start > 0 {
			previous := lastLineStart(a.draft[:start-1])
			a.draftCursor = min(previous+column, start-1)
		}
	case "down":
		if end < len(a.draft) {
			next := end + 1

			end = len(a.draft)
			if line := slices.Index(a.draft[next:], '\n'); line >= 0 {
				end = next + line
			}

			a.draftCursor = min(next+column, end)
		}
	}
}

func lastLineStart(text []rune) int {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == '\n' {
			return i + 1
		}
	}

	return 0
}

func (a *app) input(k key, out chan<- request) bool {
	defer a.keepUnreadSelection()

	if k.name == "newline" && !a.composing {
		k.name = "enter"
	}

	switch k.name {
	case "focus", "blur":
		a.focused = k.name == "focus"
		return true
	}

	if a.noticeStatusVisible {
		a.noticeStatus = ""
	}

	if k.name == "quit" {
		return false
	}

	if a.playback != nil {
		switch k.name {
		case "alt+p":
			a.toggleVoicePause()
			return true
		case "alt+shift+p":
			a.stopVoicePlayback()
			return true
		}
	}

	if a.pollDialog != nil {
		a.pollInput(commandKey(k), out)
		return true
	}

	if a.pins != nil {
		a.pinsInput(k, out)
		return true
	}

	if a.chatActions != nil {
		a.chatActionsInput(k, out)
		return true
	}

	if a.transcript != nil {
		a.voiceTranscriptInput(commandKey(k), out)
		return true
	}

	if a.voice != nil {
		switch k.name {
		case "enter", "alt+v":
			a.voice.stop()
		case "escape":
			a.voice.discard = true
			a.voice.cancel()
		}

		return true
	}

	if a.imagePreview != nil {
		k = commandKey(k)
		switch {
		case k.name == "escape" || k.name == "enter" || k.text == "q":
			a.imagePreview, a.imagePreviewPath = nil, ""
		case k.name == "" && k.text == "o" && a.imagePreviewPath != "":
			a.openExternal = a.imagePreviewPath
		}

		return true
	}

	if !a.ready {
		if k.name == "escape" {
			return false
		}

		if a.disconnected && a.pending == "" && commandKey(k).text == "r" && k.name == "" {
			a.ask(out, request{kind: "reconnect"})
			return true
		}

		if a.prompt != "" && a.pending == "" {
			if k.name == "enter" && len(a.draft) > 0 {
				a.ask(out, request{kind: "login", text: string(a.draft)})
				a.clearDraft()
			} else {
				a.edit(k)
			}
		}

		return true
	}

	if dialog := a.reactionAuthors; dialog != nil {
		name := k.name
		if name == "" {
			name = commandKey(k).text
		}

		if dialog.Key(name) {
			a.reactionAuthors = nil
		}

		return true
	}

	if len(a.topicEntries) > 0 {
		a.inputTopics(commandKey(k), out)
		return true
	}

	if a.pickingReaction {
		a.pickReaction(k, out)
		return true
	}

	if a.chatPicker {
		a.chatPickerInput(k, out)
		return true
	}

	if a.forwardIDs != nil {
		a.forwardInput(k, out)
		return true
	}

	if a.attachmentGallery != nil {
		if len(a.linkChoices) != 0 {
			a.linkInput(commandKey(k))
		} else {
			a.attachmentGalleryInput(commandKey(k), out)
		}

		return true
	}

	if a.messageSearch != nil {
		a.messageSearchInput(k, out)
		return true
	}

	if len(a.confirmDelete) > 0 {
		a.confirmDeletion(commandKey(k), out)
		return true
	}

	if len(a.linkChoices) > 0 {
		a.linkInput(commandKey(k))
		return true
	}

	if a.textAttachmentPrompt != nil {
		a.textAttachmentNameInput(k)
		return true
	}

	if a.filePicker != nil {
		a.filePickerInput(k)
		return true
	}

	if a.attachmentTitle != "" {
		a.attachmentChoiceInput(commandKey(k), out)
		return true
	}

	if a.settingsOpen {
		k = commandKey(k)
		switch {
		case k.name == "escape" || k.name == "alt+s" || k.text == "q":
			a.settingsOpen = false
		case k.name == "up" || k.text == "k":
			a.settingsCursor = max(0, a.settingsCursor-1)
		case k.name == "down" || k.text == "j":
			a.settingsCursor = min(displaySettingsCount-1, a.settingsCursor+1)
		case k.name == "enter" || k.text == " ":
			a.toggleDisplaySetting()
		}

		return true
	}

	if k.name == "alt+s" && !a.composing && !a.searching {
		a.settingsOpen = true
		a.status = ""

		return true
	}

	if k.name == "nextChat" || k.name == "previousChat" {
		step := 1
		if k.name == "previousChat" {
			step = -1
		}

		if a.open {
			a.switchChat(out, step)
		} else {
			a.moveChat(step)
		}

		return true
	}

	if k.name == "saveDraft" && a.open && a.composing {
		a.parkDraft()
		a.resetChat()

		return true
	}

	if a.open && a.pending == "" && !a.searching && a.editID == 0 && !a.demo {
		switch k.name {
		case "alt+r":
			if !a.composing && len(a.log) != 0 {
				a.beginVoiceTranscript(out, a.currentChat(), a.log[a.offset])
			}

			return true
		case "alt+a":
			a.beginAttachment(false)
			return true
		case "alt+t", "textAttachment":
			a.textAttachmentPrompt = &tui.TextAttachmentPrompt{}
			return true
		case "alt+v":
			if !a.composing {
				a.composing = true
				a.restoreDraft()
			}

			if a.editID == 0 {
				a.voiceRequested = true
			} else {
				a.status = "Finish editing before recording a voice message"
			}

			return true
		}
	}

	if k.name == "editor" && a.open && a.pending == "" && !a.searching {
		if !a.composing {
			a.restoreDraft()
		}

		a.composing, a.vimRequested, a.status = true, true, ""

		return true
	}

	if !a.composing && !a.searching {
		k = commandKey(k)
	}

	if a.open && !a.searching && (k.name == "pasteImage" || k.name == "paste" && tui.ClipboardPNGPaste(k.text) ||
		!a.composing && k.name == "" && k.text == "s") {
		if !a.demo && a.pending == "" && a.editID == 0 {
			if !a.composing {
				a.restoreDraft()
			}

			a.imageFromCompose, a.composing, a.prefixG = a.composing, false, false
			a.ask(out, request{kind: "clipboard", chat: a.currentChat()})
		}

		return true
	}

	if a.composing {
		switch k.name {
		case "cancelReply":
			if a.replyID != 0 {
				a.clearReply()

				if a.retrySend != nil {
					a.retrySend, a.status = nil, ""
				}
			}
		case "jumpBack":
			a.showDraftAttachments()
		case "escape":
			a.parkDraft()
		case "fileMode":
			if a.staged != nil && a.staged.format != "" && a.staged.format != "gif" {
				a.asFile = !a.asFile
				a.status = ""
			}
		case "newline":
			a.edit(key{text: "\n"})
		case "paste":
			k.text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(k.text)
			a.edit(k)
		case "enter":
			a.submitDraft(out)
		default:
			a.edit(k)
		}

		return true
	}

	if a.searching && a.searchInput(k, out) {
		return true
	}

	if k.name == "" && k.text == "x" && !a.searching {
		a.beginChatActions()
		return true
	}

	if k.name == "chatPicker" || !a.open && k.name == "" && k.text == "/" {
		a.beginChatPicker(out)
		return true
	}

	if k.name == "messageSearch" || k.name == "" && k.text == "?" {
		a.beginMessageSearch(k.name == "messageSearch" || !a.open)
		return true
	}

	if k.name == "" && k.text == "/" {
		a.searching, a.search, a.prefixG = true, nil, false
		return true
	}

	if k.name == "" {
		switch k.text {
		case "g":
			if !a.prefixG {
				a.prefixG = true
				return true
			}

			k.name = "home"
		case "G":
			k.name = "end"
		case "j":
			k.name = "down"
		case "k":
			k.name = "up"
		case "h":
			k.name = "left"
		case "l":
			k.name = "right"
		}
	}

	a.prefixG = false
	if a.open {
		switch k.name {
		case "escape", "left":
			if k.name == "escape" && len(a.selection) != 0 {
				a.selection = nil
				return true
			}

			if a.pending == "" {
				if a.thread != nil {
					a.leaveThread(out)
				} else {
					a.resetChat()
					a.clearFolderPickerChat()
				}
			}
		case "enter":
			if a.openPoll(out) {
				return true
			}

			if a.pending == "" {
				a.composing = true
				a.restoreDraft()
			}
		case "jumpBack":
			if a.pending == "" {
				a.showDraftAttachments()
			}
		case "up":
			a.scroll(out, 1)
		case "down":
			a.scroll(out, -1)
		case "pageup":
			a.scroll(out, 8)
		case "pagedown":
			a.scroll(out, -8)
		case "home":
			a.scroll(out, max(1, len(a.log)-1-a.offset))
		case "end":
			if a.newerTrimmed && a.pending == "" {
				a.ask(out, request{kind: "latest", chat: a.currentChat()})
			} else {
				a.offset = 0
			}
		case "":
			switch k.text {
			case "i":
				if a.pending == "" {
					a.composing = true
					a.restoreDraft()
				}
			case "e":
				a.beginEdit()
			case "t":
				if !a.demo && a.pending == "" && a.thread == nil && a.currentChat().forum {
					a.ask(out, request{kind: "topics", chat: a.currentChat()})
				}
			case "c":
				if !a.demo && a.pending == "" && a.thread == nil && len(a.log) > 0 && a.log[a.offset].comments {
					a.ask(out, request{kind: "discussion", chat: a.currentChat(), anchor: a.log[a.offset].id})
				}
			case "f":
				a.beginForward()
			case "F":
				if a.pending == "" && len(a.log) != 0 {
					a.jumpToForward(out)
				}
			case "y":
				a.copyMessage()
			case "Z":
				a.showReactionAuthors(out)
			case "z":
				if !a.demo && a.pending == "" && len(a.log) > 0 {
					a.pickingReaction, a.reactionTarget = true, a.log[a.offset].id
					a.reactionCatalog, a.reactionCatalogLoaded = nil, false
					a.reactionSuggestionIndex = 0
					a.status = ""
				}
			case "H":
				a.backToMessage(out)
			case "J":
				a.jumpToReply(out)
			case "u":
				a.jumpToUnread(out)
			case "r", "R":
				a.beginReply(k.text == "R")
			case "o", "O":
				a.openSelected(out, k.text == "O")
			case " ":
				a.toggleSelected()
			case "d", "D":
				a.askDelete(k.text == "D")
			case "a", "A":
				if !a.demo && a.pending == "" {
					a.beginAttachment(k.text == "A")
				}
			case "S":
				if !a.demo && a.pending == "" {
					a.ask(out, request{kind: "screenshot", chat: a.currentChat()})
				}
			case "m":
				if !a.demo && a.pending == "" && len(a.log) > 0 {
					a.ask(out, request{kind: "markRead", chat: a.currentChat(), anchor: a.log[a.offset].id})
				}
			case "n":
				a.findMessage(1)
			case "N":
				a.findMessage(-1)
			case "v":
				if len(a.log) != 0 {
					id := a.log[a.offset].id
					if a.revealID == id {
						id = 0
					}

					a.revealID = id
				}
			case "V":
				a.beginAttachmentGallery(out)
			}
		}

		return true
	}

	switch k.name {
	case "tab", "shift+tab":
		if !a.demo && a.pending == "" {
			a.chatQuery = ""

			step := 1
			if k.name == "shift+tab" {
				step = -1
			}

			a.cycleDialogFolder(out, step)
		}
	case "escape":
		if a.chatQuery != "" {
			a.chatQuery = ""
			return true
		}

		if a.unreadOnly {
			a.unreadOnly, a.status = false, ""
			return true
		}

		return false
	case "up":
		a.moveChat(-1)
	case "down":
		a.moveChat(1)
	case "pageup":
		a.moveChat(-8)
	case "pagedown":
		a.moveChat(8)
	case "home":
		a.moveChat(-chatLimit)
	case "end":
		a.moveChat(chatLimit)
	case "enter", "right":
		if slices.Contains(a.visibleChats(), a.selected) && a.pending == "" {
			a.openChat(out, a.selected)
		}
	case "":
		if k.text == "q" {
			return false
		}

		switch k.text {
		case "f":
			a.unreadOnly, a.status = !a.unreadOnly, ""
		case "r":
			if !a.demo && a.pending == "" {
				a.ask(out, request{kind: "chats", page: dialogPage{folder: a.dialogPage.folder, filter: a.dialogPage.filter}})
			}
		case "A":
			if !a.demo && a.pending == "" && slices.Contains(a.visibleChats(), a.selected) {
				a.ask(out, request{kind: "archiveChat", chat: a.currentChat(), page: dialogPage{folder: a.currentChat().folder}})
			}
		case "]":
			a.pageDialogs(out, 1)
		case "[":
			a.pageDialogs(out, -1)
		case "u":
			a.nextUnread(1)
		case "U":
			a.nextUnread(-1)
		}
	}

	return true
}

func (a *app) openSelected(out chan<- request, linksOnly bool) {
	if a.pending != "" || len(a.log) == 0 {
		return
	}

	msg := a.log[a.offset]
	if msg.attachment != nil && !linksOnly {
		if msg.attachment.location == nil {
			a.status = "Full attachment unavailable"
			return
		}

		a.ask(out, request{kind: "download", chat: a.currentChat(), anchor: msg.id, attachment: msg.attachment})

		return
	}

	a.chooseLink(msg)
}
