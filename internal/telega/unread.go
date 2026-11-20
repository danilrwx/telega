package telega

import (
	"context"
	"fmt"
	"slices"

	"github.com/gotd/td/tg"
)

type unreadState struct {
	count, mentions, inboxID, topID int
	marked, pinned                  bool
}

func dialogUnread(d *tg.Dialog) unreadState {
	return unreadState{
		count: d.UnreadCount, mentions: d.UnreadMentionsCount, inboxID: d.ReadInboxMaxID,
		topID: d.TopMessage, marked: d.UnreadMark, pinned: d.Pinned,
	}
}

func (u unreadState) badge() string {
	if u.mentions > 0 {
		if u.count == 0 {
			return "@"
		}

		return unreadCount(u.count) + "@"
	}

	if u.count > 0 {
		return unreadCount(u.count)
	}

	if u.marked {
		return "•"
	}

	return ""
}

func unreadCount(n int) string {
	if n > 999 {
		return "999+"
	}

	return fmt.Sprint(n)
}

func (a *app) nextUnread(step int) {
	indexes := a.visibleChats()
	if len(indexes) == 0 {
		return
	}

	position := slices.Index(indexes, a.selected)
	if position < 0 {
		position = 0
	}

	for i := range indexes {
		next := indexes[(position+step*(i+1)+len(indexes))%len(indexes)]
		if a.chats[next].unread.badge() != "" {
			a.selected, a.status = next, ""
			return
		}
	}

	a.status = "No unread chats"
}

func (a *app) newChatMessage(e serverEvent) {
	i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == e.chat })
	if i < 0 {
		return
	}

	msg := e.log[0]

	state := &a.chats[i].unread
	if msg.id <= state.topID {
		return
	}

	state.topID = msg.id
	if !msg.out && msg.id > state.inboxID {
		state.count++
		if msg.mentioned {
			state.mentions++
		}
	}

	if state.pinned {
		return
	}

	first := slices.IndexFunc(a.chats, func(c chat) bool { return !c.unread.pinned })
	if first < 0 || i <= first {
		return
	}

	selected, moved := a.chats[a.selected].key, a.chats[i]
	copy(a.chats[first+1:i+1], a.chats[first:i])
	a.chats[first] = moved
	a.selected = slices.IndexFunc(a.chats, func(c chat) bool { return c.key == selected })
}

func markRead(ctx context.Context, api *tg.Client, req request) (unreadState, error) {
	peer := &tg.InputDialogPeer{Peer: req.chat.input}
	switch input := req.chat.input.(type) {
	case *tg.InputPeerChannel:
		ok, err := api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: &tg.InputChannel{ChannelID: input.ChannelID, AccessHash: input.AccessHash},
			MaxID:   req.anchor,
		})
		if err != nil {
			return unreadState{}, err
		}

		if !ok {
			return unreadState{}, fmt.Errorf("marking the channel as read was declined")
		}
	default:
		if _, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: input, MaxID: req.anchor}); err != nil {
			return unreadState{}, err
		}
	}

	if req.chat.unread.marked {
		if _, err := api.MessagesMarkDialogUnread(ctx, &tg.MessagesMarkDialogUnreadRequest{Peer: peer}); err != nil {
			return unreadState{}, err
		}
	}

	result, err := api.MessagesGetPeerDialogs(ctx, []tg.InputDialogPeerClass{peer})
	if err != nil {
		return unreadState{}, err
	}

	for _, dialog := range result.Dialogs {
		if d, ok := dialog.(*tg.Dialog); ok && keyFor(d.Peer) == req.chat.key {
			return dialogUnread(d), nil
		}
	}

	return unreadState{}, fmt.Errorf("missing read status in the Telegram response")
}

func (a *app) markVisibleRead(out chan<- request) {
	if !a.open || a.demo || !a.focused || a.offset != 0 || a.newerTrimmed || len(a.log) == 0 ||
		a.pending != "" || a.autoReadPending || a.prefetching || a.authorLookup || a.searching || a.imagePath ||
		a.serverResults != nil || a.forwardIDs != nil || a.confirmDelete != nil || a.linkChoices != nil {
		return
	}

	current := a.chats[a.selected]
	unread := current.unread

	anchor := a.log[0].id
	if unread.count == 0 && !unread.marked || anchor < unread.topID || anchor <= unread.inboxID && !unread.marked {
		return
	}

	target := chatPosition{chat: current.key, id: anchor}
	if target == a.lastAutoRead {
		return
	}

	select {
	case out <- request{kind: "autoRead", chat: current, anchor: anchor}:
		a.autoReadPending, a.lastAutoRead = true, target
	default:
	}
}
