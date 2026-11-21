package telega

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func workflowChat(id int64, title string) chat {
	return chat{key: peerKey{'g', id}, title: title, group: true, input: &tg.InputPeerChat{ChatID: id}}
}

func TestHistoryPrefetchAtHalfScreen(t *testing.T) {
	c := workflowChat(1, "Group")

	a := app{ready: true, open: true, chats: []chat{c}}
	for id := 150; id > 150-historyWindow; id-- {
		a.log = append(a.log, message{id: id, text: "short"})
	}

	requests := make(chan request, 1)

	var screen bytes.Buffer

	a.offset = 74
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	a.prefetchHistory(requests)

	if len(requests) != 0 {
		t.Fatal("a full viewport should not load another page")
	}

	a.offset = 93
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	a.prefetchHistory(requests)

	if len(requests) != 1 {
		t.Fatal("the next page should be in flight while older messages remain")
	}

	req := <-requests
	if req.kind != "prefetch" || req.anchor != 55 || a.pending != "" {
		t.Fatalf("invalid background request: %+v", req)
	}

	a.scroll(requests, 1)
	cursor := a.log[a.offset].id

	var older []message
	for id := req.anchor - 1; id >= req.anchor-historyLimit; id-- {
		older = append(older, message{id: id})
	}

	a.receive(requests, serverEvent{kind: req.kind, chat: c.key, anchor: req.anchor, log: older})

	if a.log[a.offset].id != cursor || len(a.log) != historyWindow {
		t.Fatalf("prefetch moved the cursor or exceeded the window: %d, %d", a.log[a.offset].id, len(a.log))
	}
}

func TestSwitchChatRestoresReplyAndCaption(t *testing.T) {
	first, second := workflowChat(1, "First"), workflowChat(2, "Second")
	a := app{
		ready: true, open: true, chats: []chat{first, second}, chatQuery: "First",
		log: []message{{id: 12, text: "Original"}},
	}
	out := make(chan request, 1)
	a.input(key{text: "r"}, out)
	a.input(key{text: "before"}, out)
	a.input(key{name: "newline"}, out)
	a.input(key{text: "after"}, out)
	a.input(key{name: "home"}, out)

	path := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	a.staged = &uploadAttachment{path: path, temporary: true}
	a.input(key{name: "nextChat"}, out)

	if req := <-out; req.kind != "history" || req.chat.key != second.key || a.composing {
		t.Fatal("Ctrl+N did not switch from a filtered chat with a draft")
	}

	a.receive(out, serverEvent{kind: "history", chat: second.key, log: []message{{id: 21}}})
	a.input(key{name: "previousChat"}, out)

	if req := <-out; req.chat.key != first.key {
		t.Fatal("Ctrl+P did not go back")
	}

	a.receive(out, serverEvent{kind: "history", chat: first.key, log: []message{{id: 12}}})
	a.input(key{text: "and "}, out)

	if !a.composing || a.staged == nil || a.staged.path != path || a.replyID != 12 || string(a.draft) != "before\nand after" {
		t.Fatalf("draft or cursor not restored: %q / %+v / reply %d", a.draft, a.staged, a.replyID)
	}

	a.input(key{name: "escape"}, out)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancel did not remove the staged screenshot: %v", err)
	}
}

