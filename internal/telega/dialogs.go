package telega

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gotd/td/tg"
)

type dialogCursor struct {
	input    tg.InputPeerClass
	id, date int
}

type dialogPage struct {
	folder, step int
	cursor, next dialogCursor
}

func peerChats(users []tg.UserClass, groups []tg.ChatClass) map[peerKey]chat {
	peers := make(map[peerKey]chat)

	for _, item := range users {
		u, ok := item.(*tg.User)
		if !ok {
			continue
		}

		key := peerKey{'u', u.ID}

		title := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if u.Self {
			peers[key] = chat{key: key, title: "Saved Messages", input: &tg.InputPeerSelf{}}
		} else if u.AccessHash != 0 && !u.Min {
			peers[key] = chat{key: key, title: limitText(title, 128), input: &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}}
		}
	}

	for _, item := range groups {
		switch c := item.(type) {
		case *tg.Chat:
			key := peerKey{'g', c.ID}
			peers[key] = chat{key: key, group: true, protected: c.Noforwards, title: limitText(c.Title, 128), input: &tg.InputPeerChat{ChatID: c.ID}}
		case *tg.Channel:
			if c.AccessHash != 0 && !c.Min {
				key := peerKey{'c', c.ID}
				peers[key] = chat{key: key, group: c.Megagroup, forum: c.Forum, protected: c.Noforwards, title: limitText(c.Title, 128), input: &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}}
			}
		}
	}

	return peers
}

func loadDialogPage(ctx context.Context, api *tg.Client, page dialogPage) ([]chat, dialogCursor, error) {
	query := &tg.MessagesGetDialogsRequest{Limit: chatLimit, OffsetPeer: &tg.InputPeerEmpty{}}
	query.SetFolderID(page.folder)

	if page.cursor.input != nil {
		query.OffsetPeer, query.OffsetID, query.OffsetDate = page.cursor.input, page.cursor.id, page.cursor.date
		query.ExcludePinned = true
	}

	result, err := api.MessagesGetDialogs(ctx, query)
	if err != nil {
		return nil, dialogCursor{}, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, dialogCursor{}, nil
	}

	peers := peerChats(data.GetUsers(), data.GetChats())

	dates := make(map[chatPosition]int, len(data.GetMessages()))
	for _, raw := range data.GetMessages() {
		switch m := raw.(type) {
		case *tg.Message:
			dates[chatPosition{chat: keyFor(m.PeerID), id: m.ID}] = m.Date
		case *tg.MessageService:
			dates[chatPosition{chat: keyFor(m.PeerID), id: m.ID}] = m.Date
		}
	}

	chats := make([]chat, 0, min(len(data.GetDialogs()), chatLimit))

	var next dialogCursor

	for _, item := range data.GetDialogs() {
		d, ok := item.(*tg.Dialog)
		if !ok {
			continue
		}

		c, found := peers[keyFor(d.GetPeer())]
		if !found {
			continue
		}

		if c.title == "" {
			c.title = "Deleted account"
		}

		c.unread = dialogUnread(d)
		chats = append(chats, c)

		if date := dates[chatPosition{chat: c.key, id: d.TopMessage}]; !d.Pinned && date > 0 {
			next = dialogCursor{input: c.input, date: date, id: d.TopMessage}
		}
	}

	if _, sliced := result.(*tg.MessagesDialogsSlice); !sliced || len(data.GetDialogs()) < chatLimit {
		next = dialogCursor{}
	}

	return chats, next, nil
}

func (a *app) receiveDialogs(out chan<- request, e serverEvent) {
	a.pending = ""
	if e.page.step > 0 && len(e.chats) == 0 {
		a.dialogPage.next = dialogCursor{}
		a.status = "End of chat list"

		return
	}

	switch {
	case e.page.step > 0:
		a.dialogPrevious = append(a.dialogPrevious, a.dialogPage.cursor)
	case e.page.step < 0:
		a.dialogPrevious = a.dialogPrevious[:len(a.dialogPrevious)-1]
	default:
		a.dialogPrevious = nil
	}

	selected := 0
	if len(a.chats) > 0 && a.dialogPage.folder == e.page.folder && e.page.step == 0 {
		id := a.chats[a.selected].key
		selected = max(0, slices.IndexFunc(e.chats, func(c chat) bool { return c.key == id }))
	}

	a.chats, a.selected, a.dialogPage = e.chats, selected, e.page

	a.status = e.text
	if a.resumeChat.id != 0 {
		i := slices.IndexFunc(a.chats, func(c chat) bool { return c.key == a.resumeChat })

		a.resumeChat = peerKey{}
		if i >= 0 {
			if a.resumeThread != nil {
				a.selected, a.open, a.thread = i, true, a.resumeThread
				a.threadReturnID, a.threadJumpBase = a.resumeReturnID, len(a.jumps)
				a.ask(out, request{kind: "history", chat: a.currentChat()})
			} else {
				a.openChat(out, i)
			}
		}

		a.resumeThread = nil
	}
}

func (a *app) pageDialogs(out chan<- request, step int) {
	if a.pending != "" || a.demo {
		return
	}

	page := dialogPage{folder: a.dialogPage.folder, step: step}
	if step > 0 {
		if a.dialogPage.next.input == nil {
			a.status = "End of chat list"
			return
		}

		page.cursor = a.dialogPage.next
	} else {
		if len(a.dialogPrevious) == 0 {
			return
		}

		page.cursor = a.dialogPrevious[len(a.dialogPrevious)-1]
	}

	a.ask(out, request{kind: "chats", page: page})
}

func moveDialog(ctx context.Context, api *tg.Client, req request) error {
	_, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: req.chat.input, FolderID: 1 - req.page.folder}})
	return err
}

func (a *app) dialogTitle() string {
	title := "Chats"
	if a.dialogPage.folder == 1 {
		title = "Archive"
	}

	if a.unreadOnly {
		title += " · Unread"
	}

	if n := len(a.dialogPrevious); n > 0 {
		title += fmt.Sprintf(" · page %d", n+1)
	}

	return title
}
