package telega

import "testing"

func TestChatStatusCountsLoadedDialogs(t *testing.T) {
	a := app{ready: true, chatQuery: "missing", unreadOnly: true}
	check := func(wantUnread, wantMentions int, wantAvailable bool) {
		t.Helper()

		unread, mentions, available := a.chatStatusCounts()
		if unread != wantUnread || mentions != wantMentions || available != wantAvailable {
			t.Fatalf("chat status = (%d, %d, %v); want (%d, %d, %v)", unread, mentions, available, wantUnread, wantMentions, wantAvailable)
		}
	}
	out := make(chan request, 1)

	check(0, 0, false)
	a.receive(out, serverEvent{kind: "chats"})
	check(0, 0, true)

	key := peerKey{'u', 1}
	a.receive(out, serverEvent{kind: "chats", chats: []chat{
		{key: key, unread: unreadState{count: 10, mentions: 3, topID: 9}},
		{key: peerKey{'u', 2}, unread: unreadState{marked: true}},
		{key: peerKey{'u', 3}, unread: unreadState{mentions: 2}},
		{key: peerKey{'u', 4}},
	}})
	check(3, 5, true)

	a.receive(out, serverEvent{kind: "new", chat: key, log: []message{{id: 10, mentioned: true}}})
	check(3, 6, true)
	a.receive(out, serverEvent{kind: "markRead", chat: key, unread: unreadState{topID: 10, inboxID: 10}})
	check(2, 2, true)
	a.receive(out, serverEvent{kind: "ready"})
	check(0, 0, false)
}

func TestAutoReadRequiresVisibleStatusPane(t *testing.T) {
	visible, checks := false, 0
	c := chat{key: peerKey{'u', 1}, unread: unreadState{count: 1, topID: 8, inboxID: 7}}
	a := app{
		ready: true, open: true, focused: true,
		chats: []chat{c}, log: []message{{id: 8}},
		autoReadVisible: func() bool {
			checks++
			return visible
		},
	}
	out := make(chan request, 1)
	a.offset = 1
	a.markVisibleRead(out)

	if checks != 0 {
		t.Fatal("visibility was checked without an auto-read candidate")
	}

	a.offset = 0
	a.markVisibleRead(out)

	if checks != 1 || len(out) != 0 {
		t.Fatal("a hidden pane must not send an auto-read request")
	}

	visible = true

	a.markVisibleRead(out)

	select {
	case req := <-out:
		if req.kind != "autoRead" || req.anchor != 8 {
			t.Fatalf("unexpected request: %+v", req)
		}
	default:
		t.Fatal("a visible pane must still send an auto-read request")
	}
}