func TestJumpSearchAndUnread(t *testing.T) {
	c := workflowChat(1, "Group")
	c.unread = unreadState{count: 10, inboxID: 50, topID: 80}
	a := app{ready: true, open: true, chats: []chat{c}, log: []message{{id: 80, reply: messageReply{id: 10}}}}
	out := make(chan request, 1)
	a.input(key{text: "J"}, out)

	req := <-out
	if req.kind != "jump" || req.anchor != 10 {
		t.Fatalf("wrong original: %+v", req)
	}

	api := tg.NewClient(historyInvoker(func(query *tg.MessagesGetHistoryRequest) []tg.MessageClass {
		if query.OffsetID != 10 || query.AddOffset != -historyLimit/2 {
			t.Fatal("original must be loaded with surrounding history")
		}

		return []tg.MessageClass{&tg.Message{ID: 11}, &tg.Message{ID: 10}, &tg.Message{ID: 9}}
	}))

	log, err := loadHistory(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, anchor: req.anchor, log: log})

	if !a.newerTrimmed || a.log[a.offset].id != 10 {
		t.Fatal("jump did not select the original")
	}

	a.input(key{name: "jumpBack"}, out)

	req = <-out
	if req.kind != "jumpBack" || req.anchor != 80 {
		t.Fatalf("wrong return position: %+v", req)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, anchor: req.anchor, log: []message{{id: 80}}})

	if len(a.jumps) != 0 || a.log[a.offset].id != 80 {
		t.Fatal("return did not restore the cursor")
	}

	a.input(key{text: "?"}, out)
	a.input(key{text: "query"}, out)
	a.input(key{name: "enter"}, out)
	req = <-out
	api = tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesSearchRequest)
		if query.Q != "query" || query.Peer != c.input || query.Limit != historyLimit {
			t.Fatalf("wrong server search: %+v", query)
		}

		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 20, Message: "query match"}}}

		return nil
	}))

	log, err = searchHistory(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, log: log})
	a.input(key{name: "enter"}, out)

	if req = <-out; req.kind != "jump" || req.anchor != 20 {
		t.Fatalf("search did not open the selected hit: %+v", req)
	}

	a.receive(out, serverEvent{kind: "jump", chat: c.key, anchor: 20, log: log})

	a.input(key{text: "u"}, out)
	req = <-out
	api = tg.NewClient(historyInvoker(func(query *tg.MessagesGetHistoryRequest) []tg.MessageClass {
		if req.kind != "unread" || query.OffsetID != 50 || query.AddOffset >= 0 {
			t.Fatal("unread jump must load forward from the read marker")
		}

		return []tg.MessageClass{&tg.Message{ID: 54}, &tg.Message{ID: 53}, &tg.Message{ID: 52, Out: true}, &tg.Message{ID: 49}}
	}))

	log, anchor, err := findUnread(t.Context(), api, req)
	if err != nil || anchor != 53 {
		t.Fatalf("wrong first unread: %d, %v", anchor, err)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, anchor: anchor, log: log})

	if a.log[a.offset].id != 53 || a.chats[0].unread.count != 10 {
		t.Fatal("jump should select first unread and leave the read marker intact")
	}
}

func TestForwardConfirmationKeepsRecipient(t *testing.T) {
	source, dest, other := workflowChat(1, "Source"), workflowChat(2, "Destination"), workflowChat(3, "Other")
	a := app{ready: true, open: true, chats: []chat{source, dest, other}, log: []message{{id: 30}, {id: 25}}}

	out := make(chan request, 1)
	for _, k := range []key{{text: " "}, {text: "k"}, {text: " "}, {text: "f"}, {text: "dest"}, {name: "enter"}} {
		a.input(k, out)
	}

	a.chats[1], a.chats[2] = other, dest
	a.input(key{name: "paste", text: "y"}, out)

	if len(out) != 0 || !a.forwardConfirm {
		t.Fatal("forwarding should wait for literal confirmation")
	}

	a.input(key{text: "y"}, out)
	req := <-out
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesForwardMessagesRequest)
		if query.FromPeer != source.input || query.ToPeer != dest.input || !slices.Equal(query.ID, []int{25, 30}) || len(query.RandomID) != 2 {
			t.Fatalf("wrong forward payload: %+v", query)
		}

		output.(*tg.UpdatesBox).Updates = &tg.Updates{}

		return nil
	}))

	sent, _, err := forwardMessages(t.Context(), api, req)
	if err != nil || !slices.Equal(sent, []int{25, 30}) {
		t.Fatalf("forward confirmation: %v %v", sent, err)
	}
}

