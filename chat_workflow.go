package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type chatDraft struct {
	text, replyPreview, editOriginal string
	replyID, replyTopic, editID      int
	cursor                           int
	editHasAttachment                bool
	upload                           *uploadImage
	imagePath                        bool
}

func (a *app) parkDraft() {
	if !a.composing && !a.imagePath {
		return
	}

	if len(a.draft) > 0 || a.staged != nil || a.replyID != 0 || a.editID != 0 {
		if a.drafts == nil {
			a.drafts = make(map[peerKey]chatDraft)
		}

		a.drafts[a.chats[a.selected].key] = chatDraft{
			text: string(a.draft), replyPreview: a.replyPreview, editOriginal: a.editOriginal, replyID: a.replyID, replyTopic: a.replyTopic,
			editID: a.editID, cursor: a.draftCursor, editHasAttachment: a.editHasAttachment, upload: a.staged, imagePath: a.imagePath,
		}
		a.staged = nil
	}

	a.clearComposer()
}

func (a *app) restoreDraft() {
	key := a.chats[a.selected].key
	if draft, ok := a.drafts[key]; ok {
		delete(a.drafts, key)
		a.draft = append(a.draft[:0], []rune(draft.text)...)
		a.draftCursor, a.editHasAttachment, a.editOriginal = draft.cursor, draft.editHasAttachment, draft.editOriginal
		a.replyID, a.replyTopic, a.replyPreview = draft.replyID, draft.replyTopic, draft.replyPreview
		a.editID, a.staged, a.imagePath, a.composing = draft.editID, draft.upload, draft.imagePath, !draft.imagePath
	}
}

func (a *app) clearComposer() {
	a.composing, a.imagePath, a.editID, a.editHasAttachment, a.editOriginal = false, false, 0, false, ""
	a.clearDraft()
	a.clearReply()
	a.discardImage()
}

func (a *app) resetChat() {
	a.open, a.status, a.log, a.offset = false, "", nil, 0
	a.olderEnd, a.newerTrimmed, a.messageQuery = false, false, ""
	a.revealID, a.prefetchAnchor = 0, 0
	a.selection = nil
	a.searching, a.historySearch, a.prefixG = false, false, false
}

func (a *app) openChat(out chan<- request, index int) {
	a.selected, a.open, a.status = index, true, ""
	if a.demo {
		for i, text := range []string{"Привет! Это демонстрационный чат.", "Enter — написать, Esc — вернуться к чатам.", "Работает даже без сети."} {
			a.insert(message{id: i + 1, text: text, date: time.Now(), out: i == 2}, false)
		}

		a.restoreDraft()
	} else {
		a.ask(out, request{kind: "history", chat: a.chats[index]})
	}
}

func (a *app) switchChat(out chan<- request, step int) {
	if a.pending != "" {
		return
	}

	next := a.selected + step
	if next < 0 || next >= len(a.chats) {
		return
	}

	a.parkDraft()
	a.resetChat()
	a.chatQuery = ""
	a.openChat(out, next)
}

func (a *app) beginEdit() {
	if a.pending != "" || len(a.log) == 0 {
		return
	}

	msg := a.log[a.offset]
	if !msg.out || msg.service {
		a.status = "Choose one of your messages to edit"
		return
	}

	a.clearComposer()

	a.editID, a.editHasAttachment, a.composing, a.status = msg.id, msg.attachment != nil, true, ""
	if msg.text != "[Attachment]" || msg.attachment == nil {
		a.editOriginal = editableCode(msg)
		a.draft = append(a.draft, []rune(a.editOriginal)...)
		a.draftCursor = len(a.draft)
	}
}

func (a *app) submitDraft(out chan<- request) {
	text := string(a.draft)
	if a.editID != 0 {
		if text == a.editOriginal {
			a.clearComposer()
			return
		}

		i := slices.IndexFunc(a.log, func(m message) bool { return m.id == a.editID })
		if strings.TrimSpace(text) == "" && !a.editHasAttachment {
			a.status = "Enter text for the edited message"
			return
		}

		if a.demo {
			if i >= 0 {
				plain, entities := codeBlocks(text)
				a.log[i].text, a.log[i].spans, a.log[i].links = plain, messageSpans(entities), messageLinks(plain, nil, nil)
			}

			a.clearComposer()
		} else {
			a.ask(out, request{kind: "edit", chat: a.chats[a.selected], anchor: a.editID, text: text})
			a.composing = false
		}

		return
	}

	if strings.TrimSpace(text) == "" && a.staged == nil {
		return
	}

	if a.demo {
		text, entities := codeBlocks(text)

		id := 1
		if len(a.log) > 0 {
			id = a.log[0].id + 1
		}

		a.insert(message{
			id: id, text: text, date: time.Now(), out: true, spans: messageSpans(entities),
			links: messageLinks(text, nil, nil), reply: messageReply{id: a.replyID, topic: a.replyTopic},
		}, true)
		a.clearComposer()
	} else {
		a.ask(out, request{
			kind: "send", chat: a.chats[a.selected], text: text, upload: a.staged,
			replyID: a.replyID, replyTopic: a.replyTopic,
		})
		a.composing = false
	}

	a.offset = 0
}

