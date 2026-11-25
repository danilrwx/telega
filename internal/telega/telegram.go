package telega

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const chatLimit, historyLimit, inputLimit = 96, 48, 4096

type chat struct {
	folder                  int
	group, protected, forum bool
	threadKind              threadKind
	threadID                int
	threadClosed            bool
	key                     peerKey
	title, username         string
	input                   tg.InputPeerClass
	unread                  unreadState
	readOutboxID            int
	presence                tg.UserStatusClass
	avatar                  *attachment
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
	poll                                    *messagePoll
	author, avatarPeer                      peerKey
	avatar                                  *attachment
	authorName                              string
	authorDirectoryName, service, protected bool
	reply                                   messageReply
	forward                                 *forwardSource
	links                                   []messageLink
	id                                      int
	text                                    string
	date                                    time.Time
	out, edited                             bool
	attachment                              *attachment
	spans                                   []textSpan
	mentioned, topicStart                   bool
	comments, pinned                        bool
	commentCount                            int
	reactions, myReaction                   string
	chosenReactions, messageReactions       []reactionChoice
	hasReactions                            bool
	recentReactions                         []tg.MessagePeerReaction
}

func unwrapMessage(value tg.MessageClass) (peerKey, message, bool) {
	switch msg := value.(type) {
	case *tg.Message:
		poll := pollFromMedia(msg.Media)

		text := msg.Message
		if poll != nil && text == "" {
			text = poll.preview()
		} else if text == "" {
			text = "[Attachment]"
		}

		spans := messageSpans(msg.Entities)
		text = limitText(text, inputLimit)

		reactions, hasReactions := msg.GetReactions()
		peer, author := keyFor(msg.PeerID), keyFor(msg.FromID)
		avatarPeer := author

		if avatarPeer.id == 0 && (!msg.Out || msg.Post) {
			avatarPeer = peer
		}

		return peer, message{
			reactions: reactionSummary(reactions), myReaction: chosenReaction(reactions), hasReactions: hasReactions, recentReactions: reactions.RecentReactions,
			chosenReactions: reactionChoices(reactions, true), messageReactions: reactionChoices(reactions, false),
			poll: poll, pinned: msg.Pinned, comments: msg.Replies.Comments, commentCount: msg.Replies.Replies,
			author: author, avatarPeer: avatarPeer, authorName: limitText(msg.PostAuthor, 80), protected: msg.Noforwards,
			id: msg.ID, text: text, date: time.Unix(int64(msg.Date), 0), edited: msg.EditDate > 0 && !msg.EditHide,
			out: msg.Out, reply: replyInfo(msg.ReplyTo), forward: forwardInfo(msg.FwdFrom), attachment: fileFromMedia(msg.Media), spans: spans, links: messageLinks(text, msg.Entities, spans), mentioned: msg.Mentioned,
		}, true
	case *tg.MessageService:
		reactions, hasReactions := msg.GetReactions()
		_, topicStart := msg.Action.(*tg.MessageActionTopicCreate)

		return keyFor(msg.PeerID), message{
			service: true, topicStart: topicStart, id: msg.ID, text: "[Service message]", date: time.Unix(int64(msg.Date), 0),
			out: msg.Out, reply: replyInfo(msg.ReplyTo), reactions: reactionSummary(reactions), myReaction: chosenReaction(reactions), hasReactions: hasReactions, recentReactions: reactions.RecentReactions,
			chosenReactions: reactionChoices(reactions, true), messageReactions: reactionChoices(reactions, false),
		}, true
	}

	return peerKey{}, message{}, false
}

type request struct {
	chatLink            *chatLinkTarget
	poll                *pollRequest
	pins                *pinDialog
	pinState            bool
	historyID, searchID uint64
	historyCtx          context.Context
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
	reactions           []reactionChoice
	reactionDocument    int64
	reactionAuthors     *tui.ReactionAuthors
	recentReactions     []tg.MessagePeerReaction
	textReactions       bool
	messageSearch       *tui.MessageSearch
	searchCursor        messageSearchCursor
	browseAttachments   bool
	searchFilter        int
	transcript          *voiceTranscript
}

