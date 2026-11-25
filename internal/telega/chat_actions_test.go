package telega

import (
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestChatActions(t *testing.T) {
	c := workflowChat(1, "Public chat")
	c.username = "public_chat"
	a := app{ready: true, chats: []chat{c}}
	out := make(chan request, 1)
	a.input(key{text: "x"}, out)
	a.input(key{name: "enter"}, out)

	req := <-out
	api := tg.NewClient(invokeFunc(func(in bin.Encoder, resp bin.Decoder) error {
		q, ok := in.(*tg.MessagesToggleDialogPinRequest)
		if !ok || !q.Pinned || q.Peer.(*tg.InputDialogPeer).Peer != c.input {
			t.Fatalf("unexpected pin request: %+v", in)
		}

		*resp.(*tg.BoolBox) = tg.BoolBox{Bool: &tg.BoolTrue{}}

		return nil
	}))

	if req.kind != "toggleChatPin" {
		t.Fatalf("unexpected action: %s", req.kind)
	}

	if err := toggleChatPin(t.Context(), api, req); err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: req.kind, chat: c.key, step: 1})
	a.input(key{text: "x"}, out)
	a.input(key{name: "down"}, out)
	a.input(key{name: "down"}, out)
	a.input(key{name: "enter"}, out)

	if a.copyText != "https://t.me/public_chat" || !a.chats[0].unread.pinned {
		t.Fatal("pin state or copied public link is wrong")
	}
}

func TestToggleChatMute(t *testing.T) {
	c := workflowChat(1, "Chat")
	muted := false

	api := tg.NewClient(invokeFunc(func(in bin.Encoder, resp bin.Decoder) error {
		switch q := in.(type) {
		case *tg.AccountGetNotifySettingsRequest:
			settings := resp.(*tg.PeerNotifySettings)
			if muted {
				settings.SetMuteUntil(100)
			}
		case *tg.AccountUpdateNotifySettingsRequest:
			until, set := q.Settings.GetMuteUntil()
			if !set || (until > 0) == muted {
				t.Fatalf("mute was not toggled: %+v", q.Settings)
			}

			muted = !muted
			*resp.(*tg.BoolBox) = tg.BoolBox{Bool: &tg.BoolTrue{}}
		default:
			t.Fatalf("unexpected Telegram request: %+v", in)
		}

		return nil
	}))
	for _, want := range []string{"Chat notifications muted", "Chat notifications unmuted"} {
		if got, err := toggleChatMute(t.Context(), api, c, time.Unix(10, 0)); err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
}
