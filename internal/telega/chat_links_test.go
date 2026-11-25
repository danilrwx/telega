package telega

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestParseChatLink(t *testing.T) {
	cases := map[string]chatLinkTarget{
		"@Open_Group":                          {username: "open_group"},
		"open_group":                           {username: "open_group"},
		"  t.me/Open_Group/  ":                 {username: "open_group"},
		"https://T.ME/Open_Group":              {username: "open_group"},
		"http://t.me/+Some-Invite_01":          {invite: "Some-Invite_01"},
		"https://t.me/joinchat/CaseSensitive/": {invite: "CaseSensitive"},
	}
	for raw, want := range cases {
		if got, ok := parseChatLink(raw); !ok || got != want {
			t.Errorf("parseChatLink(%q) = %#v, %v; want %#v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"", "@", "//t.me/example", "tg://resolve?domain=example", "https://t.me.evil/example",
		"https://t.me@evil.example/example", "https://evil.example@t.me/example", "https://t.me:443/example",
		"https://t.me./example", "https://t.me/example/123", "https://t.me/s/example", "https://t.me/c/1/2",
		"https://t.me/share/url", "https://t.me/share", "https://t.me/example?start=test", "https://t.me/example#",
		"https://t.me/+491234567", "https://t.me/%2BSecret", "https://t.me/+Ab%2Fc", "https://t.me/+a/b",
		"https://t.me/joinchat", "https://t.me/joinchat/", "https://t.me//example", "https://t.me/example//",
		"https://t.me/+", "https://t.me/+hash=", "https://t.me/na\nme", "https://t.me/сhannel", "@" + strings.Repeat("a", 33),
	} {
		if got, ok := parseChatLink(raw); ok {
			t.Errorf("accepted unsupported link %q: %#v", raw, got)
		}
	}
}

type fakeChatLinkAPI struct {
	resolve      func(*tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error)
	check        func(string) (tg.ChatInviteClass, error)
	join         func(tg.InputChannelClass) (tg.MessagesChatInviteJoinResultClass, error)
	importInvite func(string) (tg.MessagesChatInviteJoinResultClass, error)
}

func (f fakeChatLinkAPI) ContactsResolveUsername(_ context.Context, r *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	return f.resolve(r)
}
func (f fakeChatLinkAPI) MessagesCheckChatInvite(_ context.Context, hash string) (tg.ChatInviteClass, error) {
	return f.check(hash)
}
func (f fakeChatLinkAPI) ChannelsJoinChannel(_ context.Context, channel tg.InputChannelClass) (tg.MessagesChatInviteJoinResultClass, error) {
	return f.join(channel)
}
func (f fakeChatLinkAPI) MessagesImportChatInvite(_ context.Context, hash string) (tg.MessagesChatInviteJoinResultClass, error) {
	return f.importInvite(hash)
}

func TestChatLinkPublicOpenAndJoin(t *testing.T) {
	for _, join := range []bool{false, true} {
		name := "open without joining"
		if join {
			name = "explicitly join"
		}
		t.Run(name, func(t *testing.T) {
			a := app{ready: true}
			out := make(chan request, 3)
			joins := 0
			api := fakeChatLinkAPI{
				resolve: func(r *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
					if r.Username != "public_chat" {
						t.Fatalf("username = %q", r.Username)
					}
					return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 22}, Chats: []tg.ChatClass{
						&tg.Channel{ID: 12, AccessHash: 4},
						&tg.Channel{ID: 22, AccessHash: 909, Left: true, Title: "Public Test"},
					}}, nil
				},
				join: func(c tg.InputChannelClass) (tg.MessagesChatInviteJoinResultClass, error) {
					joins++
					if got, ok := c.(*tg.InputChannel); !ok || got.ChannelID != 22 || got.AccessHash != 909 {
						t.Fatalf("join target = %#v", c)
					}
					return &tg.MessagesChatInviteJoinResultOk{}, nil
				},
			}
			if !a.tryChatLink(out, "https://t.me/Public_Chat") {
				t.Fatal("public link not handled")
			}
			req := nextChatLinkRequest(t, out, "chatLinkResolve")
			a.receiveChatLink(out, runChatLink(t.Context(), api, req))
			if a.chatLink == nil || a.chatLink.phase != chatLinkChoose || a.chatLink.result.title != "Public Test" {
				t.Fatalf("incorrect public preview: %#v", a.chatLink)
			}
			if join {
				a.chatLinkInput(out, key{text: "j"})
				a.chatLinkInput(out, key{name: "enter"})
				a.chatLinkInput(out, key{name: "paste", text: "y"})
				if a.chatLink.phase != chatLinkConfirm || len(out) != 0 {
					t.Fatal("enter or paste joined without explicit confirmation")
				}
				a.chatLinkInput(out, key{text: "y"})
				req = nextChatLinkRequest(t, out, "chatLinkJoin")
				a.receiveChatLink(out, runChatLink(t.Context(), api, req))
			} else {
				a.chatLinkInput(out, key{name: "enter"})
			}
			req = nextChatLinkRequest(t, out, "history")
			if req.chat.key != (peerKey{'c', 22}) || !a.open || a.chatLink != nil || (joins == 1) != join {
				t.Fatalf("opened = %v, history = %#v, joins = %d", a.open, req.chat.key, joins)
			}
		})
	}
}

