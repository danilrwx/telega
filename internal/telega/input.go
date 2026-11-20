package telega

import (
	"slices"
	"strings"
	"unicode"
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
	if k.name == "newline" && !a.composing {
		k.name = "enter"
	}

	switch k.name {
	case "focus", "blur":
		a.focused = k.name == "focus"
		return true
	}

	if k.name == "quit" {
		return false
	}

	if !a.ready {
		if k.name == "escape" {
			return false
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

	if a.forwardIDs != nil {
		a.forwardInput(k, out)
		return true
	}

	if a.serverResults != nil {
		a.serverSearchInput(commandKey(k), out)
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

	if k.name == "saveDraft" && a.open && (a.composing || a.imagePath) {
		a.parkDraft()
		a.resetChat()

		return true
	}

	if a.imagePath {
		a.pictureInput(k, out)
		return true
	}

	if !a.composing && !a.searching {
		k = commandKey(k)
	}

	if a.open && !a.searching && (k.name == "pasteImage" || !a.composing && k.name == "" && k.text == "s") {
		if !a.demo && a.pending == "" && a.staged == nil && a.editID == 0 {
			a.imageFromCompose, a.composing, a.prefixG = a.composing, false, false
			a.ask(out, request{kind: "clipboard", chat: a.chats[a.selected]})
		}

		return true
	}

	if a.composing {
		switch k.name {
		case "escape":
			a.clearComposer()
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

	if k.name == "" && (k.text == "/" || k.text == "?" && a.open && !a.demo && a.pending == "") {
		a.searching, a.search, a.prefixG, a.historySearch = true, nil, false, k.text == "?"
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
				a.resetChat()
			}
		case "enter":
			if a.pending == "" {
				a.composing = true
				a.restoreDraft()
			}
		case "jumpBack":
			a.backToMessage(out)
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
				a.ask(out, request{kind: "latest", chat: a.chats[a.selected]})
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
			case "f":
				a.beginForward()
			case "F":
				if a.pending == "" && len(a.log) != 0 {
					a.jumpToForward(out)
				}
			case "y":
				a.copyMessage()
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
			case "a":
				if !a.demo && a.pending == "" {
					a.imagePath = true
					a.clearDraft()
				}
			case "S":
				if !a.demo && a.pending == "" {
					a.ask(out, request{kind: "screenshot", chat: a.chats[a.selected]})
				}
			case "m":
				if !a.demo && a.pending == "" && len(a.log) > 0 {
					a.ask(out, request{kind: "markRead", chat: a.chats[a.selected], anchor: a.log[a.offset].id})
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
			}
		}

		return true
	}

	switch k.name {
	case "escape":
		if a.chatQuery != "" {
			a.chatQuery = ""
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
		case "r":
			if !a.demo && a.pending == "" {
				a.ask(out, request{kind: "chats"})
			}
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
		a.ask(out, request{kind: "download", chat: a.chats[a.selected], anchor: msg.id, attachment: msg.attachment})
		return
	}

	a.chooseLink(msg)
}
