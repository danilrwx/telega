package telega

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestNotificationsFollowVisibleHistory(t *testing.T) {
	c := chat{key: peerKey{'u', 4}, title: "Example Chat"}

	a := app{
		ready: true, open: true, focused: true, composing: true,
		chats: []chat{c}, log: []message{{id: 10, out: true}, {id: 9, out: true}},
		visibleMessages: 1,
	}

	incoming := serverEvent{kind: "new", chat: c.key, log: []message{{id: 11}}}
	if got := a.notification(incoming, true); got.title != "" {
		t.Fatal("message in the focused conversation should stay quiet")
	}

	a.searching = true
	if got := a.notification(incoming, true); got.title != c.title {
		t.Fatal("hidden history should not suppress a notice")
	}

	a.searching = false
	if got := a.notification(incoming, false); got.title != c.title {
		t.Fatal("a terminal that's too small to show history should not suppress a notice")
	}

	changed := serverEvent{kind: "reactions", chat: c.key, anchor: 9, reactionAt: 100}
	if got := a.notification(changed, true); got.title != c.title || got.body != "Reaction on your message" {
		t.Fatalf("reaction to a message outside the viewport should notify: %+v", got)
	}

	if got := a.notification(changed, true); got.title != "" {
		t.Fatal("a repeated unread reaction should not notify twice")
	}

	changed.anchor = 10
	if got := a.notification(changed, true); got.title != "" {
		t.Fatal("a reaction visible in the focused terminal should stay quiet")
	}

	a.focused = false

	changed.reactionAt++
	if got := a.notification(changed, true); got.title != c.title {
		t.Fatal("a new reaction in an unfocused terminal should notify")
	}
}

func TestNotificationMuteOutsideCurrentDialogPage(t *testing.T) {
	key := peerKey{'c', 7}
	source := notificationSource(key, tg.Entities{Channels: map[int64]*tg.Channel{
		7: {ID: 7, AccessHash: 77, Title: "Archived forum", Forum: true, Megagroup: true},
	}})
	a := app{ready: true}
	e := serverEvent{kind: "new", chat: key, source: source, log: []message{{id: 41, reply: messageReply{topic: 15}}}}
	notice := a.notification(e, true)

	check := a.noticeCheck(e, notice)
	if notice.title != "Archived forum" || check.topic != 15 || check.chat.input == nil {
		t.Fatalf("could not check the forum from an update: %+v", check)
	}

	for _, tc := range []struct {
		name        string
		topic       *int
		peer        bool
		defaultMute bool
		allowed     bool
		calls       int
	}{
		{name: "muted topic", topic: new(int(time.Now().Add(time.Hour).Unix())), calls: 1},
		{name: "unmuted topic overrides forum", topic: new(0), peer: true, allowed: true, calls: 1},
		{name: "muted forum", peer: true, calls: 2},
		{name: "muted group defaults", defaultMute: true, calls: 3},
		{name: "unmuted group defaults", allowed: true, calls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
				calls++
				target := input.(*tg.AccountGetNotifySettingsRequest).Peer
				settings := output.(*tg.PeerNotifySettings)
				muted := int(time.Now().Add(time.Hour).Unix())

				switch target := target.(type) {
				case *tg.InputNotifyForumTopic:
					if target.TopMsgID != 15 || target.Peer.(*tg.InputPeerChannel).AccessHash != 77 {
						t.Fatalf("wrong forum topic: %+v", target)
					}

					if tc.topic != nil {
						settings.SetMuteUntil(*tc.topic)
					}
				case *tg.InputNotifyPeer:
					if tc.peer {
						settings.SetMuteUntil(muted)
					}
				case *tg.InputNotifyChats:
					settings.SetMuteUntil(0)

					if tc.defaultMute {
						settings.SetMuteUntil(muted)
					}
				default:
					t.Fatalf("unexpected notification scope: %T", target)
				}

				return nil
			}))

			cache := make(map[chatPosition]muteSetting)
			for range 2 {
				if allowed := notificationAllowed(t.Context(), api, check, cache); allowed != tc.allowed {
					t.Fatalf("notification allowed = %v, want %v", allowed, tc.allowed)
				}
			}

			if calls != tc.calls {
				t.Fatalf("%d network calls, want %d", calls, tc.calls)
			}
		})
	}

	e.log[0].reply.topic = 0
	if got := a.noticeCheck(e, notice).topic; got != 1 {
		t.Fatalf("General topic = %d, want 1", got)
	}
}

func TestReactionNoticeUsesUnreadIncomingDates(t *testing.T) {
	events := make(chan serverEvent, 1)

	update := &tg.UpdateMessageReactions{
		Peer: &tg.PeerUser{UserID: 4}, MsgID: 9,
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 3}},
			RecentReactions: []tg.MessagePeerReaction{
				{Unread: true, Date: 100},
				{Unread: true, Date: 101},
				{Unread: true, My: true, Date: 102},
				{Date: 103},
			},
		},
	}
	if err := reactionUpdates(events)(t.Context(), tg.Entities{}, update); err != nil {
		t.Fatal(err)
	}

	if got := <-events; got.reactionAt != 101 || got.text != "👍 3" {
		t.Fatalf("wrong reaction update: %+v", got)
	}
}

func TestNotificationFailureShowsTerminalAlert(t *testing.T) {
	notices := make(chan desktopNotice, 1)
	failures := make(chan desktopNotice, 1)

	notice := desktopNotice{title: "Example Chat", body: "New Telegram message"}
	notices <- notice

	go notifyDesktop(t.Context(), filepath.Join(t.TempDir(), "missing-notifier"), notices, failures)

	select {
	case failed := <-failures:
		if failed != notice {
			t.Fatalf("failed alert was lost: %+v", failed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notifier failure was not reported")
	}

	a := app{ready: true, status: "No unread chats"}

	var screen bytes.Buffer

	a.alertInTerminal(&screen, notice)

	if err := paint(&screen, &a, 76, 16); err != nil {
		t.Fatal(err)
	}

	if output := screen.String(); !strings.HasPrefix(output, "\a") || !strings.Contains(output, "Terminal alert · Example Chat") {
		t.Fatal("notifier failure should ring and identify the chat in the terminal")
	}

	a.input(key{name: "focus"}, nil)

	if a.noticeStatus == "" {
		t.Fatal("gaining focus should keep the fallback visible")
	}
}

func TestNotificationRingsTmuxWithDesktopNotifier(t *testing.T) {
	a := app{}
	notice := desktopNotice{title: "Example Chat", body: "New message"}
	notices := make(chan desktopNotice, 1)

	var terminal bytes.Buffer

	a.deliverNotice(&terminal, notices, notice, true)

	if terminal.String() != "\a" || len(notices) != 1 || <-notices != notice {
		t.Fatal("tmux should get one bell as well as the desktop notice")
	}

	terminal.Reset()
	a.deliverNotice(&terminal, notices, notice, false)

	if terminal.Len() != 0 || len(notices) != 1 {
		t.Fatal("desktop notice outside tmux should stay quiet")
	}

	a.deliverNotice(&terminal, notices, notice, false)

	if terminal.String() != "\a" {
		t.Fatal("a full desktop queue should fall back to a terminal bell")
	}
}