func TestChatLinkInviteConfirmationAndApproval(t *testing.T) {
	a := app{ready: true}
	out := make(chan request, 3)
	const hash = "Invite-Token_1"
	api := fakeChatLinkAPI{
		check: func(got string) (tg.ChatInviteClass, error) {
			if got != hash {
				t.Fatalf("check hash = %q", got)
			}
			return &tg.ChatInvite{Title: "Private Group", RequestNeeded: true}, nil
		},
		importInvite: func(got string) (tg.MessagesChatInviteJoinResultClass, error) {
			if got != hash {
				t.Fatalf("import hash = %q", got)
			}
			return nil, &tgerr.Error{Type: "INVITE_REQUEST_SENT"}
		},
	}
	start := func() {
		t.Helper()
		a.openChatLinkPrompt()
		a.chatLinkInput(out, key{name: "paste", text: "t.me/+" + hash})
		a.chatLinkInput(out, key{name: "enter"})
		a.receiveChatLink(out, runChatLink(t.Context(), api, nextChatLinkRequest(t, out, "chatLinkResolve")))
		title, lines, footer := a.chatLinkOverlay()
		if !strings.Contains(title, "Request to join") || len(lines) != 1 || lines[0] != "Private Group" || strings.Contains(footer+strings.Join(lines, ""), hash) {
			t.Fatalf("bad confirmation: %s %v %s", title, lines, footer)
		}
	}
	start()
	a.chatLinkInput(out, key{name: "enter"})
	a.chatLinkInput(out, key{name: "escape"})
	if len(out) != 0 || a.chatLink != nil || a.open {
		t.Fatal("cancelled invite created a request or opened a chat")
	}
	start()
	a.chatLinkInput(out, key{text: "y"})
	a.receiveChatLink(out, runChatLink(t.Context(), api, nextChatLinkRequest(t, out, "chatLinkJoin")))
	if a.open || a.chatLink != nil || a.pending != "" || !strings.Contains(a.status, "waiting for approval") || len(out) != 0 {
		t.Fatalf("invite request state: %q, pending = %q", a.status, a.pending)
	}
}

func TestChatLinkInviteOpenAlreadyJoinedOrAfterImport(t *testing.T) {
	for _, member := range []bool{true, false} {
		a := app{ready: true}
		out := make(chan request, 3)
		group := &tg.Chat{ID: 5, Title: "Invite Group"}
		api := fakeChatLinkAPI{
			check: func(string) (tg.ChatInviteClass, error) {
				if member {
					return &tg.ChatInviteAlready{Chat: group}, nil
				}
				return &tg.ChatInvite{Title: group.Title}, nil
			},
		}
		if !member {
			api.importInvite = func(string) (tg.MessagesChatInviteJoinResultClass, error) {
				return &tg.MessagesChatInviteJoinResultOk{Updates: &tg.UpdatesCombined{Chats: []tg.ChatClass{group}}}, nil
			}
		}
		a.tryChatLink(out, "t.me/joinchat/TestHash")
		a.receiveChatLink(out, runChatLink(t.Context(), api, nextChatLinkRequest(t, out, "chatLinkResolve")))
		if member {
			a.chatLinkInput(out, key{name: "enter"})
		} else {
			a.chatLinkInput(out, key{text: "y"})
			a.receiveChatLink(out, runChatLink(t.Context(), api, nextChatLinkRequest(t, out, "chatLinkJoin")))
		}
		if got := nextChatLinkRequest(t, out, "history").chat.key; got != (peerKey{'g', 5}) {
			t.Fatalf("opened %v", got)
		}
	}
}

func TestChatLinkLookupErrorDoesNotExposeInvite(t *testing.T) {
	a := app{ready: true}
	out := make(chan request, 1)
	a.tryChatLink(out, "t.me/+SecretInvite")
	req := nextChatLinkRequest(t, out, "chatLinkResolve")
	api := fakeChatLinkAPI{check: func(string) (tg.ChatInviteClass, error) { return nil, errors.New("lookup failed for SecretInvite") }}
	a.receiveChatLink(out, runChatLink(t.Context(), api, req))
	if a.chatLink != nil || a.pending != "" || a.status == "" || strings.Contains(a.status, "SecretInvite") {
		t.Fatalf("unexpected error state: %q", a.status)
	}
}

func nextChatLinkRequest(t *testing.T, out <-chan request, kind string) request {
	t.Helper()
	select {
	case req := <-out:
		if req.kind != kind {
			t.Fatalf("request = %q, want %q", req.kind, kind)
		}
		return req
	default:
		t.Fatalf("no %q request", kind)
		return request{}
	}
}
