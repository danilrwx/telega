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
	}, nil)
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
	}, nil, nil)
	if len(rows) != 0 || !strings.Contains(status, "Telegram hides reaction authors") {
		t.Fatalf("expected an anonymous channel status, got %v %q", rows, status)
	}
}

func TestReactionAuthorsInPrivateChat(t *testing.T) {
	peer := &tg.PeerUser{UserID: 7}
	raw := &tg.Message{ID: 12, PeerID: peer}
	raw.SetReactions(tg.MessageReactions{RecentReactions: []tg.MessagePeerReaction{
		{PeerID: peer, Reaction: &tg.ReactionEmoji{Emoticon: "👀"}},
		{My: true, PeerID: &tg.PeerUser{UserID: 1}, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}},
	}})
	_, msg, _ := unwrapMessage(raw)
	a := app{open: true, ready: true, chats: []chat{{key: peerKey{'u', 7}, title: "Mira"}}, log: []message{msg}}
	out := make(chan request, 1)

	check := func(want []string) {
		t.Helper()
		a.showReactionAuthors(out)

		rows, status := fetchReactionAuthors(t.Context(), <-out, func(context.Context, *tg.MessagesGetMessageReactionsListRequest) (*tg.MessagesMessageReactionsList, error) {
			t.Fatal("private chats must not request the reaction authors list")
			return nil, nil
		}, nil, nil)
		if status != "" || !slices.Equal(rows, want) {
			t.Fatalf("reaction authors: %v, status %q; want %v", rows, status, want)
		}
	}

	check([]string{"👀  Mira", "👍  You"})
	a.replaceLoadedMessage(0, message{id: 12, text: "Edited"})
	check([]string{"👀  Mira", "👍  You"})

	events := make(chan serverEvent, 1)

	err := reactionUpdates(events)(t.Context(), tg.Entities{}, &tg.UpdateMessageReactions{
		Peer: peer, MsgID: 12, Reactions: tg.MessageReactions{RecentReactions: []tg.MessagePeerReaction{
			{PeerID: peer, Reaction: &tg.ReactionEmoji{Emoticon: "🔥"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	a.receive(out, <-events)
	check([]string{"🔥  Mira"})

	a.log[0].recentReactions = nil

	check(nil)
}

func TestReactionAuthorsRefreshIncompletePrivateMessage(t *testing.T) {
	peer := peerKey{'u', 7}
	a := app{ready: true, open: true, chats: []chat{{key: peer, title: "Mira"}}, log: []message{{
		id: 12, reactions: "👍 1", hasReactions: true,
		recentReactions: []tg.MessagePeerReaction{{PeerID: &tg.PeerUser{UserID: 7}, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}}},
	}}}
	a.receive(nil, serverEvent{kind: "reactions", chat: peer, anchor: 12, text: "👍 1"})

	out := make(chan request, 1)
	a.showReactionAuthors(out)
	req := <-out

	for _, scenario := range []struct {
		name      string
		reactions tg.MessageReactions
		want      []string
		status    string
	}{
		{"refreshed", tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}}, RecentReactions: []tg.MessagePeerReaction{{PeerID: &tg.PeerUser{UserID: 7}, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}}}}, []string{"👍  Mira"}, ""},
		{"still incomplete", tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}}}, nil, "Reaction authors are unavailable for this message"},
		{"removed", tg.MessageReactions{}, nil, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rows, status := fetchReactionAuthors(t.Context(), req, nil, nil, func(_ context.Context, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
				if len(ids) != 1 || ids[0].(*tg.InputMessageID).ID != 12 {
					t.Fatalf("wrong message requested: %v", ids)
				}

				msg := &tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 7}}
				msg.SetReactions(scenario.reactions)

				return &tg.MessagesMessages{Messages: []tg.MessageClass{msg}}, nil
			})
			if status != scenario.status || !slices.Equal(rows, scenario.want) {
				t.Fatalf("authors: %v, status %q; want %v, %q", rows, status, scenario.want, scenario.status)
			}
		})
	}
}
