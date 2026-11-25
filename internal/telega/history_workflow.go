package telega

import (
	"slices"
)

type chatPosition struct {
	chat peerKey
	id   int
}

func (a *app) position() chatPosition {
	pos := chatPosition{chat: a.currentChat().key}
	if len(a.log) != 0 {
		pos.id = a.log[a.offset].id
	}

	return pos
}

func (a *app) requestJump(out chan<- request, kind string, target chatPosition) {
	if a.pending != "" {
		return
	}

	destination := a.currentChat()
	if destination.key != target.chat {
		if a.thread != nil {
			a.status = "Go back to the parent chat to jump to another conversation"
			return
		}

		index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == target.chat })
		if index < 0 {
			a.status = "Original chat is outside the loaded chat list"
			return
		}

		destination = a.chats[index]
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
	a.ask(out, request{kind: kind, chat: destination, anchor: target.id})
}

func (a *app) recordJump(kind string, origin chatPosition, targetID int) {
	if kind == "jumpBack" {
		a.jumps = a.jumps[:len(a.jumps)-1]
		return
	}

	if origin.id == 0 || origin.chat == a.currentChat().key && origin.id == targetID {
		return
	}

	if len(a.jumps) == 32 {
		a.jumps = slices.Delete(a.jumps, 0, 1)
		a.threadJumpBase = max(0, a.threadJumpBase-1)
	}

	a.jumps = append(a.jumps, origin)
}

func (a *app) receiveJump(e serverEvent) {
	a.pending = ""

	index := a.selected
	if a.thread == nil {
		index = slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat })
	}

	if index < 0 || len(e.log) == 0 || a.thread != nil && !a.inConversation(e) {
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
	a.newerTrimmed = e.log[0].id < a.currentChat().unread.topID
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

	target := chatPosition{chat: a.currentChat().key, id: reply.id}
	if reply.peer.id != 0 {
		target.chat = reply.peer
	}

	a.requestJump(out, "jump", target)
}

func (a *app) backToMessage(out chan<- request) {
	if a.thread != nil && len(a.jumps) == a.threadJumpBase && a.pending == "" {
		a.leaveThread(out)
		return
	}

	if len(a.jumps) == a.threadJumpBase {
		a.status = "No previous message position"
		return
	}

	a.requestJump(out, "jumpBack", a.jumps[len(a.jumps)-1])
}

func (a *app) jumpToUnread(out chan<- request) {
	current := a.currentChat()
	if current.unread.count <= 0 {
		a.status = "No unread messages"
		return
	}

	a.requestJump(out, "unread", chatPosition{chat: current.key, id: current.unread.inboxID})
}

func (a *app) prefetchHistory(out chan<- request) {
	if a.chatActions != nil || !a.open || a.demo || a.composing || a.filePicker != nil || a.textAttachmentPrompt != nil || a.searching || a.messageSearch != nil ||
		len(a.topicEntries) > 0 || a.forwardIDs != nil || a.chatPicker || a.confirmDelete != nil || a.linkChoices != nil ||
		a.pending != "" || a.prefetching || a.authorLookup || a.autoReadPending || a.olderEnd || a.historyRoom <= 0 || len(a.log) == 0 ||
		len(a.log)+historyLimit-historyWindow > a.offset {
		return
	}

	anchor := a.log[len(a.log)-1].id
	if anchor == a.prefetchAnchor {
		return
	}

	select {
	case out <- request{kind: "prefetch", chat: a.currentChat(), anchor: anchor}:
		a.prefetching, a.prefetchAnchor = true, anchor
	default:
	}
}
