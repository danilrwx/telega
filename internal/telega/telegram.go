package telega

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const chatLimit, historyLimit, inputLimit = 96, 48, 4096

type chat struct {
	group, protected, forum bool
	threadKind              threadKind
	threadID                int
	threadClosed            bool
	key                     peerKey
	title                   string
	input                   tg.InputPeerClass
	unread                  unreadState
}
type peerKey struct {
	kind byte
	id   int64
}

func keyFor(peer tg.PeerClass) peerKey {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return peerKey{'u', p.UserID}
	case *tg.PeerChat:
		return peerKey{'g', p.ChatID}
	case *tg.PeerChannel:
		return peerKey{'c', p.ChannelID}
	}

	return peerKey{}
}

type message struct {
	author                                  peerKey
	authorName                              string
	authorDirectoryName, service, protected bool
	reply                                   messageReply
	forward                                 *forwardSource
	links                                   []messageLink
	id                                      int
	text                                    string
	date                                    time.Time
	out                                     bool
	attachment                              *attachment
	spans                                   []textSpan
	mentioned, topicStart                   bool
	comments                                bool
	commentCount                            int
	reactions, myReaction                   string
	hasReactions                            bool
}

func unwrapMessage(value tg.MessageClass) (peerKey, message, bool) {
	switch msg := value.(type) {
	case *tg.Message:
		text := msg.Message
		if text == "" {
			text = "[Attachment]"
		}

		spans := messageSpans(msg.Entities)
		text = limitText(text, inputLimit)

		reactions, hasReactions := msg.GetReactions()

		return keyFor(msg.PeerID), message{
			reactions: reactionSummary(reactions), myReaction: chosenReaction(reactions), hasReactions: hasReactions,
			comments: msg.Replies.Comments, commentCount: msg.Replies.Replies,
			author: keyFor(msg.FromID), authorName: limitText(msg.PostAuthor, 80), protected: msg.Noforwards,
			id: msg.ID, text: text, date: time.Unix(int64(msg.Date), 0),
			out: msg.Out, reply: replyInfo(msg.ReplyTo), forward: forwardInfo(msg.FwdFrom), attachment: fileFromMedia(msg.Media), spans: spans, links: messageLinks(text, msg.Entities, spans), mentioned: msg.Mentioned,
		}, true
	case *tg.MessageService:
		reactions, hasReactions := msg.GetReactions()
		_, topicStart := msg.Action.(*tg.MessageActionTopicCreate)

		return keyFor(msg.PeerID), message{
			service: true, topicStart: topicStart, id: msg.ID, text: "[Service message]", date: time.Unix(int64(msg.Date), 0),
			out: msg.Out, reply: replyInfo(msg.ReplyTo), reactions: reactionSummary(reactions), myReaction: chosenReaction(reactions), hasReactions: hasReactions,
		}, true
	}

	return peerKey{}, message{}, false
}

type request struct {
	page                dialogPage
	topics              topicPage
	author              peerKey
	randomID            int64
	asFile              bool
	to                  chat
	replyID, replyTopic int
	kind                string
	chat                chat
	text                string
	anchor              int
	step                int
	attachment          *attachment
	ids                 []int
	revoke              bool
	upload              *uploadAttachment
}

type serverEvent struct {
	page                          dialogPage
	topics                        topicPage
	thread                        *chat
	threadID                      int
	author                        peerKey
	kind                          string
	chat                          peerKey
	chats                         []chat
	log                           []message
	ids                           []int
	text                          string
	hint                          string
	masked                        bool
	anchor, step                  int
	reactionAt                    int
	myReaction                    string
	source                        chat
	notice                        desktopNotice
	reactionAuthor, reactionEmoji string
	reactionActor                 peerKey
	reactionChoiceKnown           bool
	unread                        unreadState
	upload                        *uploadAttachment
}

func loadHistory(ctx context.Context, api *tg.Client, req request) ([]message, error) {
	query := &tg.MessagesGetHistoryRequest{Peer: req.chat.input, Limit: historyLimit, OffsetID: req.anchor}
	switch req.kind {
	case "newer":
		query.AddOffset, query.MinID = -historyLimit-1, req.anchor
	case "jump", "jumpBack", "unread", "threadParent":
		query.AddOffset = -historyLimit / 2
		if req.kind == "unread" {
			query.OffsetID = max(1, req.anchor)
		}
	}

	var (
		result tg.MessagesMessagesClass
		err    error
	)
	if req.chat.threadID != 0 {
		result, err = api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
			Peer: query.Peer, MsgID: req.chat.threadID, OffsetID: query.OffsetID,
			AddOffset: query.AddOffset, Limit: query.Limit, MinID: query.MinID,
		})
	} else {
		result, err = api.MessagesGetHistory(ctx, query)
	}

	if err != nil {
		return nil, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, nil
	}

	entities := tg.EntitiesFrom(data.GetUsers(), data.GetChats())
	out := make([]message, 0, historyLimit)

	for _, item := range data.GetMessages() {
		if _, msg, ok := unwrapMessage(item); ok &&
			(req.kind != "older" && req.kind != "prefetch" || msg.id < req.anchor) &&
			(req.kind != "newer" || msg.id > req.anchor) {
			out = append(out, resolveAuthor(msg, entities))
		}

		if len(out) == historyLimit {
			break
		}
	}

	return out, nil
}

