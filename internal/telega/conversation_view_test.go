package telega

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

func TestChannelMessageBlocksKeepTheirContentTogether(t *testing.T) {
	morning := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	a := app{
		ready: true, open: true, chats: []chat{{key: peerKey{'c', 2}, title: "Release Notes"}},
		log: []message{
			{
				id: 2, date: morning.Add(time.Minute), text: strings.Repeat("x", 115),
				attachment: &attachment{name: "board.jpg", kind: "Photo"},
				reply:      messageReply{id: 1, quote: "Earlier update"}, reactions: "👍 2",
			},
			{id: 1, date: morning, authorName: "Ada", text: "[Attachment]", attachment: &attachment{name: "bench.mp4", kind: "Video"}},
		},
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 150, 20); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	for _, fragment := range []string{
		"\x1b[12;33H09:01", "[J] Ada · Earlier update",
		"[Photo] board.jpg", "\x1b[14;41H" + strings.Repeat("x", 110) + "\x1b[0m", "\x1b[15;41Hxxxxx\x1b[0m", "👍 2",
		"\x1b[9;33H09:00", "\x1b[9;39HAda", "[Video] bench.mp4",
	} {
		if !strings.Contains(out, fragment) {
			t.Errorf("conversation lost %q", fragment)
		}
	}

	if count := strings.Count(out, "Release Notes"); count != 2 {
		t.Fatalf("channel title should appear only in the chat list and the conversation title; got %d", count)
	}

	if count := strings.Count(out, "\x1b[36m│\x1b[0m"); count != 5 {
		t.Fatalf("selected block should place the same line next to all five rows; got %d", count)
	}
}

func TestEditedChannelPostCanHideEditMark(t *testing.T) {
	channel := peerKey{'c', 2}
	post := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 2}, Message: "Announcement"}
	post.SetEditDate(123)

	_, msg, _ := unwrapMessage(post)
	a := app{ready: true, open: true, chats: []chat{{key: channel, title: "News"}}, log: []message{msg}}

	var screen bytes.Buffer
	if err := paint(&screen, &a, minTerminalCols, 12); err != nil {
		t.Fatal(err)
	}

	if out := screen.String(); !strings.Contains(out, "Hedited") || strings.Contains(out, "e edit") {
		t.Fatalf("wrong mark or shortcut for an incoming post: %q", out)
	}

	post.EditHide = true
	_, msg, _ = unwrapMessage(post)
	a.receive(nil, serverEvent{kind: "live", chat: channel, log: []message{msg}})
	screen.Reset()

	if err := paint(&screen, &a, minTerminalCols, 12); err != nil || strings.Contains(screen.String(), "Hedited") {
		t.Fatalf("Telegram's hidden edit should remove the mark: %v", err)
	}
}

func TestDirectMessagesUseSeparateBlocksForEachMessage(t *testing.T) {
	morning := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	a := app{
		ready: true, open: true, chats: []chat{{key: peerKey{'u', 2}, title: "Ada Example"}},
		log: []message{
			{id: 3, date: morning.Add(2 * time.Minute), text: "Third", out: true},
			{id: 2, date: morning.Add(time.Minute), text: "Second", out: true},
			{id: 1, date: morning, text: "First"},
		},
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 16); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	for _, stamp := range []string{"\x1b[11;29H09:02", "\x1b[8;29H09:01", "\x1b[5;29H09:00", "→ You", "← Ada"} {
		if !strings.Contains(out, stamp) {
			t.Errorf("private message block is missing %q", stamp)
		}
	}

	if count := strings.Count(out, "\x1b[36m│\x1b[0m") + strings.Count(out, "\x1b[2m│\x1b[0m"); count != 3 {
		t.Fatalf("each private message should have a line beside its body; got %d", count)
	}
}

func TestConversationShowsDateAndUnreadSeparators(t *testing.T) {
	morning := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	a := app{
		ready: true, open: true,
		chats: []chat{{key: peerKey{'u', 2}, title: "Ada", unread: unreadState{count: 2, inboxID: 1}}},
		log: []message{
			{id: 3, date: morning.Add(time.Minute), text: "Third"},
			{id: 2, date: morning, text: "Second"},
			{id: 1, date: morning.Add(-24 * time.Hour), text: "First"},
		},
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 20); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	for _, part := range []string{"\x1b[5;47H── 2026-06-08 ──", "\x1b[9;47H── 2026-06-09 ──", "\x1b[10;49H── Unread ──"} {
		if !strings.Contains(out, part) {
			t.Fatalf("conversation missing %q", part)
		}
	}

	if strings.Count(out, "2026-06-09") != 1 || a.visibleMessages != 3 {
		t.Fatal("date separators should appear once per day and preserve the visible message count")
	}
}

