package telega

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

type folderCategory uint8

const (
	folderContacts folderCategory = 1 << iota
	folderNonContacts
	folderGroups
	folderBroadcasts
	folderBots
)

type dialogFilter struct {
	id, order                                 int
	title                                     string
	categories                                folderCategory
	excludeMuted, excludeRead, excludeArchive bool
	pinned, explicit                          []tg.InputPeerClass
	include, exclude                          map[peerKey]bool
}

func folderPeerKey(peer tg.InputPeerClass) peerKey {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return peerKey{'s', 0}
	case *tg.InputPeerUser:
		return peerKey{'u', p.UserID}
	case *tg.InputPeerUserFromMessage:
		return peerKey{'u', p.UserID}
	case *tg.InputPeerChat:
		return peerKey{'g', p.ChatID}
	case *tg.InputPeerChannel:
		return peerKey{'c', p.ChannelID}
	case *tg.InputPeerChannelFromMessage:
		return peerKey{'c', p.ChannelID}
	default:
		return peerKey{}
	}
}

func folderPeerSet(peers []tg.InputPeerClass) map[peerKey]bool {
	keys := make(map[peerKey]bool, len(peers))
	for _, peer := range peers {
		if key := folderPeerKey(peer); key.kind != 0 {
			keys[key] = true
		}
	}

	return keys
}

func parseDialogFilters(list []tg.DialogFilterClass) []dialogFilter {
	filters := make([]dialogFilter, 0, len(list))
	for order, item := range list {
		var f dialogFilter

		switch filter := item.(type) {
		case *tg.DialogFilter:
			f = dialogFilter{
				id: filter.ID, title: filter.Title.Text, pinned: filter.PinnedPeers, explicit: filter.IncludePeers,
				include: folderPeerSet(filter.IncludePeers), exclude: folderPeerSet(filter.ExcludePeers),
				excludeMuted: filter.ExcludeMuted, excludeRead: filter.ExcludeRead, excludeArchive: filter.ExcludeArchived,
			}
			for i, set := range []bool{filter.Contacts, filter.NonContacts, filter.Groups, filter.Broadcasts, filter.Bots} {
				if set {
					f.categories |= 1 << i
				}
			}
		case *tg.DialogFilterChatlist:
			f = dialogFilter{id: filter.ID, title: filter.Title.Text, pinned: filter.PinnedPeers, explicit: filter.IncludePeers, include: folderPeerSet(filter.IncludePeers)}
		default:
			continue
		}

		f.title = limitText(strings.TrimSpace(f.title), 128)
		if f.title == "" {
			f.title = "Unnamed folder"
		}

		f.order = order
		filters = append(filters, f)
	}

	return filters
}

func loadDialogFilters(ctx context.Context, api *tg.Client) ([]dialogFilter, error) {
	result, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, err
	}

	return parseDialogFilters(result.Filters), nil
}

func folderHas(peers map[peerKey]bool, c chat) bool {
	return peers[c.key] || peers[folderPeerKey(c.input)]
}

func (f dialogFilter) includes(c dialogEntry) bool {
	return !folderHas(f.exclude, c.chat) && (folderHas(f.include, c.chat) ||
		f.categories&c.category != 0 && (!f.excludeArchive || c.folder != 1) && (!f.excludeRead || c.unread.any()))
}

// A custom folder merges two independent Telegram dialog streams. The cursor
// owns only the unread portions of their pages and is never mutated after it
// is published, so revisiting a previous page does not consume its results.
type folderCursor struct {
	sources       [2]folderSource
	pinned, extra []dialogEntry
	resolved      map[peerKey]bool
	defaults      [3]*tg.PeerNotifySettings
	extraSorted   bool
}

type folderSource struct {
	cursor        dialogCursor
	queue         []dialogEntry
	started, done bool
}

func compareDialogDates(a, b dialogEntry) int {
	return cmp.Compare(b.date, a.date)
}

func (c *folderCursor) initialize(ctx context.Context, api *tg.Client, f dialogFilter) error {
	peers := make([]tg.InputDialogPeerClass, 0, len(f.pinned)+len(f.explicit))
	requested := make(map[peerKey]bool)

	for _, list := range [][]tg.InputPeerClass{f.pinned, f.explicit} {
		for _, peer := range list {
			key := folderPeerKey(peer)
			if key.kind != 0 && !f.exclude[key] && !requested[key] {
				peers = append(peers, &tg.InputDialogPeer{Peer: peer})
				requested[key] = true
			}
		}
	}

	byPeer := make(map[peerKey]dialogEntry, len(peers))
	for batch := range slices.Chunk(peers, chatLimit) {
		result, err := api.MessagesGetPeerDialogs(ctx, batch)
		if err != nil {
			return err
		}

		for _, entry := range parseDialogEntries(result.Dialogs, result.Users, result.Chats, result.Messages, -1) {
			byPeer[entry.key] = entry
			byPeer[folderPeerKey(entry.input)] = entry
		}
	}

	c.resolved = make(map[peerKey]bool, len(peers))
	missing := false

	for i, list := range [][]tg.InputPeerClass{f.pinned, f.explicit} {
		for _, peer := range list {
			key := folderPeerKey(peer)
			if !requested[key] {
				continue
			}

			entry, ok := byPeer[key]
			if !ok {
				missing = missing || i != 0
				continue
			}

			if c.resolved[entry.key] || folderHas(f.exclude, entry.chat) {
				continue
			}

			entry.unread.pinned = i == 0
			if entry.unread.pinned {
				c.pinned = append(c.pinned, entry)
			} else {
				c.extra = append(c.extra, entry)
			}

			c.resolved[entry.key] = true
		}
	}

	if f.categories == 0 && !missing || f.excludeArchive && !missing {
		c.sources[1].done = true
	}

	if f.categories == 0 && !missing {
		c.sources[0].done = true
	}

	return nil
}

