package telega

import (
	"slices"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestCustomFolderRulePrecedence(t *testing.T) {
	filters := parseDialogFilters([]tg.DialogFilterClass{
		&tg.DialogFilterDefault{},
		&tg.DialogFilter{
			ID: 1, Title: tg.TextWithEntities{Text: "  Personal  "}, Contacts: true,
			ExcludeMuted: true, ExcludeRead: true, ExcludeArchived: true,
			IncludePeers: []tg.InputPeerClass{&tg.InputPeerChat{ChatID: 3}, &tg.InputPeerUser{UserID: 4}},
			ExcludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 4}},
		},
		&tg.DialogFilterChatlist{ID: 7, Title: tg.TextWithEntities{Text: "Shared"}},
	})
	if len(filters) != 2 || filters[0].id != 1 || filters[0].title != "Personal" || filters[1].title != "Shared" {
		t.Fatalf("wrong custom folder definitions: %+v", filters)
	}

	for _, tc := range []struct {
		name                    string
		key                     peerKey
		category                folderCategory
		archived, marked, muted bool
		mentions                int
		want                    bool
	}{
		{name: "marked contact", key: peerKey{'u', 1}, category: folderContacts, marked: true, want: true},
		{name: "read contact", key: peerKey{'u', 1}, category: folderContacts},
		{name: "muted contact", key: peerKey{'u', 1}, category: folderContacts, marked: true, muted: true},
		{name: "muted mention", key: peerKey{'u', 1}, category: folderContacts, muted: true, mentions: 1, want: true},
		{name: "archived contact", key: peerKey{'u', 1}, category: folderContacts, marked: true, archived: true},
		{name: "bot", key: peerKey{'u', 2}, category: folderBots, marked: true},
		{name: "explicit archived read muted group", key: peerKey{'g', 3}, category: folderGroups, archived: true, muted: true, want: true},
		{name: "explicit exclusion wins", key: peerKey{'u', 4}, category: folderContacts, marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := dialogEntry{chat: chat{key: tc.key, unread: unreadState{marked: tc.marked, mentions: tc.mentions}}, category: tc.category, muteSet: true}
			if tc.archived {
				entry.folder = 1
			}

			if tc.muted {
				entry.muteUntil = 101
			}

			var scan folderCursor

			got, err := scan.matches(t.Context(), nil, filters[0], entry, 100)
			if err != nil || got != tc.want {
				t.Fatalf("matches = %t, %v; want %t", got, err, tc.want)
			}
		})
	}
}

