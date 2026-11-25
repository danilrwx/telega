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
	key      peerKey
	id, date int
	custom   *folderCursor
}

type dialogPage struct {
	folder, step int
	cursor, next dialogCursor
	filter       *dialogFilter
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
			peers[key] = chat{key: key, title: limitText(title, 128), username: u.Username, input: &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, presence: u.Status, avatar: userAvatar(u)}
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
				peers[key] = chat{key: key, group: c.Megagroup, forum: c.Forum, protected: c.Noforwards, title: limitText(c.Title, 128), username: c.Username, input: &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}}
			}
		}
	}

	return peers
}

type dialogEntry struct {
	chat
	category        folderCategory
	date, muteUntil int
	muteSet         bool
}

func parseDialogEntries(dialogs []tg.DialogClass, users []tg.UserClass, groups []tg.ChatClass, msgs []tg.MessageClass, nativeFolder int) []dialogEntry {
	peers := peerChats(users, groups)
	categories := make(map[peerKey]folderCategory, len(users))
	for _, item := range users {
		if u, ok := item.(*tg.User); ok {
			kind := folderNonContacts
			switch {
			case u.Bot:
				kind = folderBots
			case u.Contact || u.Self:
				kind = folderContacts
			}
			categories[peerKey{'u', u.ID}] = kind
		}
	}

	dates := make(map[chatPosition]int, len(msgs))
	for _, raw := range msgs {
		switch m := raw.(type) {
		case *tg.Message:
			dates[chatPosition{chat: keyFor(m.PeerID), id: m.ID}] = m.Date
		case *tg.MessageService:
			dates[chatPosition{chat: keyFor(m.PeerID), id: m.ID}] = m.Date
		}
	}

	entries := make([]dialogEntry, 0, len(dialogs))
	for _, item := range dialogs {
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

		c.folder = nativeFolder
		if nativeFolder < 0 {
			c.folder = d.FolderID
		}

		c.unread, c.readOutboxID = dialogUnread(d), d.ReadOutboxMaxID
		category := categories[c.key]
		if c.group {
			category = folderGroups
		} else if c.key.kind == 'c' {
			category = folderBroadcasts
		}

		until, muteSet := d.NotifySettings.GetMuteUntil()
		entries = append(entries, dialogEntry{chat: c, category: category,
			date: dates[chatPosition{chat: c.key, id: d.TopMessage}], muteUntil: until, muteSet: muteSet})
	}

	return entries
}

func loadNativeDialogs(ctx context.Context, api *tg.Client, folder int, cursor dialogCursor, excludePinned, keepShort bool) ([]dialogEntry, dialogCursor, bool, error) {
	query := &tg.MessagesGetDialogsRequest{Limit: chatLimit, OffsetPeer: &tg.InputPeerEmpty{}, ExcludePinned: excludePinned}
	query.SetFolderID(folder)
	if cursor.input != nil {
		query.OffsetPeer, query.OffsetID, query.OffsetDate = cursor.input, cursor.id, cursor.date
		query.ExcludePinned = true
	}

	result, err := api.MessagesGetDialogs(ctx, query)
	if err != nil {
		return nil, dialogCursor{}, false, err
	}

	data, ok := result.AsModified()
	if !ok {
		return nil, dialogCursor{}, false, nil
	}

	entries := parseDialogEntries(data.GetDialogs(), data.GetUsers(), data.GetChats(), data.GetMessages(), folder)
	var next dialogCursor
	for _, c := range entries {
		if !c.unread.pinned && c.date > 0 {
			next = dialogCursor{input: c.input, key: c.key, date: c.date, id: c.unread.topID}
		}
	}

	// Physical lists historically stop at a short page. Custom lists can
	// continue through a short slice when Telegram reports more dialogs.
	slice, sliced := result.(*tg.MessagesDialogsSlice)
	full := len(data.GetDialogs()) >= chatLimit
	more := sliced && (full || keepShort && slice.Count > len(data.GetDialogs()))
	if !more || sameDialogOffset(cursor, next) {
		next = dialogCursor{}
	}

	return entries, next, more, nil
}

func sameDialogOffset(a, b dialogCursor) bool {
	return a.key == b.key && a.id == b.id && a.date == b.date
}

func (c dialogCursor) available() bool {
	return c.input != nil || c.custom != nil
}

func loadDialogPage(ctx context.Context, api *tg.Client, page dialogPage) ([]chat, dialogCursor, error) {
	if page.filter != nil {
		return loadFolderPage(ctx, api, *page.filter, page.cursor.custom)
	}

	entries, next, _, err := loadNativeDialogs(ctx, api, page.folder, page.cursor, false, false)
	if err != nil {
		return nil, dialogCursor{}, err
	}

	chats := make([]chat, 0, len(entries))
	for _, entry := range entries {
		chats = append(chats, entry.chat)
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
	if len(a.chats) > 0 && sameDialogFolder(a.dialogPage, e.page) && e.page.step == 0 {
		id := a.chats[a.selected].key
		selected = max(0, slices.IndexFunc(e.chats, func(c chat) bool { return c.key == id }))
	}

	for i := range a.chats {
		old := a.chats[i]
		if old.readOutboxID == 0 {
			continue
		}

		if j := slices.IndexFunc(e.chats, func(c chat) bool { return c.key == old.key }); j >= 0 {
			e.chats[j].readOutboxID = max(e.chats[j].readOutboxID, old.readOutboxID)
		}
	}

	a.chats, a.selected, a.dialogPage = e.chats, selected, e.page
	a.dialogsLoaded = true

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

	page := dialogPage{folder: a.dialogPage.folder, filter: a.dialogPage.filter, step: step}
	if step > 0 {
		if !a.dialogPage.next.available() {
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
	if a.dialogPage.filter != nil {
		title = a.dialogPage.filter.title
	} else if a.dialogPage.folder == 1 {
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
