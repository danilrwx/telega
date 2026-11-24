package telega

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestReactionAuthorsPaginationAndDialog(t *testing.T) {
	c := chat{key: peerKey{'g', 2}, input: &tg.InputPeerChat{ChatID: 2}}
	a := app{open: true, ready: true, chats: []chat{c}, log: []message{{id: 12}}}
	out := make(chan request, 1)
	a.input(key{text: "Я"}, out)

	if len(out) != 1 || a.reactionAuthors == nil {
		t.Fatal("Shift+Z on the Russian layout should open the reaction authors dialog")
	}

	req := <-out
	calls := 0

	rows, status := fetchReactionAuthors(t.Context(), req, func(_ context.Context, q *tg.MessagesGetMessageReactionsListRequest) (*tg.MessagesMessageReactionsList, error) {
		calls++

		if q.ID != 12 || q.Peer != c.input {
			t.Fatalf("wrong message or peer: %+v", q)
		}

		switch q.Offset {
		case "":
			return &tg.MessagesMessageReactionsList{
				Reactions: []tg.MessagePeerReaction{
					{PeerID: &tg.PeerUser{UserID: 1}, Reaction: &tg.ReactionEmoji{Emoticon: "👀"}},
					{PeerID: &tg.PeerUser{UserID: 1}, Reaction: &tg.ReactionCustomEmoji{DocumentID: 42}},
				},
				Users:      []tg.UserClass{&tg.User{ID: 1, FirstName: "Mira", LastName: "Chen"}},
				NextOffset: "next",
			}, nil
		case "next":
			return &tg.MessagesMessageReactionsList{Reactions: []tg.MessagePeerReaction{
				{My: true, PeerID: &tg.PeerUser{UserID: 3}, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}},
			}}, nil
		default:
			t.Fatalf("unexpected offset %q", q.Offset)
			return nil, nil
		}
	}, func(_ context.Context, ids []int64) ([]tg.DocumentClass, error) {
		if !slices.Equal(ids, []int64{42}) {
			t.Fatalf("wrong custom emoji IDs: %v", ids)
		}

		return []tg.DocumentClass{&tg.Document{
			ID: 42, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeCustomEmoji{Alt: "⭐"}},
		}}, nil
	})
	if calls != 2 || status != "" || !slices.Equal(rows, []string{"⭐ Custom emoji 42  Mira Chen", "👀  Mira Chen", "👍  You"}) {
		t.Fatalf("unexpected reaction authors: %v, status %q, %d requests", rows, status, calls)
	}

	response := serverEvent{kind: req.kind, reactionAuthors: req.reactionAuthors, reactionAuthorRows: rows}
	a.receive(out, response)
	a.input(key{text: "о"}, out)

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil || !strings.Contains(screen.String(), "👀  Mira Chen") ||
		a.reactionAuthors.Selected != 1 {
		t.Fatalf("unexpected reaction authors screen: %v %s", err, screen.String())
	}

	a.input(key{name: "escape"}, out)
	a.receive(out, response)

	if a.reactionAuthors != nil || !a.open {
		t.Fatal("Escape should return to the conversation and ignore stale results")
	}
}

func TestReactionAuthorsRespectBroadcastPrivacy(t *testing.T) {
	rows, status := fetchReactionAuthors(t.Context(), request{}, func(_ context.Context, _ *tg.MessagesGetMessageReactionsListRequest) (*tg.MessagesMessageReactionsList, error) {
		return nil, tgerr.New(403, "BROADCAST_FORBIDDEN")
	}, nil)
	if len(rows) != 0 || !strings.Contains(status, "Telegram hides reaction authors") {
		t.Fatalf("expected an anonymous channel status, got %v %q", rows, status)
	}
}