func TestEditCopyAndAuthor(t *testing.T) {
	c := workflowChat(1, "Group")
	raw := &tg.Message{PeerID: &tg.PeerChat{ChatID: 1}, FromID: &tg.PeerUser{UserID: 7}, ID: 2, Out: true, Message: "first\nsecond"}
	_, msg, _ := unwrapMessage(raw)
	msg = resolveAuthor(msg, tg.Entities{Users: map[int64]*tg.User{7: {FirstName: "Ada", LastName: "Lovelace"}}})
	a := app{ready: true, open: true, chats: []chat{c}, log: []message{msg}}
	out := make(chan request, 1)

	var screen bytes.Buffer
	if err := paint(&screen, &a, 96, 24); err != nil || !strings.Contains(screen.String(), "Ada Lovelace") {
		t.Fatalf("author is missing from the rendered conversation: %v", err)
	}

	if !strings.Contains(screen.String(), "e edit") || strings.Contains(screen.String(), "· edited") {
		t.Fatal("own unedited message should have only the edit shortcut")
	}

	a.input(key{text: "e"}, out)
	a.input(key{name: "home"}, out)
	a.input(key{text: "new "}, out)
	a.input(key{name: "enter"}, out)
	req := <-out
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesEditMessageRequest)
		if text, ok := query.GetMessage(); !ok || query.ID != 2 || text != "first\nnew second" {
			t.Fatalf("wrong edit request: %+v", query)
		}

		copy := *raw
		copy.Message = query.Message
		copy.SetEditDate(123)
		output.(*tg.UpdatesBox).Updates = &tg.UpdateShort{Update: &tg.UpdateEditMessage{Message: &copy}}

		return nil
	}))

	log, err := editMessage(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, anchor: 2, log: log})
	screen.Reset()

	if err := paint(&screen, &a, 96, 24); err != nil || !strings.Contains(screen.String(), "· edited") {
		t.Fatalf("edit should be marked after acknowledgement: %v", err)
	}

	a.input(key{text: "y"}, out)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	var terminal bytes.Buffer
	if err := copyClipboard(t.Context(), &terminal, a.copyText); err != nil {
		t.Fatal(err)
	}

	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("first\nnew second")) + "\x07"
	if terminal.String() != want {
		t.Fatalf("clipboard changed the copied message: %q", terminal.String())
	}
}

func TestUnknownLiveAuthorAndCodeEdit(t *testing.T) {
	c := workflowChat(1, "Group")
	sender := peerKey{'u', 7}
	a := app{ready: true, open: true, chats: []chat{c}, log: []message{{id: 10, author: sender}}}
	out := make(chan request, 1)
	a.requestMissingAuthor(out)
	req := <-out
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.UsersGetUsersRequest)

		user := query.ID[0].(*tg.InputUserFromMessage)
		if user.UserID != sender.id || user.MsgID != 10 || user.Peer != c.input {
			t.Fatalf("wrong lookup for a live group message: %+v", user)
		}

		output.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: 7, FirstName: "New Member"}}

		return nil
	}))
	name := lookupAuthor(t.Context(), api, req)
	a.receive(out, serverEvent{kind: "author", author: sender, text: name})
	a.requestMissingAuthor(out)

	if a.authorLabel(a.log[0]) != "New Member" || len(out) != 0 {
		t.Fatal("lookup should name and cache the live sender")
	}

	const (
		plain  = "🙂 Go\n\tfmt.Println(1)\nend"
		markup = "🙂 Go\n```go\n\tfmt.Println(1)\n```\nend"
	)

	_, entities := codeBlocks(markup)
	a.log = []message{{id: 11, out: true, text: plain, spans: messageSpans(entities)}}
	a.input(key{text: "e"}, out)

	if string(a.draft) != markup {
		t.Fatalf("editing lost the original code markup: %q", a.draft)
	}

	a.input(key{name: "enter"}, out)

	if a.composing || len(out) != 0 {
		t.Fatal("submitting unchanged input should leave the original alone")
	}
}

