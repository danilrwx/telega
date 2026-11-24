package telega

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type directorySearch struct {
	id      uint64
	due     time.Time
	results []chat
	status  string
}

func (a *app) beginForward() {
	if a.demo || a.pending != "" || len(a.log) == 0 && len(a.selection) == 0 {
		return
	}

	if a.currentChat().protected {
		a.status = "This chat restricts forwarding"
		return
	}

	a.forwardIDs = a.selectedMessages()
	a.forwardIndex, a.forwardQuery, a.forwardConfirm = 0, "", false
}

func (a *app) closeForward() {
	a.forwardIDs, a.forwardConfirm = nil, false
	a.forwardTo = chat{}
	a.directorySearch = directorySearch{id: a.directorySearch.id + 1}
}

func (a *app) forwardChats() []chat {
	return a.matchChats(a.forwardQuery)
}

func (a *app) matchChats(query string) []chat {
	var matches []chat

	seen := make(map[peerKey]bool)

	add := func(c chat) {
		if !seen[c.key] {
			matches = append(matches, c)
			seen[c.key] = true
		}
	}
	for _, c := range a.chats {
		if tui.ChatMatches(c.title, c.username, query) {
			add(c)
		}
	}

	if strings.TrimSpace(query) != "" {
		for _, c := range a.chatDirectory {
			if tui.ChatMatches(c.title, c.username, query) {
				add(c)
			}
		}
	}

	userOnly := strings.HasPrefix(strings.TrimSpace(query), "@")
	for _, c := range a.directorySearch.results {
		if !userOnly || tui.ChatMatches("", c.username, query) {
			add(c)
		}
	}

	return matches
}

func (a *app) changeForwardQuery(query string) {
	if query == a.forwardQuery {
		return
	}

	a.forwardQuery, a.forwardIndex = query, 0
	a.scheduleDirectorySearch(query)
}

func (a *app) scheduleDirectorySearch(query string) {
	a.directorySearch = directorySearch{id: a.directorySearch.id + 1}

	term := strings.TrimPrefix(strings.TrimSpace(query), "@")
	if utf8.RuneCountInString(term) >= 3 {
		a.directorySearch.due = time.Now().Add(240 * time.Millisecond)
		a.directorySearch.status = "Searching Telegram…"
	}
}

func (a *app) flushDirectorySearch(out chan<- request) {
	query := a.forwardQuery
	if a.chatPicker {
		query = a.chatQuery
	}

	term := strings.TrimPrefix(strings.TrimSpace(query), "@")
	select {
	case out <- request{kind: "directorySearch", searchID: a.directorySearch.id, text: term}:
		a.directorySearch.due = time.Time{}
	default:
		a.directorySearch.due = time.Now().Add(40 * time.Millisecond)
	}
}

func searchTelegramContacts(ctx context.Context, api *tg.Client, term string) ([]chat, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: term, Limit: 30})
	if err != nil {
		return nil, err
	}

	peers := peerChats(found.Users, found.Chats)

	var matches []chat

	for _, group := range [][]tg.PeerClass{found.MyResults, found.Results} {
		for _, peer := range group {
			key := keyFor(peer)
			if c, ok := peers[key]; ok && c.title != "" {
				matches = append(matches, c)

				delete(peers, key)
			}
		}
	}

	return matches, nil
}

func (a *app) forwardInput(k key, out chan<- request) {
	if a.forwardConfirm {
		k = commandKey(k)
		switch {
		case k.name == "escape":
			a.forwardConfirm = false
		case k.name == "" && k.text == "y":
			a.ask(out, request{kind: "forward", chat: a.currentChat(), to: a.forwardTo, ids: a.forwardIDs})
			a.closeForward()
		}

		return
	}

	switch k.name {
	case "escape":
		a.closeForward()
	case "up", "previousChat":
		a.forwardIndex = max(0, a.forwardIndex-1)
	case "down", "nextChat":
		a.forwardIndex = min(a.forwardIndex+1, max(0, len(a.forwardChats())-1))
	case "enter":
		if chats := a.forwardChats(); len(chats) > 0 {
			a.forwardTo = chats[min(a.forwardIndex, len(chats)-1)]
			a.forwardConfirm = true
		}
	case "erase":
		if _, size := utf8.DecodeLastRuneInString(a.forwardQuery); size > 0 {
			a.changeForwardQuery(a.forwardQuery[:len(a.forwardQuery)-size])
		}
	case "", "paste":
		if a.forwardQuery == "" && k.name == "" && k.text == "/" {
			return
		}

		text := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}

			return r
		}, k.text)
		a.changeForwardQuery(limitText(a.forwardQuery+text, 128))
	}
}

func (a *app) renderForward(width, height int, row func(string, bool, bool, bool)) {
	title := "Forward message"
	if count := len(a.forwardIDs); count != 1 {
		title = fmt.Sprintf("Forward %d messages", count)
	}

	if a.forwardConfirm {
		row(tui.Clip(title+"?", width), true, false, false)
		row(tui.Clip("To: "+forwardChatLabel(a.forwardTo), width), false, false, false)
		row("", false, false, false)
		row(tui.Clip("y send  Esc back", width), false, false, true)

		return
	}

	var choices []string
	for _, c := range a.forwardChats() {
		choices = append(choices, forwardChatLabel(c))
	}

	tui.ChatSearchList(title, a.forwardQuery, choices, a.forwardIndex, width, height, a.directorySearch.status, row)
}

func forwardChatLabel(c chat) string {
	if c.username == "" {
		return c.title
	}

	return c.title + " · @" + c.username
}