func randomMessageID() int64 {
	var data [8]byte

	_, _ = rand.Read(data[:])

	return int64(binary.LittleEndian.Uint64(data[:]))
}

func sendMessage(ctx context.Context, api *tg.Client, req request) (message, error) {
	text, entities := codeBlocks(req.text)
	if req.randomID == 0 {
		req.randomID = randomMessageID()
	}

	api = tg.NewClient(sendInvoker{api.Invoker()})

	var (
		response tg.UpdatesClass
		err      error
	)

	if req.upload != nil {
		response, err = sendAttachment(ctx, api, req, text, entities)
	} else {
		response, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer: req.chat.input, Message: text, Entities: entities, RandomID: req.randomID, ReplyTo: req.inputReply(),
		})
	}

	if err != nil {
		return message{}, err
	}

	ack := message{text: text, date: time.Now(), out: true, reply: messageReply{id: req.replyID, topic: req.replyTopic, top: req.chat.threadID}, spans: messageSpans(entities), links: messageLinks(text, nil, nil)}
	if req.upload != nil && ack.text == "" {
		ack.text = "[Attachment]"
	}

	if short, ok := response.(*tg.UpdateShortSentMessage); ok {
		ack.id, ack.date = short.ID, time.Unix(int64(short.Date), 0)
		ack.attachment = fileFromMedia(short.Media)
	} else {
		var updates []tg.UpdateClass

		switch result := response.(type) {
		case *tg.Updates:
			updates = result.Updates
		case *tg.UpdatesCombined:
			updates = result.Updates
		case *tg.UpdateShort:
			updates = []tg.UpdateClass{result.Update}
		}

		for _, item := range updates {
			if id, ok := item.(*tg.UpdateMessageID); ok && id.RandomID == req.randomID {
				ack.id = id.ID
				break
			}
		}

		for _, item := range updates {
			if update, ok := item.(interface{ GetMessage() tg.MessageClass }); ok {
				if key, full, ok := unwrapMessage(update.GetMessage()); ok && key == req.chat.key && full.id == ack.id {
					ack = full
					break
				}
			}
		}
	}

	return ack, nil
}

func dispatchUpdates(ctx context.Context, dispatch tg.UpdateDispatcher, onNew tg.AnyMessageHandler, update tg.UpdatesClass) error {
	switch short := update.(type) {
	case *tg.UpdateShortMessage:
		return onNew(ctx, tg.Entities{}, &tg.Message{
			PeerID: &tg.PeerUser{UserID: short.UserID}, FromID: &tg.PeerUser{UserID: short.UserID}, ID: short.ID,
			Date: short.Date, Message: short.Message, Out: short.Out, Mentioned: short.Mentioned,
			Entities: short.Entities, ReplyTo: short.ReplyTo, FwdFrom: short.FwdFrom,
		})
	case *tg.UpdateShortChatMessage:
		return onNew(ctx, tg.Entities{}, &tg.Message{
			PeerID: &tg.PeerChat{ChatID: short.ChatID}, FromID: &tg.PeerUser{UserID: short.FromID}, ID: short.ID,
			Date: short.Date, Message: short.Message, Out: short.Out, Mentioned: short.Mentioned,
			Entities: short.Entities, ReplyTo: short.ReplyTo, FwdFrom: short.FwdFrom,
		})
	default:
		return dispatch.Handle(ctx, update)
	}
}

