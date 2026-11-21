package telega

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestChannelMessageBlocksKeepTheirContentTogether(t *testing.T) {
	morning := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	a := app{
		ready: true, open: true, chats: []chat{{key: peerKey{'c', 2}, title: "Release Notes"}},
		log: []message{
			{
				id: 2, date: morning.Add(time.Minute), text: strings.Repeat("x", 95),
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
		"\x1b[13;32H09:01", "[J] Ada · Earlier update",
		"[Photo] board.jpg", strings.Repeat("x", 90) + "\x1b[0m", "👍 2",
		"\x1b[10;32H09:00", "\x1b[10;38HAda", "[Video] bench.mp4",
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
	for _, stamp := range []string{"\x1b[12;28H09:02", "\x1b[9;28H09:01", "\x1b[6;28H09:00", "→ You", "← Ada"} {
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
	for _, part := range []string{"\x1b[6;46H── 2026-06-08 ──", "\x1b[10;46H── 2026-06-09 ──", "\x1b[11;48H── Unread ──"} {
		if !strings.Contains(out, part) {
			t.Fatalf("conversation missing %q", part)
		}
	}

	if strings.Count(out, "2026-06-09") != 1 || a.visibleMessages != 3 {
		t.Fatal("date separators should appear once per day and preserve the visible message count")
	}
}
