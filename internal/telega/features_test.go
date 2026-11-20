package telega

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type historyInvoker func(*tg.MessagesGetHistoryRequest) []tg.MessageClass

func (f historyInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: f(input.(*tg.MessagesGetHistoryRequest))}
	return nil
}

func TestHistoryPagination(t *testing.T) {
	c := chat{key: peerKey{'g', 42}, input: &tg.InputPeerChat{ChatID: 42}}
	a := app{ready: true, open: true, chats: []chat{c}}
	requests := make(chan request, 1)

	for id := 200; id > 200-historyWindow; id-- {
		a.log = append(a.log, message{id: id})
	}

	a.offset = historyWindow - 3
	a.scroll(requests, 8)

	req := <-requests
	if req.kind != "older" || req.anchor != 105 {
		t.Fatalf("wrong request: %+v", req)
	}

	api := tg.NewClient(historyInvoker(func(query *tg.MessagesGetHistoryRequest) []tg.MessageClass {
		if query.OffsetID != 105 || query.Limit != historyLimit || query.AddOffset != 0 {
			t.Fatalf("wrong history cursor: %+v", query)
		}

		var result []tg.MessageClass
		for id := 105; id > 105-historyLimit-1; id-- {
			result = append(result, &tg.Message{ID: id, Message: "older"})
		}

		return result
	}))

	items, err := loadHistory(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(requests, serverEvent{kind: req.kind, chat: c.key, step: req.step, log: items})

	if len(a.log) != historyWindow || a.log[a.offset].id != 99 || !a.newerTrimmed {
		t.Fatalf("older window has %d messages, selected %d", len(a.log), a.log[a.offset].id)
	}

	a.offset = 0
	a.scroll(requests, -8)

	req = <-requests
	if req.kind != "newer" || req.anchor != 152 {
		t.Fatalf("wrong request: %+v", req)
	}

	api = tg.NewClient(historyInvoker(func(query *tg.MessagesGetHistoryRequest) []tg.MessageClass {
		if query.OffsetID != 152 || query.MinID != 152 || query.AddOffset != -historyLimit-1 {
			t.Fatalf("wrong newer cursor: %+v", query)
		}

		var result []tg.MessageClass
		for id := 200; id > 152; id-- {
			result = append(result, &tg.Message{ID: id, Message: "newer"})
		}

		return result
	}))

	items, err = loadHistory(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(requests, serverEvent{kind: req.kind, chat: c.key, step: req.step, log: items})

	if len(a.log) != historyWindow || a.log[a.offset].id != 160 {
		t.Fatalf("newer window has %d messages, selected %d", len(a.log), a.log[a.offset].id)
	}
}

func TestUnreadAndNotifications(t *testing.T) {
	pinned, active, incoming := peerKey{'u', 1}, peerKey{'c', 2}, peerKey{'g', 3}
	a := app{ready: true, open: true, focused: true, selected: 1, chats: []chat{
		{key: pinned, title: "Pinned", unread: unreadState{pinned: true}},
		{key: active, title: "Active"},
		{key: incoming, title: "Incoming"},
	}}

	var arrived serverEvent

	dispatch := tg.NewUpdateDispatcher()

	onNew := func(_ context.Context, _ tg.Entities, raw tg.MessageClass) error {
		key, msg, ok := unwrapMessage(raw)
		if !ok {
			t.Fatal("short update was not decoded")
		}

		arrived = serverEvent{kind: "new", chat: key, log: []message{msg}}

		return nil
	}
	if err := dispatchUpdates(t.Context(), dispatch, onNew, &tg.UpdateShortChatMessage{
		ChatID: incoming.id, ID: 11, Mentioned: true, Message: "new",
	}); err != nil {
		t.Fatal(err)
	}

	if title := a.notification(arrived); title != "Incoming" {
		t.Fatalf("notification title: %q", title)
	}

	a.receive(nil, arrived)
	a.receive(nil, arrived)

	if a.chats[a.selected].key != active || a.chats[0].key != pinned || a.chats[1].unread.badge() != "1@" {
		t.Fatalf("active chat, pinned order or unread badge changed incorrectly")
	}

	a.chats[1].muteUntil = int(time.Now().Add(time.Hour).Unix())
	if title := a.notification(arrived); title != "" {
		t.Fatalf("muted chat notified: %q", title)
	}

	a.receive(nil, serverEvent{kind: "read", chat: incoming, unread: unreadState{inboxID: 11}})

	if a.chats[1].unread.count != 0 || a.chats[1].unread.inboxID != 11 {
		t.Fatal("server read update was not applied")
	}
}

func TestFormattedChannelMessage(t *testing.T) {
	msg := message{
		text:       "🙂bold\nquote\nsecret\x1b[31m",
		attachment: &attachment{name: "note.txt"},
		spans: messageSpans([]tg.MessageEntityClass{
			&tg.MessageEntityBold{Offset: 2, Length: 4},
			&tg.MessageEntityBlockquote{Offset: 7, Length: 5},
			&tg.MessageEntitySpoiler{Offset: 13, Length: 6},
		}),
	}

	hidden := strings.Join(formattedMessage(msg, 24, 8, false), "\n")
	if !strings.Contains(hidden, "[File] note.txt") ||
		!strings.Contains(hidden, "🙂\x1b[0;1mbold") ||
		!strings.Contains(hidden, "│ ") ||
		!strings.Contains(hidden, "██████") ||
		strings.Contains(hidden, "secret") || strings.Contains(hidden, "\x1b[31m") {
		t.Fatalf("unexpected formatted content %q", hidden)
	}

	shown := strings.Join(formattedMessage(msg, 24, 8, true), "\n")
	if !strings.Contains(shown, "secret") {
		t.Fatal("spoiler was not revealed")
	}

	var out bytes.Buffer

	a := app{ready: true, open: true, chats: []chat{{title: "Channel", unread: unreadState{count: 2}}}, log: []message{msg}}
	if err := paint(&out, &a, 80, 24); err != nil || !strings.Contains(out.String(), "\x1b[0;1mbold") {
		t.Fatal("formatted message was lost when painting the terminal")
	}
}

func TestAttachmentNameAndSystemOpen(t *testing.T) {
	media := &tg.MessageMediaDocument{Document: &tg.Document{
		DCID: 2, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "..\\notes/a.pdf"}},
	}}

	file := fileFromMedia(media)
	if file == nil || file.name != "a.pdf" || file.dc != 2 {
		t.Fatalf("wrong file metadata: %+v", file)
	}

	dir := t.TempDir()
	t.Setenv("PATH", dir)
	target := filepath.Join(dir, "opened")
	t.Setenv("TELEGA_TEST_OPENED", target)

	opener := filepath.Join(dir, "open")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$TELEGA_TEST_OPENED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "a file.pdf")

	cmd, err := openAttachment(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(target)
	if err != nil || string(data) != path {
		t.Fatalf("wrong opener argument: %q, %v", data, err)
	}
}