func (c *folderCursor) matches(ctx context.Context, api *tg.Client, f dialogFilter, entry dialogEntry, now int64) (bool, error) {
	if c.resolved[entry.key] || !f.includes(entry) {
		return false, nil
	}

	if !f.excludeMuted || folderHas(f.include, entry.chat) || entry.unread.mentions > 0 && entry.folder != 1 {
		return true, nil
	}

	if entry.muteSet {
		return int64(entry.muteUntil) <= now, nil
	}

	var kind int

	var target tg.InputNotifyPeerClass = &tg.InputNotifyUsers{}

	switch entry.category {
	case folderGroups:
		kind, target = 1, &tg.InputNotifyChats{}
	case folderBroadcasts:
		kind, target = 2, &tg.InputNotifyBroadcasts{}
	}

	if c.defaults[kind] == nil {
		settings, err := api.AccountGetNotifySettings(ctx, target)
		if err != nil {
			return false, err
		}

		c.defaults[kind] = settings
	}

	until, _ := c.defaults[kind].GetMuteUntil()

	return int64(until) <= now, nil
}

func (c *folderCursor) fill(ctx context.Context, api *tg.Client, f dialogFilter, folder int, now int64) error {
	source := &c.sources[folder]
	for len(source.queue) == 0 && !source.done {
		initial := !source.started

		entries, next, more, err := loadNativeDialogs(ctx, api, folder, source.cursor, !initial, true)
		if err != nil {
			return err
		}

		source.started = true
		source.cursor = next
		// A full page of native pins has no usable offset. In that case,
		// request the start of the non-pinned stream on the next iteration.
		source.done = !next.available() && !(initial && more && slices.ContainsFunc(entries, func(entry dialogEntry) bool {
			return entry.unread.pinned
		}))

		for _, entry := range entries {
			ok, err := c.matches(ctx, api, f, entry, now)
			if err != nil {
				return err
			}

			if !ok {
				continue
			}

			nativePin := entry.unread.pinned

			entry.unread.pinned = false
			if nativePin && initial {
				c.extra = append(c.extra, entry)
			} else {
				source.queue = append(source.queue, entry)
			}
		}
	}

	return nil
}

func (c *folderCursor) next(ctx context.Context, api *tg.Client, f dialogFilter, now int64) (*[]dialogEntry, error) {
	if len(c.pinned) > 0 {
		return &c.pinned, nil
	}

	for folder := range c.sources {
		if err := c.fill(ctx, api, f, folder, now); err != nil {
			return nil, err
		}
	}

	if !c.extraSorted {
		slices.SortStableFunc(c.extra, compareDialogDates)
		c.extraSorted = true
	}

	var best *[]dialogEntry
	for _, queue := range []*[]dialogEntry{&c.extra, &c.sources[0].queue, &c.sources[1].queue} {
		if len(*queue) > 0 && (best == nil || compareDialogDates((*queue)[0], (*best)[0]) < 0) {
			best = queue
		}
	}

	return best, nil
}

func loadFolderPage(ctx context.Context, api *tg.Client, f dialogFilter, cursor *folderCursor) ([]chat, dialogCursor, error) {
	var scan folderCursor
	if cursor == nil {
		if err := scan.initialize(ctx, api, f); err != nil {
			return nil, dialogCursor{}, err
		}
	} else {
		scan = *cursor
	}

	chats := make([]chat, 0, chatLimit)

	now := time.Now().Unix()
	for {
		queue, err := scan.next(ctx, api, f, now)
		if err != nil {
			return nil, dialogCursor{}, err
		}

		if queue == nil {
			return chats, dialogCursor{}, nil
		}

		if len(chats) == chatLimit {
			return chats, dialogCursor{custom: &scan}, nil
		}

		chats = append(chats, (*queue)[0].chat)
		*queue = (*queue)[1:len(*queue):len(*queue)]
	}
}

func sameDialogFolder(a, b dialogPage) bool {
	if a.filter == nil || b.filter == nil {
		return a.filter == nil && b.filter == nil && a.folder == b.folder
	}

	return a.filter.id == b.filter.id
}

func (a *app) cycleDialogFolder(out chan<- request, step int) {
	if a.pending != "" || a.demo {
		return
	}

	pages := []dialogPage{{folder: 0}, {folder: 1}}
	for i := range a.dialogFilters {
		pages = append(pages, dialogPage{filter: &a.dialogFilters[i]})
	}

	index := max(0, slices.IndexFunc(pages, func(page dialogPage) bool { return sameDialogFolder(page, a.dialogPage) }))
	a.ask(out, request{kind: "chats", page: pages[(index+step+len(pages))%len(pages)]})
}

func (a *app) receiveDialogFilters(out chan<- request, filters []dialogFilter) {
	a.dialogFilters = filters

	active := a.dialogPage.filter
	if active == nil {
		return
	}

	var updated *dialogFilter
	if i := slices.IndexFunc(filters, func(f dialogFilter) bool { return f.id == active.id }); i >= 0 {
		updated = &filters[i]
	}

	if reflect.DeepEqual(active, updated) {
		return
	}

	if !a.open && a.pending == "" {
		a.ask(out, request{kind: "chats", page: dialogPage{filter: updated}})
	}
}
