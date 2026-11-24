package telega

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/danilrwx/telega/tui"
)

type app struct {
	demo, ready, open, composing, masked           bool
	colors                                         *terminalPalette
	inlineImages                                   *tui.InlineImages
	inlineLoading                                  bool
	layoutSettings                                 tui.LayoutSettings
	display                                        displaySettings
	settingsOpen                                   bool
	settingsCursor                                 int
	textReactions, console                         bool
	pending, prompt, status, qr                    string
	draft                                          []rune
	draftCursor                                    int
	vimRequested                                   bool
	draftStore                                     *tui.DraftStore
	accountID                                      int64
	loadedDraftKey                                 string
	savedDraft                                     diskDraft
	chats                                          []chat
	dialogsLoaded                                  bool
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
	reactionTarget, reactionIndex                  int
	reactionPickerOpen, reactionCatalogLoaded      bool
	reactionSuggestionIndex                        int
	reactionClosing                                bool
	reactionAuthors                                *tui.ReactionAuthors
	reactionQueue                                  []reactionChoice
	reactionQuery                                  string
	reactionCatalog                                []reactionChoice
	search                                         []rune
	chatQuery, messageQuery                        string
	chatPicker, chatDirectoryStarted               bool
	chatDirectoryLoading                           bool
	chatPickerIndex                                int
	chatDirectory                                  []chat
	unreadOnly                                     bool
	selection                                      map[int]struct{}
	confirmDelete                                  []int
	deleteForEveryone                              bool
	linkChoices                                    []messageLink
	linkIndex                                      int
	attachmentChoices                              []attachmentChoice
	attachmentTitle                                string
	attachmentIndex                                int
	openTarget                                     string
	playTarget                                     string
	openExternal                                   string
	previewOnly                                    bool
	imagePreview                                   *tui.TerminalImage
	imagePreviewTitle                              string
	imagePreviewPath                               string
	asFile                                         bool
	retrySend                                      *request
	disconnected                                   bool
	resumeChat                                     peerKey
	staged                                         *uploadAttachment
	moreUploads                                    []stagedAttachment
	filePicker                                     *tui.FilePicker
	textAttachmentPrompt                           *tui.TextAttachmentPrompt
	editAttachmentName                             string
	pickerFile, editAttachmentRequested            bool
	editAttachmentIndex                            int
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
	directorySearch                                directorySearch
	forwardConfirm                                 bool
	forwardTo                                      chat
	copyText                                       string
	authors                                        map[peerKey]string
	authorAvatars                                  map[peerKey]*attachment
	authorLookup                                   bool
	autoReadPending                                bool
	autoReadVisible                                func() bool
	lastAutoRead                                   chatPosition
	historyRoom, prefetchAnchor                    int
	historyDue                                     time.Time
	historySeq                                     uint64
	historyCancel                                  context.CancelFunc
	prefetching                                    bool
	jumps                                          []chatPosition
	jumpOrigin                                     chatPosition
	messageSearch                                  *tui.MessageSearch
	messageSearchScope                             chat
	messageSearchHits                              []messageHit
	messageSearchCursor                            messageSearchCursor
	attachmentGallery                              *attachmentGallery
	voiceRequested                                 bool
	voice                                          *voiceRecording
	playback                                       *voicePlayback
	transcript                                     *voiceTranscript
	voiceTranscripts                               map[chatPosition]*voiceTranscript
}

func (a *app) ask(out chan<- request, req request) {
	if isChatHistory(req.kind) {
		req = a.trackHistory(req)
	}

	out <- req

	a.pending, a.status = req.kind, ""
}