type serverEvent struct {
	chatLink                          *chatLinkResult
	dialogFilters                     []dialogFilter
	poll                              *pollUpdate
	pollRequest                       *pollRequest
	pins                              *pinDialog
	pinState                          bool
	historyID, searchID               uint64
	accountID                         int64
	avatar                            *attachment
	presence                          tg.UserStatusClass
	page                              dialogPage
	topics                            topicPage
	thread                            *chat
	threadID                          int
	author                            peerKey
	kind                              string
	chat                              peerKey
	chats                             []chat
	log                               []message
	ids                               []int
	text                              string
	hint                              string
	masked                            bool
	anchor, step                      int
	reactionAt                        int
	myReaction                        string
	source                            chat
	notice                            desktopNotice
	reactionAuthor, reactionEmoji     string
	reactionActor                     peerKey
	reactionAuthors                   *tui.ReactionAuthors
	recentReactions                   []tg.MessagePeerReaction
	reactionAuthorRows                []string
	reactionChoiceKnown               bool
	playVoice                         bool
	chosenReactions, messageReactions []reactionChoice
	inlineImage                       *tui.InlineImage
	unread                            unreadState
	upload                            *uploadAttachment
	messageSearch                     *tui.MessageSearch
	searchCursor                      messageSearchCursor
	messageHits                       []messageHit
	transcript                        *voiceTranscript
	transcriptID                      int64
	transcriptPending                 bool
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
		peer := &tg.PeerUser{UserID: short.UserID}

		var from tg.PeerClass

		if !short.Out {
			from = peer
		}

		return onNew(ctx, tg.Entities{}, &tg.Message{
			PeerID: peer, FromID: from, ID: short.ID,
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
	dispatch.OnTranscribedAudio(func(ctx context.Context, _ tg.Entities, update *tg.UpdateTranscribedAudio) error {
		return emit(ctx, events, serverEvent{
			kind: "voiceTranscript", chat: keyFor(update.Peer), anchor: update.MsgID, text: update.Text,
			transcriptID: update.TranscriptionID, transcriptPending: update.Pending,
		})
	})
	dispatch.OnUserStatus(func(ctx context.Context, _ tg.Entities, update *tg.UpdateUserStatus) error {
		return emit(ctx, events, serverEvent{
			kind: "presence", chat: peerKey{'u', update.UserID}, presence: update.Status,
		})
	})
	dispatch.OnMessagePoll(func(ctx context.Context, _ tg.Entities, update *tg.UpdateMessagePoll) error {
		return emit(ctx, events, pollEvent(update))
	})
	registerPinUpdates(dispatch, events)
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
	dispatch.OnReadHistoryOutbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadHistoryOutbox) error {
		return emit(ctx, events, serverEvent{kind: "readOutbox", chat: keyFor(update.Peer), anchor: update.MaxID})
	})
	dispatch.OnReadChannelOutbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadChannelOutbox) error {
		return emit(ctx, events, serverEvent{kind: "readOutbox", chat: peerKey{'c', update.ChannelID}, anchor: update.MaxID})
	})
	dispatch.OnReadChannelDiscussionOutbox(func(ctx context.Context, _ tg.Entities, update *tg.UpdateReadChannelDiscussionOutbox) error {
		return emit(ctx, events, serverEvent{
			kind: "readOutbox", chat: peerKey{'c', update.ChannelID}, threadID: update.TopMsgID, anchor: update.ReadMaxID,
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
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}

		self := status.User
		if !status.Authorized {
			if err := login(ctx, client, loginUI{requests, events}, qrMode, loggedIn, cfg); err != nil {
				return err
			}

			self, _ = client.Self(ctx)
		}

		accountID, _, _ := client.SelfID()
		if self != nil {
			accountID = self.ID
		}

		if err := emit(ctx, events, serverEvent{kind: "ready", accountID: accountID, text: userDisplayName(self), avatar: userAvatar(self)}); err != nil {
			return err
		}

		api := client.API()
		reactionLimit, premium := 0, false

		notifyCtx, stopNotifications := context.WithCancel(ctx)
		defer stopNotifications()

		go checkNotifications(notifyCtx, api, checks, events, &notifyChanged)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case req := <-requests:
				value := serverEvent{kind: req.kind, chat: req.chat.key, threadID: req.chat.threadID, anchor: req.anchor, step: req.step, historyID: req.historyID}

				var err error

				switch req.kind {
				case "chats":
					if req.page.step == 0 {
						go func() {
							filters, loadErr := loadDialogFilters(ctx, api)
							if loadErr == nil {
								_ = emit(ctx, events, serverEvent{kind: "dialogFilters", dialogFilters: filters})
							}
						}()
					}

					value.page = req.page
					value.chats, value.page.next, err = loadDialogPage(ctx, api, req.page)
				case "chatLinkResolve", "chatLinkJoin":
					value = runChatLink(ctx, api, req)
				case "pollResults", "pollVote":
					go func() { _ = emit(ctx, events, runPollRequest(ctx, api, req)) }()

					continue
				case "listPins":
					go func() { _ = emit(ctx, events, runPinsRequest(ctx, api, req)) }()

					continue
				case "setPin":
					value = runPinsRequest(ctx, api, req)
				case "toggleChatPin":
					err = toggleChatPin(ctx, api, req)

					value.text = "Chat unpinned"
					if !req.chat.unread.pinned {
						value.step, value.text = 1, "Chat pinned"
					}
				case "toggleChatMute":
					value.text, err = toggleChatMute(ctx, api, req.chat, time.Now())
					if err == nil {
						notifyChanged.Store(true)
					}
				case "archiveChat":
					err = moveDialog(ctx, api, req)
					value.page = req.page

				case "history", "older", "newer", "latest", "jump", "jumpBack", "threadParent":
					if req.historyCtx == nil {
						value.log, err = loadHistory(ctx, api, req)
						break
					}

					if req.historyCtx.Err() != nil {
						continue
					}

					loadCtx, cancel := context.WithCancel(ctx)
					stop := context.AfterFunc(req.historyCtx, cancel)
					value.log, err = loadHistory(loadCtx, api, req)

					stop()
					cancel()

					if req.historyCtx.Err() != nil {
						continue
					}
				case "topics":
					value.topics = req.topics
					value.chats, value.topics.next, err = loadTopics(ctx, api, req)
				case "discussion":
					value.thread, err = loadDiscussion(ctx, api, req)
				case "author":
					value.author = req.author
					value.text, value.avatar = lookupAuthor(ctx, api, req)
				case "prefetch":
					value.log, err = loadHistory(ctx, api, req)
					if err != nil {
						value.text, err = err.Error(), nil
					}
				case "unread":
					value.log, value.anchor, err = findUnread(ctx, api, req)
				case "messageSearch":
					go func() {
						hits, next, searchErr := searchMessages(ctx, api, req)

						event := serverEvent{
							kind: req.kind, searchID: req.searchID, messageSearch: req.messageSearch,
							anchor: req.searchCursor.id, messageHits: hits, searchCursor: next,
						}
						if searchErr != nil {
							event.hint = searchErr.Error()
						}

						_ = emit(ctx, events, event)
					}()

					continue
				case "reactionAuthors":
					go func(req request, value serverEvent) {
						value.reactionAuthors = req.reactionAuthors
						value.reactionAuthorRows, value.text = fetchReactionAuthors(ctx, req, api.MessagesGetMessageReactionsList, api.MessagesGetCustomEmojiDocuments, api.MessagesGetMessages)
						_ = emit(ctx, events, value)
					}(req, value)

					continue
				case "voiceTranscript":
					go func() {
						_ = emit(ctx, events, fetchVoiceTranscript(ctx, api, req, value))
					}()

					continue
				case "react", "reactionCatalog":
					if reactionLimit == 0 {
						if self, selfErr := client.Self(ctx); selfErr == nil {
							premium = self.Premium
						}

						reactionLimit = telegramReactionLimit(ctx, api, premium)
					}

					if req.kind == "reactionCatalog" {
						value.messageReactions, err = loadReactionCatalog(ctx, api, req.chat.input, premium)
						break
					}

					if excess := len(req.reactions) - reactionLimit; excess > 0 {
						req.reactions = req.reactions[excess:]
					}

					var result tg.UpdatesClass

					result, err = sendReaction(ctx, api, req)
					if tgerr.Is(err, "REACTION_INVALID") {
						err = errors.New("reaction rejected by Telegram for this message")
					}

					if err == nil {
						err = dispatch.Handle(ctx, result)
					}

					if err == nil {
						value.chosenReactions = req.reactions
					} else {
						value.hint, err = err.Error(), nil
					}
				case "edit":
					value.log, err = editMessage(ctx, api, req)
				case "chatDirectory":
					go streamChatDirectory(ctx, api, events)
					continue
				case "directorySearch":
					go func() {
						chats, searchErr := searchTelegramContacts(ctx, api, req.text)

						event := serverEvent{kind: req.kind, searchID: req.searchID, chats: chats}
						if searchErr != nil {
							event.hint = "Telegram search unavailable"
						}

						_ = emit(ctx, events, event)
					}()

					continue
				case "forward":
					value.chat = req.to.key

					value.ids, value.log, err = forwardMessages(ctx, api, req)
					if err != nil && len(value.ids) > 0 {
						value.hint, err = err.Error(), nil
					}

					value.text = req.to.title
				case "download":
					value.text, err = downloadAttachment(ctx, client, req)
					value.playVoice = req.attachment.kind == "Voice"
				case "reactionImage":
					value.text, err = downloadReactionImage(ctx, client, accountID, req.reactionDocument)
				case "inlineImage":
					go func(req request, value serverEvent) {
						value.text = req.text
						value.inlineImage = fetchInlineImage(ctx, client, accountID, req)
						_ = emit(ctx, events, value)
					}(req, value)

					continue
				case "clipboard", "screenshot":
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
					value = serverEvent{kind: "error", text: err.Error(), historyID: req.historyID}
				}

				if err := emit(ctx, events, value); err != nil {
					if value.upload != nil {
						value.upload.discard()
					}

					return err
				}
			}
		}
	})
}
