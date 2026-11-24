package telega

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

func (a *app) beginChatPicker(out chan<- request) {
	if a.pending != "" {
		return
	}

	a.chatPicker, a.chatPickerIndex = true, 0

	a.chatQuery = ""
	if !a.demo && out != nil && !a.chatDirectoryStarted {
		out <- request{kind: "chatDirectory"}

		a.chatDirectoryStarted, a.chatDirectoryLoading = true, true
		a.chatDirectory = nil
	}
}

func (a *app) closeChatPicker() {
	a.chatPicker, a.chatQuery = false, ""
	a.directorySearch = directorySearch{id: a.directorySearch.id + 1}
}

func (a *app) chatPickerInput(k key, out chan<- request) {
	switch k.name {
	case "escape", "chatPicker":
		a.closeChatPicker()
	case "up", "previousChat":
		a.chatPickerIndex = max(0, a.chatPickerIndex-1)
	case "down", "nextChat":
		a.chatPickerIndex = min(a.chatPickerIndex+1, max(0, len(a.matchChats(a.chatQuery))-1))
	case "enter":
		matches := a.matchChats(a.chatQuery)
		if len(matches) == 0 {
			return
		}

		match := matches[min(a.chatPickerIndex, len(matches)-1)]

		index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == match.key })
		if index == a.selected && a.open {
			a.closeChatPicker()
			return
		}

		if a.open {
			a.parkDraft()
			a.resetChat()
		}

		a.closeChatPicker()

		if index < 0 {
			a.chats = append([]chat{match}, a.chats...)
			index = 0
		}

		a.openChat(out, index)
	case "erase":
		if _, size := utf8.DecodeLastRuneInString(a.chatQuery); size > 0 {
			a.setChatPickerQuery(a.chatQuery[:len(a.chatQuery)-size])
		}
	case "", "paste":
		text := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}

			return r
		}, k.text)
		a.setChatPickerQuery(limitText(a.chatQuery+text, 128))
	}
}

func (a *app) setChatPickerQuery(query string) {
	if query == a.chatQuery {
		return
	}

	a.chatQuery, a.chatPickerIndex = query, 0
	a.scheduleDirectorySearch(query)

	if a.demo {
		a.directorySearch = directorySearch{}
	}

	if !a.open {
		a.moveChat(0)
	}
}

func (a *app) renderChatPicker(width, height int, row func(string, bool, bool, bool)) {
	matches := a.matchChats(a.chatQuery)

	choices := make([]string, 0, len(matches))
	for _, c := range matches {
		choices = append(choices, forwardChatLabel(c))
	}

	status := a.directorySearch.status
	if status == "" && a.chatDirectoryLoading && len(choices) == 0 {
		status = "Loading older chats…"
	}

	tui.ChatSearchList("Find a chat", a.chatQuery, choices, a.chatPickerIndex, width, height, status, row)
}

func (a *app) receiveChatDirectory(event serverEvent) {
	if event.kind == "directorySearch" && (a.directorySearch.id != event.searchID || !a.chatPicker && a.forwardIDs == nil) {
		return
	}

	query, index := a.forwardQuery, &a.forwardIndex
	if a.chatPicker {
		query, index = a.chatQuery, &a.chatPickerIndex
	}

	var selected peerKey

	if a.chatPicker || a.forwardIDs != nil {
		matches := a.matchChats(query)
		if *index < len(matches) {
			selected = matches[*index].key
		}
	}

	if event.kind == "chatDirectory" {
		a.chatDirectory = append(a.chatDirectory, event.chats...)
	} else {
		a.directorySearch.results, a.directorySearch.status = event.chats, event.hint
	}

	if selected.id != 0 {
		if current := slices.IndexFunc(a.matchChats(query), func(c chat) bool { return c.key == selected }); current >= 0 {
			*index = current
		}
	}
}

func streamChatDirectory(ctx context.Context, api *tg.Client, events chan<- serverEvent) {
	for folder := range 2 {
		page := dialogPage{folder: folder}
		for {
			chats, next, err := loadDialogPage(ctx, api, page)
			if err != nil {
				_ = emit(ctx, events, serverEvent{kind: "chatDirectoryDone", hint: err.Error()})
				return
			}

			if len(chats) > 0 {
				if err := emit(ctx, events, serverEvent{kind: "chatDirectory", chats: chats}); err != nil {
					return
				}
			}

			if next.input == nil || next.id == page.cursor.id && next.date == page.cursor.date {
				break
			}

			page.cursor = next
		}
	}

	_ = emit(ctx, events, serverEvent{kind: "chatDirectoryDone"})
}