func (a *app) receive(out chan<- request, e serverEvent) {
	if e.historyID != 0 {
		if a.historyCancel == nil || e.historyID != a.historySeq {
			return
		}

		a.cancelHistory()
	}

	defer func() {
		a.keepUnreadSelection()

		if a.pickingReaction && a.reactionTarget != 0 {
			if i := slices.IndexFunc(a.log, func(msg message) bool { return msg.id == a.reactionTarget }); i >= 0 {
				a.offset = i
			} else {
				a.closeReactionPicker()
			}
		}
	}()

	a.rememberAuthors(e.log)

	switch e.kind {
	case "prompt":
		a.prompt, a.masked, a.pending, a.qr, a.status = e.text, e.masked, "", "", e.hint
	case "qr":
		a.qr, a.pending, a.prompt, a.status = e.text, "", "", ""
	case "ready":
		a.chatDirectory, a.chatDirectoryStarted, a.chatDirectoryLoading = nil, false, false
		a.ready, a.prompt, a.qr, a.disconnected = true, "", "", false
		a.dialogsLoaded = false

		if a.accountID != e.accountID {
			a.authorAvatars = nil
		}

		a.accountID = e.accountID
		a.rememberAvatar(peerKey{'u', e.accountID}, e.avatar)
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
		a.rememberAvatar(e.author, e.avatar)
	case "prefetch":
		a.prefetching = false
		if e.text == "" && a.inConversation(e) &&
			len(a.log) > 0 && a.log[len(a.log)-1].id == e.anchor {
			a.mergePage(serverEvent{kind: "older", log: e.log})
		}
	case "jump", "unread", "jumpBack":
		a.receiveJump(e)
	case "messageSearch":
		a.receiveMessageSearch(e)
	case "chatDirectory", "directorySearch":
		a.receiveChatDirectory(e)
	case "chatDirectoryDone":
		a.chatDirectoryLoading = false
		if e.hint != "" {
			a.chatDirectoryStarted = false
		}
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
		a.addStaged(e.upload, false)
		a.composing, a.pending = true, ""
	case "send":
		if len(a.moreUploads) > 0 {
			sent := a.replaceStaged(0, stagedAttachment{})
			a.clearDraft()
			a.retrySend = nil

			a.composing, a.pending = true, ""
			if a.persistDraft() {
				sent.discard()
			}

			if a.inConversation(e) {
				for _, msg := range e.log {
					a.insert(msg, false)
				}

				a.offset = 0
			}

			a.submitDraft(out)

			break
		}

		sent := a.staged
		uploaded := sent != nil
		a.staged = nil
		a.clearComposer()

		if a.persistDraft() && sent != nil {
			sent.discard()
		}

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
	case "reactionAuthors":
		if a.reactionAuthors != nil && a.reactionAuthors == e.reactionAuthors {
			a.reactionAuthors.SetRows(e.reactionAuthorRows, e.text)
		}
	case "reactionCatalog":
		a.pending = ""
		if a.pickingReaction && a.currentChat().key == e.chat {
			a.reactionCatalog, a.reactionCatalogLoaded = e.messageReactions, true
		}
	case "react", "reactions":
		if e.kind == "react" {
			a.pending = ""
		}

		if a.open && a.currentChat().key == e.chat {
			if index := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor }); index >= 0 {
				if e.kind == "react" {
					a.status = e.hint
					if e.hint == "" {
						a.log[index].chosenReactions = e.chosenReactions

						a.log[index].myReaction = ""
						if n := len(e.chosenReactions); n > 0 {
							a.log[index].myReaction = e.chosenReactions[n-1].emoji
						}

						a.status = "Reaction updated"
					}
				} else {
					a.log[index].reactions, a.log[index].hasReactions = e.text, true

					a.log[index].messageReactions = e.messageReactions
					if e.reactionChoiceKnown {
						a.log[index].chosenReactions = e.chosenReactions
						a.log[index].myReaction = e.myReaction
					}
				}
			}
		}

		if e.kind == "react" && a.pickingReaction {
			if len(a.reactionQueue) > 0 {
				choice := a.reactionQueue[0]
				a.reactionQueue = a.reactionQueue[1:]
				a.submitReaction(choice, out)
			} else if a.reactionClosing {
				a.closeReactionPicker()
			} else if a.reactionPickerOpen && !a.reactionCatalogLoaded {
				a.ask(out, request{kind: "reactionCatalog", chat: a.currentChat()})
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
	case "presence":
		if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			a.chats[i].presence = e.presence
		}
	case "readOutbox":
		if e.threadID != 0 {
			if a.inConversation(e) && a.thread != nil {
				a.thread.readOutboxID = max(a.thread.readOutboxID, e.anchor)
			}

			if i := slices.IndexFunc(a.topicEntries, func(c chat) bool {
				return c.key == e.chat && c.threadID == e.threadID
			}); i >= 0 {
				a.topicEntries[i].readOutboxID = max(a.topicEntries[i].readOutboxID, e.anchor)
			}
		} else if i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat }); i >= 0 {
			a.chats[i].readOutboxID = max(a.chats[i].readOutboxID, e.anchor)
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
	case "inlineImage":
		a.inlineLoading = false
		if a.inlineImages != nil {
			a.inlineImages.Store(e.text, e.inlineImage)
		}
	case "download", "reactionImage":
		a.pending, a.status = "", ""

		if e.playVoice {
			a.playTarget = e.text
		} else {
			a.openTarget = e.text
			a.previewOnly = e.kind == "reactionImage"
		}
	case "voiceTranscript":
		a.receiveVoiceTranscript(e)
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
		failedSend := a.pending == "send"

		a.composing = a.composing || failedSend || a.pending == "edit" || a.imageFromCompose
		if a.pending == "history" && a.open && e.kind != "offline" {
			a.restoreDraft()
		}

		a.imageFromCompose = false

		a.status, a.pending = e.text, ""
		if e.kind == "offline" {
			clear(a.voiceTranscripts)

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
		} else if failedSend {
			a.status = "Send failed · Enter retry · " + e.text
		}
	}
}

func (a *app) replaceLoadedMessage(i int, msg message) {
	if msg.authorName == "" && msg.author == a.log[i].author {
		msg.authorName, msg.authorDirectoryName = a.log[i].authorName, a.log[i].authorDirectoryName
	}

	if msg.avatar == nil && msg.avatarPeer == a.log[i].avatarPeer {
		msg.avatar = a.log[i].avatar
	}

	if !msg.hasReactions {
		msg.reactions, msg.hasReactions, msg.myReaction = a.log[i].reactions, a.log[i].hasReactions, a.log[i].myReaction
		msg.chosenReactions, msg.messageReactions = a.log[i].chosenReactions, a.log[i].messageReactions
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
