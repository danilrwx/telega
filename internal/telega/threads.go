package telega

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gotd/td/tg"
)

type threadKind uint8

const (
	forumThread threadKind = iota + 1
	commentThread
)

type topicCursor struct {
	topic, message, date int
}

type topicPage struct {
	cursor, next topicCursor
	step         int
}

func (a *app) currentChat() chat {
	if a.thread != nil {
		return *a.thread
	}

	return a.chats[a.selected]
}

func (a *app) inConversation(e serverEvent) bool {
	return a.open && a.currentChat().key == e.chat && a.currentChat().threadID == e.threadID
}

func (c chat) includesMessage(msg message) bool {
	switch c.threadKind {
	case forumThread:
		if c.threadID == 1 {
			return msg.reply.topic == 0 && !msg.topicStart
		}

		return msg.id == c.threadID || msg.reply.topic == c.threadID
	case commentThread:
		return msg.id == c.threadID || msg.reply.top == c.threadID || msg.reply.id == c.threadID
	default:
		return true
	}
}

func loadTopics(ctx context.Context, api *tg.Client, req request) ([]chat, topicCursor, error) {
	cursor := req.topics.cursor

	result, err := api.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
		Peer: req.chat.input, OffsetTopic: cursor.topic, OffsetID: cursor.message, OffsetDate: cursor.date, Limit: historyLimit,
	})
	if err != nil {
		return nil, topicCursor{}, err
	}

	dates := make(map[int]int, len(result.Messages))
	for _, raw := range result.Messages {
		switch msg := raw.(type) {
		case *tg.Message:
			dates[msg.ID] = msg.Date
		case *tg.MessageService:
			dates[msg.ID] = msg.Date
		}
	}

	var (
		list []chat
		next topicCursor
	)

	for _, raw := range result.Topics {
		topic, ok := raw.(*tg.ForumTopic)
		if !ok {
			continue
		}

		if date := dates[topic.TopMessage]; date > 0 {
			next = topicCursor{topic: topic.ID, message: topic.TopMessage, date: date}
		}

		if topic.Hidden {
			continue
		}

		item := req.chat
		item.title = limitText(topic.Title, 128)
		item.threadKind, item.threadID, item.threadClosed = forumThread, topic.ID, topic.Closed
		item.unread = unreadState{count: topic.UnreadCount, mentions: topic.UnreadMentionsCount, inboxID: topic.ReadInboxMaxID, topID: topic.TopMessage}
		list = append(list, item)
	}

	if result.Count <= len(result.Topics) || next == cursor {
		next = topicCursor{}
	}

	return list, next, nil
}

func loadDiscussion(ctx context.Context, api *tg.Client, req request) (*chat, error) {
	result, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
		Peer: req.chat.input, MsgID: req.anchor,
	})
	if err != nil {
		return nil, err
	}

	if len(result.Messages) == 0 {
		return nil, errors.New("comments are unavailable for this post")
	}

	key, root, ok := unwrapMessage(result.Messages[len(result.Messages)-1])
	if !ok || key.kind != 'c' || key == req.chat.key {
		return nil, errors.New("could not identify the linked discussion")
	}

	discussion, available := peerChats(result.Users, result.Chats)[key]
	if !available {
		discussion = chat{key: key, group: true, input: &tg.InputPeerChannelFromMessage{
			Peer: req.chat.input, MsgID: req.anchor, ChannelID: key.id,
		}}
	}

	discussion.title = "Comments · " + req.chat.title
	discussion.threadKind, discussion.threadID = commentThread, root.id
	discussion.unread = unreadState{count: result.UnreadCount, inboxID: result.ReadInboxMaxID, topID: result.MaxID}

	return &discussion, nil
}

func (a *app) receiveTopics(e serverEvent) {
	a.pending = ""
	if e.topics.step > 0 && len(e.chats) == 0 {
		a.topicPage.next = topicCursor{}
		a.status = "End of topic list"

		return
	}

	switch {
	case e.topics.step > 0:
		a.topicPrevious = append(a.topicPrevious, a.topicPage.cursor)
	case e.topics.step < 0:
		a.topicPrevious = a.topicPrevious[:len(a.topicPrevious)-1]
	default:
		a.topicPrevious = nil
	}

	a.topicPage, a.topicEntries, a.topicIndex = e.topics, e.chats, 0
	if len(a.topicEntries) == 0 {
		a.status = "No visible topics"
	}
}

func (a *app) inputTopics(k key, out chan<- request) {
	if a.pending != "" {
		return
	}

	switch {
	case k.name == "escape" || k.name == "left" || k.name == "" && k.text == "h":
		a.topicEntries = nil
	case k.name == "up" || k.name == "" && k.text == "k":
		a.topicIndex = max(0, a.topicIndex-1)
	case k.name == "down" || k.name == "" && k.text == "j":
		a.topicIndex = min(len(a.topicEntries)-1, a.topicIndex+1)
	case k.name == "enter":
		a.openThread(out, a.topicEntries[a.topicIndex])
	case k.name == "" && k.text == "]" && a.topicPage.next.topic != 0:
		a.ask(out, request{kind: "topics", chat: a.currentChat(), topics: topicPage{cursor: a.topicPage.next, step: 1}})
	case k.name == "" && k.text == "[" && len(a.topicPrevious) > 0:
		previous := a.topicPrevious[len(a.topicPrevious)-1]
		a.ask(out, request{kind: "topics", chat: a.currentChat(), topics: topicPage{cursor: previous, step: -1}})
	}
}

func (a *app) openThread(out chan<- request, thread chat) {
	returnID := 0
	if len(a.log) > 0 {
		returnID = a.log[a.offset].id
	}

	a.resetChat()
	a.open, a.thread, a.threadReturnID, a.threadJumpBase = true, &thread, returnID, len(a.jumps)
	a.ask(out, request{kind: "history", chat: thread})
}

func (a *app) leaveThread(out chan<- request) {
	returnID := a.threadReturnID
	a.resetChat()
	a.open = true

	kind := "history"
	if returnID != 0 {
		kind = "threadParent"
	}

	a.ask(out, request{kind: kind, chat: a.currentChat(), anchor: returnID})
}

func (a *app) receiveThreadParent(e serverEvent) {
	a.pending = ""
	if !a.inConversation(e) {
		return
	}

	a.log, a.offset = e.log, 0
	if i := slices.IndexFunc(a.log, func(m message) bool { return m.id == e.anchor }); i >= 0 {
		a.offset = i
	}

	a.olderEnd = len(a.log) == 0
	a.newerTrimmed = len(a.log) > 0 && a.log[0].id < a.currentChat().unread.topID
	a.restoreDraft()
}

func threadTitle(c chat) string {
	if c.threadClosed {
		return c.title + " · closed"
	}

	return c.title
}

func topicLabel(topic chat) string {
	label := threadTitle(topic)
	if badge := topic.unread.badge(); badge != "" {
		label += fmt.Sprintf("  [%s]", badge)
	}

	return label
}