func TestForwardSourceAndJump(t *testing.T) {
	current := workflowChat(1, "Discussion")
	channel := chat{key: peerKey{'c', 2}, title: "News", input: &tg.InputPeerChannel{ChannelID: 2}}
	_, msg, _ := unwrapMessage(&tg.Message{ID: 20, Message: "news text", FwdFrom: tg.MessageFwdHeader{
		FromID: &tg.PeerChannel{ChannelID: 2}, ChannelPost: 45,
	}})
	msg = resolveAuthor(msg, tg.Entities{Channels: map[int64]*tg.Channel{2: {Title: "News", Username: "example_news"}}})
	a := app{ready: true, open: true, chats: []chat{current, channel}, log: []message{msg}}
	out := make(chan request, 1)

	if lines := a.messageLines(msg, 60, 3); !strings.Contains(lines[0], "[F] Forwarded from News") {
		t.Fatalf("source is missing from the forwarded message: %q", lines)
	}

	if lines := a.messageLines(msg, 14, 3); !strings.Contains(lines[0], "[F]") {
		t.Fatalf("shortcut disappeared at narrow width: %q", lines)
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 24, 12); err != nil || !strings.Contains(screen.String(), "F open source") {
		t.Fatalf("shortcut missing from the status row: %v", err)
	}

	a.input(key{text: "F"}, out)

	if req := <-out; req.kind != "jump" || req.chat.key != channel.key || req.anchor != 45 {
		t.Fatalf("did not jump to the source post: %+v", req)
	}

	a.chats, a.pending = a.chats[:1], ""
	a.input(key{text: "J"}, out)

	if a.openTarget != "https://t.me/example_news/45" {
		t.Fatalf("expected public source URL, got %q", a.openTarget)
	}

	_, hidden, _ := unwrapMessage(&tg.Message{ID: 21, Message: "private text", FwdFrom: tg.MessageFwdHeader{FromName: "Ada"}})
	a.log[0], a.openTarget = hidden, ""
	a.input(key{text: "F"}, out)

	if len(out) != 0 || a.openTarget != "" || !strings.Contains(a.status, "did not include") || a.forwardName(hidden.forward) != "Ada" {
		t.Fatal("a name without an original post must remain visible")
	}

	saved := forwardInfo(tg.MessageFwdHeader{FromName: "Ada", SavedFromPeer: &tg.PeerChat{ChatID: 1}, SavedFromMsgID: 12})
	if saved.position != (chatPosition{chat: current.key, id: 12}) {
		t.Fatalf("wrong link back from Saved Messages: %+v", saved.position)
	}
}

func TestCyrillicCommandsAndPrivateChatDirection(t *testing.T) {
	c := chat{key: peerKey{'u', 1}, title: "Собеседник"}
	a := app{
		ready: true, open: true, chats: []chat{c},
		log: []message{{id: 2, out: true, text: "Исходящее"}, {id: 1, text: "Входящее"}},
	}
	out := make(chan request, 1)
	a.input(key{text: "л"}, out)

	if a.offset != 1 {
		t.Fatal("Russian equivalent of k should select the older message")
	}

	a.input(key{text: "о"}, out)

	if a.offset != 0 {
		t.Fatal("Russian equivalent of j should select the newer message")
	}

	a.input(key{text: "ш"}, out)
	a.input(key{text: "л"}, out)

	if !a.composing || string(a.draft) != "л" {
		t.Fatal("normal commands should follow layout positions and draft text should stay Russian")
	}

	a.input(key{name: "escape"}, out)
	a.input(key{text: "."}, out)
	a.input(key{text: "о"}, out)

	if !a.searching || string(a.search) != "о" {
		t.Fatal("the Russian slash position should start search and preserve query text")
	}

	a.input(key{name: "escape"}, out)

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil || !strings.Contains(screen.String(), "→ You") ||
		!strings.Contains(screen.String(), "← Собеседник") {
		t.Fatalf("a private chat should identify both sides: %v", err)
	}
}

func TestAutoReadWhileComposing(t *testing.T) {
	c := workflowChat(1, "Group")
	c.unread = unreadState{count: 2, inboxID: 10, topID: 12}
	a := app{
		ready: true, open: true, focused: true, composing: true, chats: []chat{c},
		log: []message{{id: 12}, {id: 11}},
	}
	out := make(chan request, 1)
	a.input(key{text: "draft"}, out)
	a.markVisibleRead(out)

	req := <-out
	if req.kind != "autoRead" || req.anchor != 12 || a.pending != "" {
		t.Fatalf("visible messages should be read without blocking the composer: %+v", req)
	}

	a.receive(out, serverEvent{kind: "autoRead", chat: c.key, unread: unreadState{topID: 12, inboxID: 12}})

	if !a.composing || string(a.draft) != "draft" || a.chats[0].unread.count != 0 {
		t.Fatal("read acknowledgment changed the draft or left an unread count")
	}

	a.chats[0].unread = unreadState{count: 1, inboxID: 12, topID: 13}
	a.log = slices.Insert(a.log, 0, message{id: 13})
	a.focused = false
	a.markVisibleRead(out)
	a.focused, a.offset = true, 1
	a.markVisibleRead(out)

	if len(out) != 0 {
		t.Fatal("history outside the active view should stay unread")
	}

	a.offset = 0
	a.markVisibleRead(out)

	if req := <-out; req.anchor != 13 {
		t.Fatalf("returning to the latest message did not mark it read: %+v", req)
	}
}
