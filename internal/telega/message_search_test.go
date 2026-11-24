package telega

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestGlobalMessageSearchUsesTelegramAndJumpsToMatchedChat(t *testing.T) {
	current := workflowChat(1, "Current")
	a := app{ready: true, open: true, chats: []chat{current}, log: []message{{id: 40, text: "Current history"}}}
	out := make(chan request, 1)
	a.input(key{name: "messageSearch"}, out)
	a.input(key{name: "paste", text: "needle"}, out)
	a.input(key{name: "enter"}, out)
	req := <-out
	api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
		q, ok := in.(*tg.MessagesSearchGlobalRequest)
		if !ok || q.Q != "needle" || q.OffsetID != 0 {
			t.Fatalf("wrong global search: %+v", in)
		}

		page := &tg.MessagesMessagesSlice{
			Messages: []tg.MessageClass{&tg.Message{ID: 13, PeerID: &tg.PeerUser{UserID: 9}, Date: 500, Message: "needle in old chat"}},
			Users:    []tg.UserClass{&tg.User{ID: 9, FirstName: "Mira", AccessHash: 123}},
		}
		page.SetNextRate(555)
		out.(*tg.MessagesMessagesBox).Messages = page

		return nil
	}))

	hits, next, err := searchMessages(t.Context(), api, req)
	if err != nil || next.id != 13 || next.rate != 555 {
		t.Fatalf("unexpected response %v %+v %v", hits, next, err)
	}

	a.receive(out, serverEvent{kind: req.kind, searchID: req.searchID, messageSearch: req.messageSearch, messageHits: hits, searchCursor: next})

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil || !strings.Contains(screen.String(), "Search messages · All chats") ||
		!strings.Contains(screen.String(), "needle in old chat") {
		t.Fatalf("missing result: %v %s", err, screen.String())
	}

	a.input(key{name: "enter"}, out)

	jump := <-out
	if jump.kind != "jump" || jump.anchor != 13 || jump.chat.key != (peerKey{'u', 9}) {
		t.Fatalf("unexpected jump: %+v", jump)
	}
}

func TestMediaSearchUsesServerFiltersWithoutText(t *testing.T) {
	current := workflowChat(1, "Current")
	filters := []tg.MessagesFilterClass{
		&tg.InputMessagesFilterPhotos{}, &tg.InputMessagesFilterDocument{},
		&tg.InputMessagesFilterURL{}, &tg.InputMessagesFilterVoice{},
	}

	for i, want := range filters {
		for _, global := range []bool{false, true} {
			a := app{ready: true, open: true, chats: []chat{current}}
			out := make(chan request, 1)

			a.beginMessageSearch(global)

			for range i + 1 {
				a.input(key{name: "shift+tab"}, out)
			}

			a.input(key{name: "enter"}, out)

			if len(out) == 0 {
				t.Fatalf("filter %T needs to support an empty query", want)
			}

			req := <-out

			api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
				var filter tg.MessagesFilterClass

				switch query := in.(type) {
				case *tg.MessagesSearchGlobalRequest:
					filter = query.Filter
					if !global || query.Q != "" {
						t.Fatalf("unexpected global query: %+v", query)
					}
				case *tg.MessagesSearchRequest:
					filter = query.Filter
					if global || query.Q != "" || query.Peer != current.input {
						t.Fatalf("unexpected scoped query: %+v", query)
					}
				default:
					t.Fatalf("unexpected query: %T", in)
				}

				if reflect.TypeOf(filter) != reflect.TypeOf(want) {
					t.Fatalf("got %T, want %T", filter, want)
				}

				out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{}

				return nil
			}))

			if _, _, err := searchMessages(t.Context(), api, req); err != nil {
				t.Fatal(err)
			}
		}
	}
}