func telegramLoop(ctx context.Context, cfg config, qrMode bool, requests <-chan request, events chan<- serverEvent, checks <-chan noticeCheck) error {
	dispatch := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatch)

	var notifyChanged atomic.Bool

	forward := func(kind string) tg.AnyMessageHandler {
		return func(ctx context.Context, entities tg.Entities, value tg.MessageClass) error {
			if key, msg, ok := unwrapMessage(value); ok {
				msg = resolveAuthor(msg, entities)
				return emit(ctx, events, serverEvent{kind: kind, chat: key, log: []message{msg}, source: notificationSource(key, entities)})
			}

			return nil
		}
	}
	onNew := forward("new")
	dispatch.OnAnyNewMessage(onNew)
	dispatch.OnAnyEditMessage(forward("live"))
	dispatch.OnMessageReactions(reactionUpdates(events))
	dispatch.OnNotifySettings(func(context.Context, tg.Entities, *tg.UpdateNotifySettings) error {
		notifyChanged.Store(true)

		return nil
	})
	dispatch.OnReadHistoryInbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadHistoryInbox) error {
		if update.TopMsgID != 0 {
			return nil
		}

		return emit(ctx, events, serverEvent{
			kind: "read", chat: keyFor(update.Peer),
			unread: unreadState{count: update.StillUnreadCount, inboxID: update.MaxID},
		})
	})
	dispatch.OnReadChannelInbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadChannelInbox) error {
		return emit(ctx, events, serverEvent{
			kind: "read", chat: peerKey{'c', update.ChannelID},
			unread: unreadState{count: update.StillUnreadCount, inboxID: update.MaxID},
		})
	})
	dispatch.OnDialogUnreadMark(func(ctx context.Context, _ tg.Entities, update *tg.UpdateDialogUnreadMark) error {
		peer, ok := update.Peer.(*tg.DialogPeer)
		if !ok || update.SavedPeerID != nil {
			return nil
		}

		return emit(ctx, events, serverEvent{
			kind: "unreadMark", chat: keyFor(peer.Peer),
			unread: unreadState{marked: update.Unread},
		})
	})
	dispatch.OnAnyDeleteMessages(func(ctx context.Context, _ tg.Entities, channel int64, ids []int) error {
		return emit(ctx, events, serverEvent{kind: "delete", chat: peerKey{'c', channel}, ids: ids})
	})
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: cfg.Storage,
		UpdateHandler: telegram.UpdateHandlerFunc(func(ctx context.Context, update tg.UpdatesClass) error {
			return dispatchUpdates(ctx, dispatch, onNew, update)
		}),
		FilePoolSize: 1,
		Device:       telegram.DeviceConfig{DeviceModel: "telega terminal", AppVersion: strings.TrimPrefix(cfg.version, "v")},
	})

	return client.Run(ctx, func(ctx context.Context) error {
		if err := login(ctx, client, loginUI{requests, events}, qrMode, loggedIn, cfg); err != nil {
			return err
		}

		if err := emit(ctx, events, serverEvent{kind: "ready"}); err != nil {
			return err
		}

		api := client.API()

		notifyCtx, stopNotifications := context.WithCancel(ctx)
		defer stopNotifications()

		go checkNotifications(notifyCtx, api, checks, events, &notifyChanged)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case req := <-requests:
				value := serverEvent{kind: req.kind, chat: req.chat.key, threadID: req.chat.threadID, anchor: req.anchor, step: req.step}

				var err error

				switch req.kind {
				case "chats":
					value.page = req.page
					value.chats, value.page.next, err = loadDialogPage(ctx, api, req.page)
				case "archiveChat":
					err = moveDialog(ctx, api, req)
					value.page = req.page

				case "history", "older", "newer", "latest", "jump", "jumpBack", "threadParent":
					value.log, err = loadHistory(ctx, api, req)
				case "topics":
					value.topics = req.topics
					value.chats, value.topics.next, err = loadTopics(ctx, api, req)
				case "discussion":
					value.thread, err = loadDiscussion(ctx, api, req)
				case "author":
					value.author, value.text = req.author, lookupAuthor(ctx, api, req)
				case "prefetch":
					value.log, err = loadHistory(ctx, api, req)
					if err != nil {
						value.text, err = err.Error(), nil
					}
				case "unread":
					value.log, value.anchor, err = findUnread(ctx, api, req)
				case "searchHistory":
					value.log, err = searchHistory(ctx, api, req)
				case "react":
					var result tg.UpdatesClass

					result, err = sendReaction(ctx, api, req)
					if err == nil {
						value.text = req.text
						err = dispatch.Handle(ctx, result)
					}
				case "edit":
					value.log, err = editMessage(ctx, api, req)
				case "forward":
					value.chat = req.to.key

					value.ids, value.log, err = forwardMessages(ctx, api, req)
					if err != nil && len(value.ids) > 0 {
						value.hint, err = err.Error(), nil
					}

					value.text = req.to.title
				case "download":
					value.text, err = downloadAttachment(ctx, client, req)
				case "image", "file", "clipboard", "screenshot":
					value.kind = "imageReady"
					value.upload, err = stageAttachment(ctx, req, cfg.scratch)
				case "deleteMessages":
					value.kind = "deletedMessages"

					value.ids, err = deleteMessages(ctx, api, req)
					if err != nil {
						value.text, err = err.Error(), nil
					}
				case "markRead", "autoRead":
					value.unread, err = markRead(ctx, api, req)
					if err != nil && req.kind == "autoRead" {
						value.text, err = err.Error(), nil
					}
				case "send":
					var msg message

					msg, err = sendMessage(ctx, api, req)
					if tgerr.Is(err, "RANDOM_ID_DUPLICATE") {
						err = nil
					}

					if err == nil && msg.id != 0 {
						value.log = []message{msg}
					}
				}

				if err != nil {
					value = serverEvent{kind: "error", text: err.Error()}
				}

				if err := emit(ctx, events, value); err != nil {
					if value.upload != nil && value.upload.temporary {
						_ = os.Remove(value.upload.path)
					}

					return err
				}
			}
		}
	})
}
