package telega

import (
	"fmt"
	"slices"
)

type chatPosition struct {
	chat peerKey
	id   int
}

func (a *app) position() chatPosition {
	pos := chatPosition{chat: a.chats[a.selected].key}
	if len(a.log) != 0 {
		pos.id = a.log[a.offset].id
	}

	return pos
}

func (a *app) requestJump(out chan<- request, kind string, target chatPosition) {
	if a.pending != "" {
		return
	}

	index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == target.chat })
	if index < 0 {
		a.status = "Original chat is outside the loaded chat list"
		return
	}

	origin := a.position()
	if target.chat == origin.chat && target.id != 0 && kind != "unread" {
		if i := slices.IndexFunc(a.log, func(m message) bool { return m.id == target.id }); i >= 0 {
			a.offset, a.status = i, ""
			a.recordJump(kind, origin, target.id)

			return
		}
	}

	if a.demo {
		a.status = "Message is outside the demo history"
		return
	}

	a.jumpOrigin = origin
	a.ask(out, request{kind: kind, chat: a.chats[index], anchor: target.id})
}

func (a *app) recordJump(kind string, origin chatPosition, targetID int) {
	if kind == "jumpBack" {
		a.jumps = a.jumps[:len(a.jumps)-1]
		return
	}

	if origin.id == 0 || origin.chat == a.chats[a.selected].key && origin.id == targetID {
		return
	}

	if len(a.jumps) == 32 {
		a.jumps = slices.Delete(a.jumps, 0, 1)
	}

	a.jumps = append(a.jumps, origin)
}

func (a *app) receiveJump(e serverEvent) {
	a.pending = ""

	index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat })
	if index < 0 || len(e.log) == 0 {
		a.status = "Message is unavailable"
		if e.kind == "unread" {
			a.status = "No unread messages found"
		}

		return
	}

	i := slices.IndexFunc(e.log, func(m message) bool { return m.id == e.anchor })
	if i < 0 {
		a.status = "Original message is unavailable"
		return
	}

	if index != a.selected {
		a.resetChat()
		a.selected, a.open = index, true
	}

	a.log, a.offset, a.status = e.log, i, ""
	a.olderEnd = false
	a.newerTrimmed = e.log[0].id < a.chats[index].unread.topID
	a.prefetchAnchor = 0
	a.recordJump(e.kind, a.jumpOrigin, e.anchor)
}

func (a *app) jumpToReply(out chan<- request) {
	if len(a.log) == 0 {
		return
	}

	reply := a.log[a.offset].reply
	if reply.id == 0 {
		if a.log[a.offset].forward != nil {
			a.jumpToForward(out)
			return
		}

		a.status = "Choose a reply or forwarded message"

		return
	}

	target := chatPosition{chat: a.chats[a.selected].key, id: reply.id}
	if reply.peer.id != 0 {
		target.chat = reply.peer
	}

	a.requestJump(out, "jump", target)
}

func (a *app) backToMessage(out chan<- request) {
	if len(a.jumps) == 0 {
		a.status = "No previous message position"
		return
	}

	a.requestJump(out, "jumpBack", a.jumps[len(a.jumps)-1])
}

func (a *app) jumpToUnread(out chan<- request) {
	current := a.chats[a.selected]
	if current.unread.count <= 0 {
		a.status = "No unread messages"
		return
	}

	a.requestJump(out, "unread", chatPosition{chat: current.key, id: current.unread.inboxID})
}

func (a *app) prefetchHistory(out chan<- request) {
	if !a.open || a.demo || a.composing || a.imagePath || a.searching || a.serverResults != nil ||
		a.forwardIDs != nil || a.confirmDelete != nil || a.linkChoices != nil ||
		a.pending != "" || a.prefetching || a.authorLookup || a.autoReadPending || a.olderEnd || a.historyRoom <= 0 || len(a.log) == 0 ||
		len(a.log)+historyLimit-historyWindow > a.offset {
		return
	}

	anchor := a.log[len(a.log)-1].id
	if anchor == a.prefetchAnchor {
		return
	}

	select {
	case out <- request{kind: "prefetch", chat: a.chats[a.selected], anchor: anchor}:
		a.prefetching, a.prefetchAnchor = true, anchor
	default:
	}
}

func (a *app) receiveSearch(e serverEvent) {
	a.pending = ""
	if !a.open || a.chats[a.selected].key != e.chat || a.serverQuery == "" {
		return
	}

	if len(e.log) == 0 {
		if a.serverResults != nil {
			a.serverEnd = true
		} else {
			a.status = "No matches in chat history"
		}

		return
	}

	if e.step > 0 {
		if len(a.serverPrevious) == 32 {
			a.serverPrevious = slices.Delete(a.serverPrevious, 0, 1)
		}

		a.serverPrevious = append(a.serverPrevious, a.serverAnchor)
	} else if e.step < 0 {
		a.serverPrevious = a.serverPrevious[:len(a.serverPrevious)-1]
	}

	a.serverResults, a.serverIndex, a.serverAnchor = e.log, 0, e.anchor
	a.serverEnd = len(e.log) < historyLimit
}

func (a *app) serverSearchInput(k key, out chan<- request) {
	if k.name == "escape" || k.name == "left" || k.name == "" && k.text == "h" {
		a.serverResults, a.serverQuery = nil, ""
		return
	}

	if a.pending != "" {
		return
	}

	query := request{kind: "searchHistory", chat: a.chats[a.selected], text: a.serverQuery}
	switch {
	case k.name == "enter":
		target := a.serverResults[a.serverIndex].id
		a.serverResults, a.serverQuery = nil, ""
		a.requestJump(out, "jump", chatPosition{chat: query.chat.key, id: target})
	case k.name == "up" || k.name == "" && k.text == "k":
		a.serverIndex = max(0, a.serverIndex-1)
	case k.name == "down" || k.name == "" && k.text == "j":
		a.serverIndex = min(len(a.serverResults)-1, a.serverIndex+1)
	case k.name == "pagedown" || k.name == "" && k.text == "n":
		if !a.serverEnd {
			query.anchor = a.serverResults[len(a.serverResults)-1].id
			query.step = 1
			a.ask(out, query)
		}
	case k.name == "pageup" || k.name == "" && k.text == "N":
		if len(a.serverPrevious) > 0 {
			query.anchor = a.serverPrevious[len(a.serverPrevious)-1]
			query.step = -1
			a.ask(out, query)
		}
	}
}

func searchResultLabel(msg message) string {
	text := messageExcerpt(msg, 0)
	return fmt.Sprintf("%s  %s", msg.date.UTC().Format("02 Jan 15:04"), text)
}
