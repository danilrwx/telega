package main

import (
	"fmt"
	"slices"
)

type app struct {
	demo, ready, open, composing, masked bool
	pending, prompt, status, qr          string
	draft                                []rune
	draftCursor                          int
	chats                                []chat
	log                                  []message
	selected, offset, revealID           int
	focused, olderEnd, newerTrimmed      bool
	prefixG, searching                   bool
	search                               []rune
	chatQuery, messageQuery              string
	selection                            map[int]struct{}
	confirmDelete                        []int
	deleteForEveryone                    bool
	linkChoices                          []string
	linkIndex                            int
	openTarget                           string
	imagePath                            bool
	staged                               *uploadImage
	replyID, replyTopic                  int
	replyPreview                         string
	imageFromCompose                     bool
	drafts                               map[peerKey]chatDraft
	editID                               int
	editOriginal                         string
	editHasAttachment                    bool
	forwardIDs                           []int
	forwardIndex                         int
	forwardQuery                         string
	forwardConfirm                       bool
	forwardTo                            chat
	copyText                             string
	authors                              map[peerKey]string
	authorLookup                         bool
	autoReadPending                      bool
	lastAutoRead                         chatPosition
	historyRoom, prefetchAnchor          int
	prefetching                          bool
	jumps                                []chatPosition
	jumpOrigin                           chatPosition
	historySearch                        bool
	serverResults                        []message
	serverQuery                          string
	serverIndex, serverAnchor            int
	serverEnd                            bool
	serverPrevious                       []int
}

func (a *app) ask(out chan<- request, req request) {
	out <- req

	a.pending, a.status = req.kind, ""
}