func (a *app) beginForward() {
	if a.demo || a.pending != "" || len(a.log) == 0 && len(a.selection) == 0 {
		return
	}

	if a.chats[a.selected].protected {
		a.status = "This chat restricts forwarding"
		return
	}

	a.forwardIDs = a.selectedMessages()
	a.forwardIndex, a.forwardQuery, a.forwardConfirm = 0, "", false
}

func (a *app) forwardChats() []int {
	var indexes []int

	for i, c := range a.chats {
		if strings.Contains(strings.ToLower(c.title), strings.ToLower(a.forwardQuery)) {
			indexes = append(indexes, i)
		}
	}

	return indexes
}

func (a *app) forwardInput(k key, out chan<- request) {
	indexes := a.forwardChats()
	if a.forwardConfirm {
		k = commandKey(k)
		switch {
		case k.name == "escape":
			a.forwardConfirm = false
		case k.name == "" && k.text == "y":
			a.ask(out, request{kind: "forward", chat: a.chats[a.selected], to: a.forwardTo, ids: a.forwardIDs})
			a.forwardIDs, a.forwardConfirm = nil, false
		}

		return
	}

	switch k.name {
	case "escape":
		a.forwardIDs = nil
	case "up", "previousChat":
		a.forwardIndex = max(0, a.forwardIndex-1)
	case "down", "nextChat":
		a.forwardIndex = min(a.forwardIndex+1, max(0, len(indexes)-1))
	case "enter":
		if len(indexes) > 0 {
			a.forwardTo = a.chats[indexes[a.forwardIndex]]
			a.forwardConfirm = true
		}
	case "erase":
		query := []rune(a.forwardQuery)
		if len(query) > 0 {
			a.forwardQuery = string(query[:len(query)-1])
		}

		a.forwardIndex = 0
	default:
		if text := strings.TrimSpace(k.text); text != "" || k.text == " " {
			a.forwardQuery = limitText(a.forwardQuery+k.text, 128)
			a.forwardIndex = 0
		}
	}
}

func (a *app) copyMessage() {
	if len(a.log) == 0 {
		return
	}

	msg := a.log[a.offset]
	if msg.protected || a.chats[a.selected].protected {
		a.status = "This message restricts copying"
	} else if !msg.service && (msg.text != "[Attachment]" || msg.attachment == nil) && msg.text != "" {
		a.copyText = msg.text
	} else {
		a.status = "No text to copy"
	}
}

func (a *app) authorLabel(msg message) string {
	if msg.authorName != "" && (a.chats[a.selected].group || !msg.authorDirectoryName) {
		return msg.authorName
	}

	if !a.chats[a.selected].group || msg.service {
		return ""
	}

	if msg.out {
		return "You"
	}

	if msg.author.id != 0 {
		if name := a.authors[msg.author]; name != "" {
			return name
		}

		if index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == msg.author }); index >= 0 {
			return a.chats[index].title
		}

		return fmt.Sprintf("Sender #%d", msg.author.id)
	}

	return ""
}

func (a *app) rememberAuthors(messages []message) {
	for _, msg := range messages {
		if msg.author.id != 0 && msg.authorName != "" && msg.authorDirectoryName {
			a.rememberAuthor(msg.author, msg.authorName)
		}
	}
}

func (a *app) rememberAuthor(author peerKey, name string) {
	if a.authors == nil || len(a.authors) >= 512 {
		a.authors = make(map[peerKey]string)
	}

	a.authors[author] = name
}

func (a *app) requestMissingAuthor(out chan<- request) {
	if !a.open || a.demo || a.authorLookup || a.prefetching || a.autoReadPending || a.pending != "" || len(a.log) == 0 {
		return
	}

	c := a.chats[a.selected]
	for _, msg := range a.log[a.offset:min(len(a.log), a.offset+12)] {
		var candidates [2]peerKey
		if c.group && !msg.out && msg.authorName == "" {
			candidates[0] = msg.author
		}

		if msg.forward != nil && msg.forward.name == "" {
			candidates[1] = msg.forward.author
		}

		for _, author := range candidates {
			if author.kind != 'u' || author.id == 0 {
				continue
			}

			if _, tried := a.authors[author]; tried {
				continue
			}

			if slices.ContainsFunc(a.chats, func(c chat) bool { return c.key == author }) {
				continue
			}

			select {
			case out <- request{kind: "author", chat: c, anchor: msg.id, author: author}:
				a.authorLookup = true
			default:
			}

			return
		}
	}
}
