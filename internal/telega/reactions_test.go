package telega

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestHistoryAndLiveReactions(t *testing.T) {
	peer := &tg.PeerUser{UserID: 7}
	raw := &tg.Message{PeerID: peer, ID: 24, Message: "On the bench", Out: true}
	raw.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{
		{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 2},
		{Reaction: &tg.ReactionEmoji{Emoticon: "❤️"}, Count: 1},
		{Reaction: &tg.ReactionCustomEmoji{DocumentID: 5}, Count: 3},
	}})

	chatKey, msg, _ := unwrapMessage(raw)

	a := app{ready: true, open: true, chats: []chat{{key: chatKey, title: "Александр Волков"}}, log: []message{msg}}
	if got := a.messageLines(msg, 48, 2); len(got) != 2 || got[0] != msg.text || !strings.Contains(got[1], "👍 2  ❤️ 1  custom 3") {
		t.Fatalf("wrong reaction footer: %+v", got)
	}

	events := make(chan serverEvent, 1)
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnMessageReactions(reactionUpdates(events))

	deliver := func(update *tg.UpdateMessageReactions) {
		t.Helper()

		if err := dispatchUpdates(t.Context(), dispatcher, nil, &tg.UpdateShort{Update: update}); err != nil {
			t.Fatal(err)
		}

		a.receive(nil, <-events)
	}
	deliver(&tg.UpdateMessageReactions{Peer: &tg.PeerUser{UserID: 8}, MsgID: 24})

	if a.log[0].reactions != msg.reactions {
		t.Fatal("update from another chat changed the message")
	}

	deliver(&tg.UpdateMessageReactions{
		Peer: peer, MsgID: 24, Reactions: tg.MessageReactions{Results: []tg.ReactionCount{
			{Reaction: &tg.ReactionEmoji{Emoticon: "🔥"}, Count: 4},
		}},
	})

	if a.log[0].reactions != "🔥 4" {
		t.Fatalf("live update was not applied: %q", a.log[0].reactions)
	}

	a.receive(nil, serverEvent{kind: "live", chat: chatKey, log: []message{{id: 24, text: "Edited", out: true}}})

	if a.log[0].reactions != "🔥 4" || a.log[0].text != "Edited" {
		t.Fatal("edit without reaction metadata lost the live reactions")
	}

	deliver(&tg.UpdateMessageReactions{Peer: peer, MsgID: 24})

	if a.log[0].reactions != "" {
		t.Fatal("the final reaction was not removed")
	}

	a.log = append(a.log, message{id: 23, text: "Incoming"})

	var screen bytes.Buffer

	if err := paint(&screen, &a, 100, 24); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	if !strings.Contains(out, "Александр Волков") || !strings.Contains(out, "← Александр") ||
		strings.Contains(out, "← Александр Вол") {
		t.Fatal("private chat should show the full title and a compact sender label")
	}
}

func TestConsoleReactionLabels(t *testing.T) {
	msg := message{text: "On the bench", reactions: "👍 3  ❤️ 2  ⭐ 1  custom 4  🛠 5", myReaction: "❤️"}
	a := app{
		ready: true, open: true, textReactions: true, pickingReaction: true,
		chats: []chat{{title: "Lab"}}, log: []message{msg},
	}

	lines := a.messageLines(msg, 80, 2)
	if len(lines) != 2 || !strings.Contains(lines[1], "[like] 3  [heart] 2  [star] 1  [custom] 4  [U+1F6E0] 5") {
		t.Fatalf("unexpected console reactions: %q", lines)
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, minTerminalCols, 12); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	if !strings.Contains(out, "1 Like 2 Heart 3 Fire") || !strings.Contains(out, "4 Laugh 5 Party 6 No") ||
		!strings.Contains(out, "Your reaction: heart") || strings.Contains(out, "👍") {
		t.Fatalf("unexpected console reaction menu: %q", out)
	}
}