func (a *app) receive(out chan<- request, e serverEvent) {
	a.rememberAuthors(e.log)

	switch e.kind {
	case "prompt":
		a.prompt, a.masked, a.pending, a.qr, a.status = e.text, e.masked, "", "", e.hint
	case "qr":
		a.qr, a.pending, a.prompt, a.status = e.text, "", "", ""
	case "ready":
		a.ready, a.prompt, a.qr = true, "", ""
		a.ask(out, request{kind: "chats"})
	case "chats":
		if len(a.chats) > 0 {
			id := a.chats[a.selected].key
			a.selected = max(0, slices.IndexFunc(e.chats, func(c chat) bool { return c.key == id }))
		}

		a.chats, a.pending = e.chats, ""
	case "history", "older", "newer", "latest":
		if a.open && a.chats[a.selected].key == e.chat {
			a.mergePage(e)

			if e.kind == "history" {
				a.restoreDraft()
			}
		}

		a.pending = ""
	case "author":
		a.authorLookup = false
		a.rememberAuthor(e.author, e.text)
	case "prefetch":
		a.prefetching = false
		if e.text == "" && a.open && a.chats[a.selected].key == e.chat &&
			len(a.log) > 0 && a.log[len(a.log)-1].id == e.anchor {
			a.mergePage(serverEvent{kind: "older", log: e.log})
		}
	case "jump", "unread", "jumpBack":
		a.receiveJump(e)
	case "searchHistory":
		a.receiveSearch(e)
	case "forward":
		a.pending = ""
		for _, id := range e.ids {
			delete(a.selection, id)
		}

		a.status = fmt.Sprintf("Forwarded %d message(s) to %s", len(e.ids), e.text)
		if e.hint != "" {
			a.status += ". " + e.hint
		}

		if a.open && a.chats[a.selected].key == e.chat && !a.newerTrimmed {
			for _, msg := range e.log {
				a.insert(msg, true)
			}
		}
	case "edit":
		a.pending = ""
		if a.open && a.chats[a.selected].key == e.chat {
			i := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor })
			if i >= 0 {
				if len(e.log) > 0 {
					a.replaceLoadedMessage(i, e.log[0])
				} else {
					text, entities := codeBlocks(string(a.draft))
					a.log[i].text, a.log[i].spans, a.log[i].links = text, messageSpans(entities), messageLinks(text, nil, nil)
				}
			}

			a.status = "Message edited"
		}

		a.clearComposer()
	case "imageReady":
		if !a.imageFromCompose {
			a.clearDraft()
		}

		a.imageFromCompose = false
		a.staged, a.composing, a.pending = e.upload, true, ""
	case "send":
		uploaded := a.staged != nil
		a.clearReply()
		a.discardImage()
		a.clearDraft()

		a.pending = ""
		if a.open && a.chats[a.selected].key == e.chat {
			if a.newerTrimmed || uploaded && (len(e.log) == 0 || e.log[0].attachment == nil) {
				a.ask(out, request{kind: "latest", chat: a.chats[a.selected]})
			} else {
				for _, msg := range e.log {
					a.insert(msg, false)
				}

				a.offset = 0
			}
		}
	case "new", "live":
		if e.kind == "new" {
			a.newChatMessage(e)
		}

		if a.open && a.chats[a.selected].key == e.chat {
			msg := e.log[0]
			if e.kind == "live" {
				if i := slices.IndexFunc(a.log, func(m message) bool { return m.id == msg.id }); i >= 0 {
					a.replaceLoadedMessage(i, msg)
				}
			} else if len(a.log) == 0 || msg.id >= a.log[len(a.log)-1].id &&
				(msg.id <= a.log[0].id || !a.newerTrimmed && a.pending != "older") {
				a.insert(msg, true)
			} else if msg.id > a.log[0].id {
				a.newerTrimmed = true
			}
		}
	case "read", "markRead", "autoRead", "unreadMark":
		if e.kind == "autoRead" {
			a.autoReadPending = false

			if e.text != "" {
				return
			}
		}

		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			current := &a.chats[i].unread

			switch e.kind {
			case "unreadMark":
				current.marked = e.unread.marked
			case "read":
				if e.unread.inboxID >= current.inboxID {
					current.inboxID, current.count = e.unread.inboxID, e.unread.count
				}
			case "markRead", "autoRead":
				if e.unread.topID >= current.topID && e.unread.inboxID >= current.inboxID {
					*current = e.unread
				}
			}
		}

		if e.kind == "markRead" {
			a.pending = ""
			a.status = "Marked as read through the selected message"
		}
	case "mute":
		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			a.chats[i].muteUntil = e.muteUntil
		}
	case "download":
		a.pending = ""
		a.status = "Downloaded: " + e.text
		a.openTarget = e.text
	case "deletedMessages":
		if a.open && a.chats[a.selected].key == e.chat {
			a.removeMessages(e.ids)
		}

		a.pending = ""
		if e.text == "" {
			a.status = fmt.Sprintf("Deleted %d message(s)", len(e.ids))
		} else if len(e.ids) > 0 {
			a.status = fmt.Sprintf("Deleted %d message(s). %s", len(e.ids), e.text)
		} else {
			a.status = e.text
		}
	case "openError":
		a.status = e.text
	case "delete":
		if a.open {
			peer := a.chats[a.selected].key
			if peer == e.chat || (e.chat.id == 0 && peer.kind != 'c') {
				a.removeMessages(e.ids)
			}
		}
	case "error", "offline":
		a.composing = a.pending == "send" || a.pending == "edit" || a.imageFromCompose
		if a.pending == "history" && a.open && e.kind != "offline" {
			a.restoreDraft()
		}

		a.imageFromCompose = false
		a.imagePath = a.pending == "image"

		a.status, a.pending = e.text, ""
		if e.kind == "offline" {
			a.prompt, a.qr, a.ready, a.open, a.composing = "", "", false, false, false
			a.confirmDelete, a.linkChoices, a.selection = nil, nil, nil
			a.imagePath = false
			a.discardImage()
			a.clearReply()
		}
	}
}

func (a *app) replaceLoadedMessage(i int, msg message) {
	if msg.authorName == "" && msg.author == a.log[i].author {
		msg.authorName, msg.authorDirectoryName = a.log[i].authorName, a.log[i].authorDirectoryName
	}

	a.log[i] = msg
}

func (a *app) insert(msg message, replace bool) {
	i, found := slices.BinarySearchFunc(a.log, msg.id, func(m message, id int) int {
		return id - m.id
	})
	if found {
		if replace {
			a.replaceLoadedMessage(i, msg)
		}
	} else if i < historyWindow {
		a.log = slices.Insert(a.log, i, msg)
		if len(a.log) > historyWindow {
			a.log = slices.Delete(a.log, historyWindow, len(a.log))
			a.olderEnd = false
		}

		if a.offset > 0 && i <= a.offset {
			a.offset = min(a.offset+1, len(a.log)-1)
		}
	}
}
