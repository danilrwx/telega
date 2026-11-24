package telega

import (
	"context"
	"slices"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

type messageHit struct {
	chat chat
	msg  message
}

type messageSearchCursor struct {
	id, rate int
	peer     tg.InputPeerClass
}

var mediaSearchNames = []string{"Messages", "Photos", "Files", "Links", "Voice messages"}

const (
	searchPhotoVideo = 5
	searchMusic      = 6
)

func telegramSearchFilter(index int) tg.MessagesFilterClass {
	switch index {
	case 1:
		return &tg.InputMessagesFilterPhotos{}
	case 2:
		return &tg.InputMessagesFilterDocument{}
	case 3:
		return &tg.InputMessagesFilterURL{}
	case 4:
		return &tg.InputMessagesFilterVoice{}
	case searchPhotoVideo:
		return &tg.InputMessagesFilterPhotoVideo{}
	case searchMusic:
		return &tg.InputMessagesFilterMusic{}
	default:
		return &tg.InputMessagesFilterEmpty{}
	}
}

func (a *app) beginMessageSearch(global bool) {
	if !a.ready || a.pending != "" || a.demo {
		return
	}

	a.messageSearchScope = chat{}
	if a.open {
		a.messageSearchScope = a.currentChat()
	}

	a.messageSearch = &tui.MessageSearch{Global: global || !a.open, Scope: a.messageSearchScope.title, FilterNames: mediaSearchNames}
	a.messageSearchHits = nil
	a.messageSearchCursor = messageSearchCursor{}
}

func (a *app) closeMessageSearch() {
	a.messageSearch, a.messageSearchHits = nil, nil
	a.messageSearchScope, a.messageSearchCursor = chat{}, messageSearchCursor{}
	a.attachmentGallery = nil
}

func (a *app) messageSearchInput(k key, out chan<- request) {
	dialog := a.messageSearch
	switch action := dialog.Key(k.name, k.text); action {
	case "close":
		a.closeMessageSearch()
	case "open":
		hit := a.messageSearchHits[dialog.Selected]
		scoped := !dialog.Global

		a.closeMessageSearch()
		a.jumpToSearchHit(out, hit, scoped)
	case "search", "more":
		cursor := messageSearchCursor{}
		if action == "more" {
			cursor = a.messageSearchCursor
		} else {
			a.messageSearchHits = nil
			a.messageSearchCursor = messageSearchCursor{}
		}

		scope := a.messageSearchScope
		if dialog.Global {
			scope = chat{}
		}

		out <- request{
			kind: "messageSearch", chat: scope, text: dialog.Query(), messageSearch: dialog,
			searchID: dialog.Revision, searchCursor: cursor, searchFilter: dialog.FilterIndex,
		}
	}
}

func (a *app) receiveMessageSearch(e serverEvent) {
	dialog := a.messageSearch
	if dialog == nil || dialog != e.messageSearch || dialog.Revision != e.searchID {
		return
	}

	appendPage := e.anchor != 0
	if e.hint == "" {
		if !appendPage {
			a.messageSearchHits = nil
		}

		a.messageSearchHits = append(a.messageSearchHits, e.messageHits...)
		a.messageSearchCursor = e.searchCursor
	}

	more := a.messageSearchCursor.id != 0
	if a.attachmentGallery != nil {
		dialog.Busy, dialog.More, dialog.Status = false, more, e.hint

		return
	}

	labels := make([]string, 0, len(e.messageHits))
	for _, hit := range e.messageHits {
		label := hit.msg.date.Local().Format("02 Jan 15:04") + " · " + messageExcerpt(hit.msg, 0)
		if dialog.Global {
			label = hit.chat.title + " · " + label
		}

		labels = append(labels, label)
	}

	dialog.SetResults(labels, more, appendPage, e.hint)
}

func (a *app) jumpToSearchHit(out chan<- request, hit messageHit, scoped bool) {
	target := chatPosition{chat: hit.chat.key, id: hit.msg.id}
	if scoped {
		a.requestJump(out, "jump", target)
		return
	}

	index := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == hit.chat.key })
	if index < 0 {
		a.chats = append(a.chats, hit.chat)
		index = len(a.chats) - 1
	}

	if a.open && a.thread == nil && (!hit.chat.forum || hit.msg.reply.topic == 0) {
		a.requestJump(out, "jump", target)
		return
	}

	var origin chatPosition
	if a.open {
		origin = a.position()
		a.parkDraft()
	}

	a.resetChat()
	a.open, a.selected = true, index

	scope := a.currentChat()
	if scope.forum && hit.msg.reply.topic != 0 {
		scope.threadKind, scope.threadID = forumThread, hit.msg.reply.topic
		a.thread = &scope
		a.threadJumpBase = len(a.jumps)
	}

	a.jumpOrigin = origin
	a.ask(out, request{kind: "jump", chat: scope, anchor: hit.msg.id})
}

func searchMessages(ctx context.Context, api *tg.Client, req request) ([]messageHit, messageSearchCursor, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	if req.chat.input != nil {
		req.anchor = req.searchCursor.id
		messages, err := searchHistory(ctx, api, req)

		hits := make([]messageHit, 0, len(messages))
		for _, msg := range messages {
			hits = append(hits, messageHit{req.chat, msg})
		}

		var next messageSearchCursor
		if len(messages) == historyLimit || req.browseAttachments && len(messages) > 0 {
			next.id = messages[len(messages)-1].id
		}

		return hits, next, err
	}

	cursor := req.searchCursor
	if cursor.peer == nil {
		cursor.peer = &tg.InputPeerEmpty{}
	}

	result, err := api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{
		Q: req.text, Filter: telegramSearchFilter(req.searchFilter), OffsetID: cursor.id, OffsetPeer: cursor.peer, OffsetRate: cursor.rate, Limit: historyLimit,
	})
	if err != nil {
		return nil, messageSearchCursor{}, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, messageSearchCursor{}, nil
	}

	peers := peerChats(data.GetUsers(), data.GetChats())
	entities := tg.EntitiesFrom(data.GetUsers(), data.GetChats())

	var (
		hits []messageHit
		last messageSearchCursor
	)

	for _, item := range data.GetMessages() {
		key, msg, ok := unwrapMessage(item)
		if c, found := peers[key]; ok && found {
			hits = append(hits, messageHit{c, resolveAuthor(msg, entities)})
			last = messageSearchCursor{id: msg.id, rate: int(msg.date.Unix()), peer: c.input}
		}
	}

	if page, ok := result.(*tg.MessagesMessagesSlice); ok && last.id != 0 {
		if next, present := page.GetNextRate(); present {
			last.rate = next
		} else if len(data.GetMessages()) < historyLimit {
			last = messageSearchCursor{}
		}
	} else {
		last = messageSearchCursor{}
	}

	return hits, last, nil
}
