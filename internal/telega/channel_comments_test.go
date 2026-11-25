package telega

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestChannelCommentsAndNestedReplies(t *testing.T) {
	channel := chat{key: peerKey{'c', 5}, title: "News"}
	a := app{ready: true, open: true, chats: []chat{channel}, log: []message{{id: 80, text: "Post", comments: true, commentCount: 2}}}
	out := make(chan request, 1)

	checkScreen := func(want ...string) {
		t.Helper()

		var screen strings.Builder
		if err := paint(&screen, &a, 80, 24); err != nil {
			t.Fatal(err)
		}

		for _, text := range want {
			if !strings.Contains(screen.String(), text) {
				t.Errorf("screen does not show %q: %q", text, screen.String())
			}
		}
	}

	checkScreen("[c] Comments (2)", "c comments")
	a.input(key{text: "c"}, out)

	if req := <-out; req.kind != "discussion" || req.anchor != 80 {
		t.Fatalf("c must open the selected post's discussion: %+v", req)
	}

	thread := chat{key: peerKey{'c', 6}, title: "Comments · News", group: true, threadKind: commentThread, threadID: 70}
	a.receive(out, serverEvent{kind: "discussion", chat: channel.key, thread: &thread})

	if req := <-out; req.kind != "history" || req.chat.threadID != 70 {
		t.Fatalf("expected discussion history: %+v", req)
	}

	a.receive(out, serverEvent{kind: "history", chat: thread.key, threadID: thread.threadID, log: []message{
		{id: 82, text: "Nested response", reply: messageReply{id: 81, top: 70}},
		{id: 81, text: "First comment", reply: messageReply{id: 70}},
	}})
	checkScreen("i comment  r reply", ";37HFirst comment", ";39HNested response")

	send := func(shortcut string, target int) {
		t.Helper()
		a.input(key{text: shortcut}, out)
		a.input(key{text: "New text"}, out)
		a.input(key{name: "enter"}, out)

		req := <-out

		reply, ok := req.inputReply().(*tg.InputReplyToMessage)
		if req.kind != "send" || req.chat.key != thread.key || !ok || reply.ReplyToMsgID != target {
			t.Fatalf("%s should send a reply to %d in the discussion: %+v, reply %+v", shortcut, target, req, reply)
		}

		a.receive(out, serverEvent{kind: "send", chat: thread.key, threadID: thread.threadID, log: []message{
			{id: a.log[0].id + 1, text: req.text, out: true, reply: messageReply{id: target, top: thread.threadID}},
		}})
	}

	send("i", 70)

	a.offset = 2

	send("r", 81)
	a.input(key{text: "h"}, out)

	if req := <-out; req.kind != "threadParent" || req.anchor != 80 || req.chat.key != channel.key {
		t.Fatalf("h should return to the selected post: %+v", req)
	}
}