func TestCustomFolderPaginationMergesNativeSources(t *testing.T) {
	type item struct {
		id              int64
		date            int
		contact, pinned bool
	}

	build := func(items []item, folder int) *tg.MessagesDialogs {
		data := &tg.MessagesDialogs{}

		for _, row := range items {
			peer := &tg.PeerUser{UserID: row.id}
			data.Dialogs = append(data.Dialogs, &tg.Dialog{Peer: peer, TopMessage: int(row.id), Pinned: row.pinned, FolderID: folder})
			data.Users = append(data.Users, &tg.User{ID: row.id, FirstName: "Dialog", AccessHash: row.id, Contact: row.contact})
			data.Messages = append(data.Messages, &tg.Message{ID: int(row.id), PeerID: peer, Date: row.date})
		}

		return data
	}

	main := make([]item, chatLimit)
	for i := range main {
		main[i] = item{id: int64(i + 10), date: 300 - i, contact: true}
	}

	pin := item{id: 1, date: 1, contact: false}
	archived := build([]item{pin, {id: 2, date: 201, pinned: true, contact: true}, {id: 3, date: 301, contact: true}}, 1)
	calls := 0
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		calls++

		switch req := input.(type) {
		case *tg.MessagesGetPeerDialogsRequest:
			if len(req.Peers) != 1 || folderPeerKey(req.Peers[0].(*tg.InputDialogPeer).Peer).id != pin.id {
				t.Fatalf("unexpected pinned peer query: %+v", req)
			}

			result := output.(*tg.MessagesPeerDialogs)
			*result = tg.MessagesPeerDialogs{Dialogs: archived.Dialogs[:1], Messages: archived.Messages[:1], Users: archived.Users[:1]}
		case *tg.MessagesGetDialogsRequest:
			box := output.(*tg.MessagesDialogsBox)

			if folder, set := req.GetFolderID(); !set || (folder != 0 && folder != 1) {
				t.Fatalf("custom folder ID was sent to getDialogs: %+v", req)
			} else if folder == 1 {
				box.Dialogs = archived
			} else if req.OffsetID == 0 {
				// Telegram may return a short page with no matching chats.
				sparse := build([]item{{id: 200, date: 400}}, 0)
				box.Dialogs = &tg.MessagesDialogsSlice{Count: len(main) + 1, Dialogs: sparse.Dialogs, Messages: sparse.Messages, Users: sparse.Users}
			} else if req.OffsetID == 200 && req.OffsetDate == 400 && req.ExcludePinned {
				box.Dialogs = build(main, 0)
			} else {
				t.Fatalf("unexpected offset: %+v", req)
			}
		default:
			t.Fatalf("unexpected API call: %T", req)
		}

		return nil
	}))

	filter := parseDialogFilters([]tg.DialogFilterClass{&tg.DialogFilter{ID: 42, Contacts: true, PinnedPeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: pin.id, AccessHash: pin.id}}}})[0]
	page := dialogPage{filter: &filter}

	first, next, err := loadDialogPage(t.Context(), api, page)
	if err != nil || len(first) != chatLimit || !next.available() {
		t.Fatalf("first custom page = %d rows, next %t, err %v", len(first), next.available(), err)
	}

	if first[0].key.id != pin.id || !first[0].unread.pinned || first[0].folder != 1 || first[1].key.id != 3 || first[1].folder != 1 {
		t.Fatalf("wrong order or source at start of first page: %+v", first[:2])
	}

	page.cursor = next
	for range 2 {
		last, end, err := loadDialogPage(t.Context(), api, page)

		var ids []int64
		for _, c := range last {
			ids = append(ids, c.key.id)
			if c.unread.pinned {
				t.Fatalf("native pin appeared as a custom pin: %+v", c)
			}
		}

		if err != nil || end.available() || !slices.Equal(ids, []int64{104, 105, 2}) {
			t.Fatalf("last page = %v, next %t, err %v", ids, end.available(), err)
		}
	}

	if calls != 4 {
		t.Fatalf("continuing/reusing a buffered cursor should need no new calls; %d total", calls)
	}
}

func TestCustomFolderNavKeepsSeparatePageState(t *testing.T) {
	a := app{dialogFilters: []dialogFilter{{id: 1, title: "Work"}, {id: 2, title: "Shared"}}}
	out := make(chan request, 1)
	a.cycleDialogFolder(out, 1)

	native := <-out
	if native.page.folder != 1 || native.page.filter != nil {
		t.Fatalf("first Tab should open native Archive, got %+v", native.page)
	}

	a.receiveDialogs(out, serverEvent{page: native.page})
	a.cycleDialogFolder(out, 1)

	custom := <-out
	if custom.page.filter == nil || custom.page.filter.id != 1 {
		t.Fatalf("second Tab should open first custom folder, got %+v", custom.page)
	}

	custom.page.next = dialogCursor{custom: &folderCursor{}}
	a.receiveDialogs(out, serverEvent{page: custom.page, chats: []chat{{key: peerKey{'u', 1}}}})
	a.pageDialogs(out, 1)

	second := <-out
	if second.page.filter != custom.page.filter || second.page.cursor.custom == nil {
		t.Fatalf("lost custom folder or cursor on next page: %+v", second.page)
	}

	a.receiveDialogs(out, serverEvent{page: second.page, chats: []chat{{key: peerKey{'u', 2}}}})

	if got := a.dialogTitle(); got != "Work · page 2" {
		t.Fatalf("custom page title = %q", got)
	}

	a.pageDialogs(out, -1)

	previous := <-out
	if previous.page.filter != custom.page.filter || previous.page.cursor.available() || previous.page.step != -1 {
		t.Fatalf("incorrect previous page: %+v", previous.page)
	}
}
