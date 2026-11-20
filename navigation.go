package main

import (
	"slices"
	"strings"
	"unicode"
)

const historyWindow = 2 * historyLimit

func (a *app) visibleChats() []int {
	needle := strings.ToLower(a.chatQuery)

	var indexes []int

	for i, chat := range a.chats {
		if strings.Contains(strings.ToLower(chat.title), needle) {
			indexes = append(indexes, i)
		}
	}

	return indexes
}

func (a *app) moveChat(step int) {
	indexes := a.visibleChats()
	if len(indexes) == 0 {
		return
	}

	i := slices.Index(indexes, a.selected)
	if i < 0 {
		a.selected = indexes[0]
		return
	}

	a.selected = indexes[min(max(i+step, 0), len(indexes)-1)]
}

func (a *app) searchInput(k key, out chan<- request) bool {
	switch k.name {
	case "escape":
		a.searching, a.historySearch = false, false
		if !a.open {
			a.chatQuery = ""
		}

		return true
	case "enter":
		a.searching = false
		if a.historySearch {
			a.historySearch = false

			query := strings.TrimSpace(string(a.search))
			if query != "" && a.pending == "" {
				a.serverQuery, a.serverPrevious, a.serverAnchor = query, nil, 0
				a.ask(out, request{kind: "searchHistory", chat: a.chats[a.selected], text: query})
			}

			return true
		}

		if a.open {
			a.messageQuery = string(a.search)
			a.findMessage(1)

			return true
		}

		return false
	case "up", "down":
		if !a.open {
			step := 1
			if k.name == "up" {
				step = -1
			}

			a.moveChat(step)
		}

		return true
	case "erase":
		if len(a.search) > 0 {
			a.search = a.search[:len(a.search)-1]
		}
	default:
		for _, r := range k.text {
			if len(a.search) >= 128 {
				break
			}

			if !unicode.IsControl(r) {
				a.search = append(a.search, r)
			}
		}
	}

	if !a.open {
		a.chatQuery = string(a.search)
		a.moveChat(0)
	}

	return true
}

func (a *app) findMessage(step int) {
	if a.messageQuery == "" || len(a.log) == 0 {
		return
	}

	needle := strings.ToLower(a.messageQuery)
	for n := range a.log {
		i := (a.offset + (n+1)*step + len(a.log)) % len(a.log)

		msg := a.log[i]
		if strings.Contains(strings.ToLower(msg.text), needle) ||
			msg.attachment != nil && strings.Contains(strings.ToLower(msg.attachment.name), needle) {
			a.offset, a.status = i, ""
			return
		}
	}

	a.status = "No match in loaded messages"
}

func (a *app) scroll(out chan<- request, step int) {
	if len(a.log) == 0 {
		return
	}

	next := a.offset + step
	switch {
	case next >= len(a.log) && !a.olderEnd && a.pending == "" && !a.prefetching && !a.demo:
		a.ask(out, request{kind: "older", chat: a.chats[a.selected], anchor: a.log[len(a.log)-1].id, step: next - len(a.log) + 1})
		a.offset = len(a.log) - 1
	case next < 0 && a.newerTrimmed && a.pending == "":
		a.ask(out, request{kind: "newer", chat: a.chats[a.selected], anchor: a.log[0].id, step: next})
		a.offset = 0
	default:
		a.offset = min(max(0, next), len(a.log)-1)
	}
}

func (a *app) mergePage(e serverEvent) {
	if e.kind == "latest" {
		a.log, a.offset = nil, 0
		a.olderEnd, a.newerTrimmed = false, false
	}

	if e.kind == "history" || e.kind == "latest" {
		for _, msg := range e.log {
			a.insert(msg, false)
		}

		a.olderEnd = len(e.log) == 0

		return
	}

	if len(e.log) == 0 {
		if e.kind == "older" {
			a.olderEnd = true
			a.status = "Beginning of chat"
		} else {
			a.newerTrimmed = false
		}

		return
	}
	// IDs, rather than date offsets, keep neighboring pages contiguous as new messages arrive.
	if e.kind == "older" {
		if a.offset == len(a.log)-1 {
			a.offset += e.step
		}

		a.log = append(a.log, e.log...)
		if overflow := len(a.log) - historyWindow; overflow > 0 {
			a.log = slices.Delete(a.log, 0, overflow)
			a.offset -= overflow
			a.newerTrimmed = true
		}
	} else {
		if a.offset == 0 {
			a.offset = len(e.log) + e.step
		} else {
			a.offset += len(e.log)
		}

		a.log = append(e.log, a.log...)
		if len(a.log) > historyWindow {
			a.log = slices.Delete(a.log, historyWindow, len(a.log))
			a.olderEnd = false
		}
	}

	a.offset = min(max(a.offset, 0), len(a.log)-1)
}
