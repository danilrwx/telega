package telega

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestForwardSearchPopupFindsRecipientsOutsideSidebar(t *testing.T) {
	source := workflowChat(1, "Source chat")
	a := app{ready: true, open: true, chats: []chat{source}, log: []message{{id: 30, text: "Original message"}}}
	out := make(chan request, 1)
	a.input(key{text: "f"}, out)
	a.input(key{name: "paste", text: "@mar"}, out)
	a.flushDirectorySearch(out)

	stale := <-out
	a.input(key{text: "a"}, out)
	a.flushDirectorySearch(out)
	search := <-out

	a.receive(out, serverEvent{kind: stale.kind, searchID: stale.searchID, chats: []chat{workflowChat(9, "Stale result")}})

	if len(a.forwardChats()) != 0 || search.text != "mara" {
		t.Fatal("search should ignore results from the previous query")
	}

	api := tg.NewClient(invokeFunc(func(in bin.Encoder, response bin.Decoder) error {
		if req, ok := in.(*tg.ContactsSearchRequest); !ok || req.Q != "mara" {
			t.Fatalf("unexpected Telegram search: %+v", in)
		}

		*response.(*tg.ContactsFound) = tg.ContactsFound{
			Results: []tg.PeerClass{&tg.PeerUser{UserID: 4}},
			Users:   []tg.UserClass{&tg.User{ID: 4, FirstName: "Mara", LastName: "Chen", Username: "mara", AccessHash: 12}},
		}

		return nil
	}))

	matches, err := searchTelegramContacts(t.Context(), api, search.text)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(out, serverEvent{kind: search.kind, searchID: search.searchID, chats: matches})

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil {
		t.Fatal(err)
	}

	if got := screen.String(); !strings.Contains(got, "╭") || !strings.Contains(got, "Mara Chen · @mara") ||
		!strings.Contains(got, "Original message") || !strings.Contains(got, "/ @mara") {
		t.Fatalf("expected a recipient popup over the conversation: %q", got)
	}

	a.input(key{name: "enter"}, out)

	if !a.forwardConfirm || a.forwardTo.key != (peerKey{'u', 4}) || len(out) != 0 {
		t.Fatal("selecting a recipient must wait for confirmation")
	}

	a.input(key{name: "escape"}, out)
	a.input(key{name: "escape"}, out)

	if a.forwardIDs != nil || len(out) != 0 {
		t.Fatal("cancel should close the popup without forwarding")
	}
}

func TestSidebarSearchResetsAfterOpeningUsernameMatch(t *testing.T) {
	source := workflowChat(1, "Source")
	recipient := workflowChat(2, "Мира Чен")
	recipient.username = "mira_chen"
	a := app{ready: true, demo: true, chats: []chat{source, recipient}}
	a.input(key{text: "/"}, nil)
	a.input(key{name: "paste", text: "Мира"}, nil)

	if got := a.visibleChats(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("expected a match by display name: %v", got)
	}

	a.input(key{name: "escape"}, nil)
	a.input(key{text: "/"}, nil)
	a.input(key{name: "paste", text: "@MIRA_"}, nil)

	if got := a.visibleChats(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("expected a case-insensitive match by username: %v", got)
	}

	a.input(key{name: "enter"}, nil)

	if a.currentChat().key != recipient.key || a.chatQuery != "" || len(a.visibleChats()) != 2 {
		t.Fatal("opening the matched chat must restore the sidebar")
	}
}

func TestReactionShortcutTogglesWithoutConsumingSearchText(t *testing.T) {
	a := app{ready: true, open: true, chats: []chat{workflowChat(1, "Chat")}, log: []message{{id: 30}}}
	out := make(chan request, 1)
	a.input(key{text: "z"}, out)
	a.input(key{text: "я"}, out)

	if a.pickingReaction {
		t.Fatal("z on the Russian layout should close quick reactions")
	}

	a.input(key{text: "z"}, out)
	a.input(key{name: "tab"}, out)
	a.input(key{text: "z"}, out)

	if !a.reactionPickerOpen || a.reactionQuery != "z" {
		t.Fatal("z must be searchable in the emoji catalog")
	}
}
