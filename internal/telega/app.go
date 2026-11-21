package telega

import (
	"fmt"
	"slices"
)

type app struct {
	demo, ready, open, composing, masked           bool
	colors                                         *terminalPalette
	textReactions                                  bool
	pending, prompt, status, qr                    string
	draft                                          []rune
	draftCursor                                    int
	chats                                          []chat
	dialogPage                                     dialogPage
	dialogPrevious                                 []dialogCursor
	thread, resumeThread                           *chat
	threadReturnID, threadJumpBase, resumeReturnID int
	topicEntries                                   []chat
	topicPage                                      topicPage
	topicPrevious                                  []topicCursor
	topicIndex                                     int
	log                                            []message
	selected, offset, revealID                     int
	focused, olderEnd, newerTrimmed                bool
	visibleMessages                                int
	noticeStatus                                   string
	noticeStatusVisible                            bool
	recentReactionAlerts                           map[chatPosition]int
	prefixG, searching, pickingReaction            bool
	search                                         []rune
	chatQuery, messageQuery                        string
	selection                                      map[int]struct{}
	confirmDelete                                  []int
	deleteForEveryone                              bool
	linkChoices                                    []messageLink
	linkIndex                                      int
	openTarget                                     string
	imagePath, filePath, asFile                    bool
	retrySend                                      *request
	disconnected                                   bool
	resumeChat                                     peerKey
	staged                                         *uploadAttachment
	replyID, replyTopic                            int
	replyPreview                                   string
	imageFromCompose                               bool
	drafts                                         map[chatPosition]chatDraft
	editID                                         int
	editOriginal                                   string
	editHasAttachment                              bool
	forwardIDs                                     []int
	forwardIndex                                   int
	forwardQuery                                   string
	forwardConfirm                                 bool
	forwardTo                                      chat
	copyText                                       string
	authors                                        map[peerKey]string
	authorLookup                                   bool
	autoReadPending                                bool
	lastAutoRead                                   chatPosition
	historyRoom, prefetchAnchor                    int
	prefetching                                    bool
	jumps                                          []chatPosition
	jumpOrigin                                     chatPosition
	historySearch                                  bool
	serverResults                                  []message
	serverQuery                                    string
	serverIndex, serverAnchor                      int
	serverEnd                                      bool
	serverPrevious                                 []int
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
		a.ready, a.prompt, a.qr, a.disconnected = true, "", "", false
		a.ask(out, request{kind: "chats", page: dialogPage{folder: a.dialogPage.folder, cursor: a.dialogPage.cursor}})
	case "chats":
		a.receiveDialogs(out, e)
	case "archiveChat":
		a.pending = ""
		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			a.chats = slices.Delete(a.chats, i, i+1)
			a.selected = min(a.selected, max(0, len(a.chats)-1))
		}

		a.status = "Chat archived"
		if e.page.folder == 1 {
			a.status = "Chat moved out of archive"
		}
	case "topics":
		a.receiveTopics(e)
	case "discussion":
		a.pending = ""
		if a.inConversation(e) && e.thread != nil {
			a.openThread(out, *e.thread)
		}
	case "threadParent":
		a.receiveThreadParent(e)
	case "history", "older", "newer", "latest":
		if a.inConversation(e) {
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
		if e.text == "" && a.inConversation(e) &&
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

		if a.open && a.thread == nil && a.currentChat().key == e.chat && !a.newerTrimmed {
			for _, msg := range e.log {
				a.insert(msg, true)
			}
		}
	case "edit":
		a.pending = ""
		if a.inConversation(e) {
			i := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor })
			if i >= 0 {
				if len(e.log) > 0 {
					a.replaceLoadedMessage(i, e.log[0])
				} else {
					text, entities := codeBlocks(string(a.draft))
					a.log[i].text, a.log[i].spans, a.log[i].links = text, messageSpans(entities), messageLinks(text, nil, nil)
					a.log[i].edited = true
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
		a.staged, a.composing, a.pending, a.asFile = e.upload, true, "", false
	case "send":
		uploaded := a.staged != nil
		a.clearComposer()

		a.pending = ""
		if a.inConversation(e) {
			if a.newerTrimmed || len(e.log) == 0 || uploaded && e.log[0].attachment == nil {
				a.ask(out, request{kind: "latest", chat: a.currentChat()})
			} else {
				for _, msg := range e.log {
					a.insert(msg, false)
				}

				a.offset = 0
			}
		}
	case "react", "reactions":
		if e.kind == "react" {
			a.pending = ""
		}

		if a.open && a.currentChat().key == e.chat {
			if index := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor }); index >= 0 {
				if e.kind == "react" {
					a.log[index].myReaction = e.text
					a.status = "Reaction updated"
				} else {
					a.log[index].reactions, a.log[index].hasReactions = e.text, true
					if e.reactionChoiceKnown {
						a.log[index].myReaction = e.myReaction
					}
				}
			}
		}
	case "new", "live":
		if e.kind == "new" {
			a.newChatMessage(e)
		}

		if a.open && a.currentChat().key == e.chat && a.currentChat().includesMessage(e.log[0]) {
			msg := e.log[0]
			if e.kind == "new" && a.thread != nil && msg.id > a.thread.unread.topID {
				a.thread.unread.topID = msg.id
				if !msg.out && msg.id > a.thread.unread.inboxID {
					a.thread.unread.count++
				}
			}

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

		if e.threadID != 0 {
			if a.inConversation(e) && e.unread.inboxID >= a.thread.unread.inboxID {
				if e.unread.topID >= a.thread.unread.topID {
					a.thread.unread = e.unread
				} else {
					a.thread.unread.inboxID = e.unread.inboxID
				}
			}

			if e.kind == "markRead" {
				a.pending, a.status = "", "Marked as read through the selected message"
			}

			break
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
	case "download":
		a.pending = ""
		a.status = "Downloaded: " + e.text
		a.openTarget = e.text
	case "deletedMessages":
		if a.open && a.currentChat().key == e.chat {
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
			peer := a.currentChat().key
			if peer == e.chat || (e.chat.id == 0 && peer.kind != 'c') {
				a.removeMessages(e.ids)
			}
		}
	case "error", "offline":
		a.composing = a.composing || a.pending == "send" || a.pending == "edit" || a.imageFromCompose
		if a.pending == "history" && a.open && e.kind != "offline" {
			a.restoreDraft()
		}

		a.imageFromCompose = false
		a.imagePath = a.imagePath || a.pending == "image" || a.pending == "file"

		a.status, a.pending = e.text, ""
		if e.kind == "offline" {
			if a.open {
				a.resumeChat = a.chats[a.selected].key
				a.resumeThread, a.resumeReturnID = a.thread, a.threadReturnID
				a.parkDraft()
				a.resetChat()
			}

			a.prompt, a.qr, a.ready, a.disconnected = "", "", false, true
			a.confirmDelete, a.linkChoices, a.selection = nil, nil, nil
			a.open = false
			a.status = e.text
		} else if a.retrySend != nil && a.composing {
			a.status = "Send failed · Enter retries · " + e.text
		}
	}
}

func (a *app) replaceLoadedMessage(i int, msg message) {
	if msg.authorName == "" && msg.author == a.log[i].author {
		msg.authorName, msg.authorDirectoryName = a.log[i].authorName, a.log[i].authorDirectoryName
	}

	if !msg.hasReactions {
		msg.reactions, msg.hasReactions, msg.myReaction = a.log[i].reactions, a.log[i].hasReactions, a.log[i].myReaction
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