func TestOutgoingChecksUpdateWithReadReceipt(t *testing.T) {
	peer := peerKey{'u', 2}
	at := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	a := app{
		ready: true, open: true, chats: []chat{{key: peer, title: "Ada", readOutboxID: 1}},
		log: []message{
			{id: 3, date: at, text: "Third"},
			{id: 2, date: at, text: "Second", out: true, edited: true},
			{id: 1, date: at, text: "First", out: true},
		},
	}

	render := func() string {
		var screen bytes.Buffer
		if err := paint(&screen, &a, minTerminalCols, 18); err != nil {
			t.Fatal(err)
		}

		return screen.String()
	}

	if out := render(); !strings.Contains(out, " · ✓ · edited") || strings.Count(out, "✓") != 3 {
		t.Fatalf("expected one sent and one read outgoing message: %q", out)
	}

	a.receive(nil, serverEvent{kind: "readOutbox", chat: peer, anchor: 2})
	a.receive(nil, serverEvent{kind: "readOutbox", chat: peer, anchor: 1})

	if out := render(); !strings.Contains(out, " · ✓✓ · edited") || strings.Count(out, "✓") != 4 {
		t.Fatalf("expected the live read receipt to advance: %q", out)
	}
}

func TestDMDisplaySettingsAlignOutgoingAndHideNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	a := app{
		ready: true, open: true,
		chats: []chat{{key: peerKey{'u', 2}, title: "Ada Example"}},
		log:   []message{{id: 2, out: true, text: "Outgoing 🙂"}, {id: 1, text: "Incoming"}},
	}

	column := func(frame, text string) int {
		t.Helper()

		match := regexp.MustCompile(`\x1b\[\d+;(\d+)H` + regexp.QuoteMeta(text)).FindStringSubmatch(frame)
		if match == nil {
			t.Fatalf("missing message body %q", text)
		}

		x, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatal(err)
		}

		return x
	}
	painted := func() string {
		t.Helper()

		var frame bytes.Buffer
		if err := paint(&frame, &a, 100, 24); err != nil {
			t.Fatal(err)
		}

		return frame.String()
	}

	before := painted()
	if column(before, "Outgoing 🙂") != column(before, "Incoming") || !strings.Contains(before, "→ You") {
		t.Fatal("default layout should retain names and left-aligned messages")
	}

	a.input(key{name: "alt+s"}, nil)
	a.input(key{text: "j"}, nil)
	a.input(key{name: "enter"}, nil)
	a.input(key{text: "j"}, nil)
	a.input(key{name: "enter"}, nil)
	a.input(key{name: "escape"}, nil)

	after := painted()
	if column(after, "Outgoing 🙂") <= column(after, "Incoming") ||
		column(after, "Incoming") != column(before, "Incoming") ||
		strings.Contains(after, "→ You") || strings.Contains(after, "← Ada") {
		t.Fatal("outgoing should move right while names disappear and incoming keeps its position")
	}

	var saved displaySettings
	if err := tui.ReadSettings("telega", &saved); err != nil || !saved.DMRight || !saved.HideDMNames {
		t.Fatalf("expected both settings to persist: %+v (%v)", saved, err)
	}
}

func TestHideChatListWhenOpen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	a := app{
		ready: true, open: true,
		chats: []chat{{key: peerKey{'u', 2}, title: "Ada"}, {key: peerKey{'u', 3}, title: "Other chat"}},
		log:   []message{{id: 1, text: "Message body"}},
	}
	painted := func() string {
		t.Helper()

		var frame bytes.Buffer
		if err := paint(&frame, &a, 100, 24); err != nil {
			t.Fatal(err)
		}

		return frame.String()
	}

	if !strings.Contains(painted(), "Other chat") {
		t.Fatal("the sidebar should be visible by default")
	}

	a.input(key{name: "alt+s"}, nil)

	for range displaySettingsCount - 1 {
		a.input(key{text: "j"}, nil)
	}

	a.input(key{name: "enter"}, nil)
	a.input(key{name: "escape"}, nil)

	if frame := painted(); strings.Contains(frame, "Other chat") || !strings.Contains(frame, "Message body") {
		t.Fatal("the open view should contain only the conversation")
	}

	a.input(key{text: "h"}, nil)

	if frame := painted(); !strings.Contains(frame, "Other chat") || strings.Contains(frame, "Message body") {
		t.Fatal("returning from the chat should show the chat list")
	}

	var saved displaySettings
	if err := tui.ReadSettings("telega", &saved); err != nil || !saved.HideChatListWhenOpen {
		t.Fatalf("expected the sidebar option to persist: %+v (%v)", saved, err)
	}
}
