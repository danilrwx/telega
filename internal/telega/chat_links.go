package telega

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const chatLinkInputLimit = 384

type chatLinkTarget struct {
	username, invite string
}

var (
	chatUsername   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
	chatInviteHash = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
)

func parseChatLink(raw string) (chatLinkTarget, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) > chatLinkInputLimit || strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return chatLinkTarget{}, false
	}

	if name, ok := strings.CutPrefix(raw, "@"); ok {
		return publicChatLink(name)
	}

	if !strings.ContainsAny(raw, "/:.") {
		return publicChatLink(raw)
	}

	// Do not reinterpret message permalinks, bot parameters, proxies, or other deep links.
	if strings.ContainsAny(raw, "%?#\\") || strings.HasPrefix(raw, "/") {
		return chatLinkTarget{}, false
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || !strings.EqualFold(u.Host, "t.me") {
		return chatLinkTarget{}, false
	}

	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	if hash, ok := strings.CutPrefix(path, "+"); ok {
		if chatInviteHash.MatchString(hash) && strings.ContainsAny(hash, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-") {
			return chatLinkTarget{invite: hash}, true
		}
		// +digits denotes a Telegram phone-number link, not an invite.
		return chatLinkTarget{}, false
	}

	if kind, hash, ok := strings.Cut(path, "/"); ok {
		if strings.EqualFold(kind, "joinchat") && chatInviteHash.MatchString(hash) {
			return chatLinkTarget{invite: hash}, true
		}

		return chatLinkTarget{}, false
	}

	return publicChatLink(path)
}

func publicChatLink(name string) (chatLinkTarget, bool) {
	if !chatUsername.MatchString(name) {
		return chatLinkTarget{}, false
	}

	name = strings.ToLower(name)
	switch name {
	case "a", "addemoji", "addlist", "addstickers", "addtheme", "auth", "bg", "boost", "c", "confirmphone", "contact", "folder", "giftcode", "invoice", "iv", "joinchat", "login", "m", "nft", "proxy", "s", "setlanguage", "share", "socks", "web":
		return chatLinkTarget{}, false
	}

	return chatLinkTarget{username: name}, true
}

type chatLinkAPI interface {
	ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error)
	MessagesCheckChatInvite(context.Context, string) (tg.ChatInviteClass, error)
	ChannelsJoinChannel(context.Context, tg.InputChannelClass) (tg.MessagesChatInviteJoinResultClass, error)
	MessagesImportChatInvite(context.Context, string) (tg.MessagesChatInviteJoinResultClass, error)
}

type chatLinkResult struct {
	target            *chatLinkTarget
	chat              chat
	title             string
	canJoin, approval bool
}

func runChatLink(ctx context.Context, api chatLinkAPI, req request) serverEvent {
	result := &chatLinkResult{target: req.chatLink}

	e := serverEvent{kind: req.kind, chatLink: result}
	if req.chatLink == nil {
		e.hint = "No Telegram chat link selected"
		return e
	}

	var err error

	switch req.kind {
	case "chatLinkResolve":
		e.hint, err = resolveChatLink(ctx, api, result)
	case "chatLinkJoin":
		e.hint, err = joinChatLink(ctx, api, req.chat, result)
	}

	// Do not include raw RPC errors or invite tokens in user-facing status.
	switch {
	case tgerr.Is(err, "INVITE_REQUEST_SENT"):
		e.hint = "Join request sent · waiting for approval"
	case tgerr.Is(err, "INVITE_HASH_EMPTY", "INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED"):
		e.hint = "This invite is invalid or expired"
	case tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID"):
		e.hint = "Telegram username not found"
	case err != nil && req.kind == "chatLinkJoin":
		e.hint = "Could not confirm the join with Telegram"
	case err != nil:
		e.hint = "Could not look up this chat on Telegram"
	}

	return e
}

func resolveChatLink(ctx context.Context, api chatLinkAPI, r *chatLinkResult) (string, error) {
	if r.target.invite != "" {
		info, err := api.MessagesCheckChatInvite(ctx, r.target.invite)
		if err != nil {
			return "", err
		}

		switch invite := info.(type) {
		case *tg.ChatInviteAlready:
			r.chat = inviteChat(invite.Chat)
		case *tg.ChatInvite:
			r.title, r.approval = invite.Title, invite.RequestNeeded
			if _, paid := invite.GetSubscriptionPricing(); paid {
				return "Paid Telegram invites are not supported in this client", nil
			}

			r.canJoin = true
		case *tg.ChatInvitePeek:
			r.title, r.canJoin = inviteChat(invite.Chat).title, true
		}
	} else {
		info, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: r.target.username})
		if err != nil {
			return "", err
		}

		key := keyFor(info.Peer)

		r.chat = peerChats(info.Users, info.Chats)[key]
		if r.chat.input != nil && key.kind == 'c' {
			for _, item := range info.Chats {
				if c, ok := item.(*tg.Channel); ok && c.ID == key.id {
					r.canJoin, r.approval = c.Left, c.JoinRequest
					break
				}
			}
		}
	}

	if r.chat.input == nil && !r.canJoin {
		return "Telegram did not return an accessible chat", nil
	}

	if r.title == "" {
		r.title = r.chat.title
	}

	if r.title == "" && r.target.username != "" {
		r.title = "@" + r.target.username
	}

	return "", nil
}

func inviteChat(value tg.ChatClass) chat {
	var key peerKey

	switch c := value.(type) {
	case *tg.Chat:
		key = peerKey{'g', c.ID}
	case *tg.Channel:
		key = peerKey{'c', c.ID}
	default:
		return chat{}
	}

	return peerChats(nil, []tg.ChatClass{value})[key]
}

func joinChatLink(ctx context.Context, api chatLinkAPI, public chat, r *chatLinkResult) (string, error) {
	var response tg.MessagesChatInviteJoinResultClass

	var err error

	if r.target.invite != "" {
		response, err = api.MessagesImportChatInvite(ctx, r.target.invite)
	} else {
		peer, ok := public.input.(*tg.InputPeerChannel)
		if !ok || peer.ChannelID != public.key.id || public.key.kind != 'c' || peer.AccessHash == 0 {
			return "This chat cannot be joined", nil
		}

		response, err = api.ChannelsJoinChannel(ctx, &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
	}

	if err != nil && !tgerr.Is(err, "USER_ALREADY_PARTICIPANT") {
		return "", err
	}

	if _, ok := response.(*tg.MessagesChatInviteJoinResultWebView); ok {
		return "This join requires a Telegram web view and was not completed here", nil
	}

	if r.target.invite == "" {
		r.chat = public
	} else {
		if result, ok := response.(*tg.MessagesChatInviteJoinResultOk); ok {
			r.chat = joinedInviteChat(result.Updates)
		}

		if r.chat.input == nil {
			// Updates sometimes omit the target chat; a read-only check confirms the exact invite.
			if info, _ := api.MessagesCheckChatInvite(ctx, r.target.invite); info != nil {
				if already, ok := info.(*tg.ChatInviteAlready); ok {
					r.chat = inviteChat(already.Chat)
				}
			}
		}
	}

	if r.chat.input == nil {
		return "Join accepted; refresh the chat list to find it", nil
	}

	return "", nil
}

func joinedInviteChat(updates tg.UpdatesClass) chat {
	var items []tg.ChatClass

	switch u := updates.(type) {
	case *tg.Updates:
		items = u.Chats
	case *tg.UpdatesCombined:
		items = u.Chats
	}

	if len(items) == 1 {
		return inviteChat(items[0])
	}

	return chat{}
}

type chatLinkPhase int

const (
	chatLinkEnter chatLinkPhase = iota
	chatLinkResolving
	chatLinkChoose
	chatLinkConfirm
	chatLinkJoining
)

type chatLinkDialog struct {
	phase       chatLinkPhase
	input, hint string
	target      *chatLinkTarget
	result      *chatLinkResult
}

func (a *app) canOpenChatLink() bool {
	return a.ready && !a.demo && !a.disconnected && a.pending == "" && a.chatLink == nil
}

func (a *app) openChatLinkPrompt() {
	if a.demo {
		a.status = "Telegram chat links are unavailable in demo mode"
	} else if a.canOpenChatLink() {
		a.chatLink = &chatLinkDialog{}
	}
}

func (a *app) tryChatLink(out chan<- request, raw string) bool {
	target, ok := parseChatLink(raw)
	if !ok || !a.canOpenChatLink() {
		return false
	}

	a.chatLink = &chatLinkDialog{}
	a.resolveChatLink(out, target)

	return true
}

func (a *app) resolveChatLink(out chan<- request, target chatLinkTarget) {
	d := a.chatLink
	d.phase, d.target, d.input, d.hint = chatLinkResolving, &target, "", ""
	a.ask(out, request{kind: "chatLinkResolve", chatLink: d.target})
}

func (a *app) chatLinkInput(out chan<- request, k key) {
	d := a.chatLink
	if d == nil || d.phase == chatLinkResolving || d.phase == chatLinkJoining {
		return
	}

	if k.name == "escape" {
		a.chatLink = nil
		return
	}

	switch d.phase {
	case chatLinkEnter:
		switch k.name {
		case "enter":
			if target, ok := parseChatLink(d.input); ok {
				a.resolveChatLink(out, target)
			} else {
				d.hint = "Enter a username or a t.me chat or invite link"
			}
		case "erase":
			_, size := utf8.DecodeLastRuneInString(d.input)
			d.input, d.hint = d.input[:len(d.input)-size], ""
		case "", "paste":
			if len(d.input)+len(k.text) <= chatLinkInputLimit {
				d.input, d.hint = d.input+k.text, ""
			} else {
				d.input, d.hint = "", "Chat link is too long"
			}
		}
	case chatLinkChoose:
		k = commandKey(k)
		switch {
		case k.name == "enter" && d.result.chat.input != nil:
			a.openResolvedChat(out, d.result.chat)
		case k.name == "" && k.text == "j" && d.result.canJoin:
			d.phase = chatLinkConfirm
		}
	case chatLinkConfirm:
		k = commandKey(k)
		if k.name != "" {
			return
		}

		if k.text == "n" {
			a.chatLink = nil
		} else if k.text == "y" && d.result.canJoin && a.pending == "" && a.ready && !a.demo {
			d.phase = chatLinkJoining
			a.ask(out, request{kind: "chatLinkJoin", chat: d.result.chat, chatLink: d.target})
		}
	}
}

func (a *app) receiveChatLink(out chan<- request, e serverEvent) {
	d := a.chatLink
	if a.pending != e.kind || d == nil || e.chatLink == nil || d.target != e.chatLink.target {
		return
	}

	a.pending = ""
	if e.hint != "" {
		a.chatLink, a.status = nil, e.hint
		return
	}

	if e.kind == "chatLinkJoin" {
		a.openResolvedChat(out, e.chatLink.chat)
		return
	}

	d.result, d.phase = e.chatLink, chatLinkChoose
	if d.result.chat.input == nil && d.result.canJoin {
		d.phase = chatLinkConfirm
	}
}

func (a *app) openResolvedChat(out chan<- request, c chat) {
	a.chatLink = nil

	if c.input == nil {
		return
	}

	index := slices.IndexFunc(a.chats, func(item chat) bool { return item.key == c.key })
	if a.open && index == a.selected && a.thread == nil {
		return
	}

	if a.open {
		a.parkDraft()
		a.resetChat()
	}

	if index < 0 {
		a.chats = append([]chat{c}, a.chats...)
		index = 0
	}

	a.openChat(out, index)
}

func (a *app) chatLinkOverlay() (title string, lines []string, footer string) {
	d := a.chatLink
	if d == nil {
		return "", nil, ""
	}

	switch d.phase {
	case chatLinkEnter:
		lines = []string{"> " + chatLinkDisplay(d.input)}
		if d.hint != "" {
			lines = append(lines, d.hint)
		}

		return "Open Telegram chat", lines, "Enter lookup · Esc cancel"
	case chatLinkResolving:
		return "Open Telegram chat", []string{"Looking up chat…"}, ""
	case chatLinkJoining:
		return "Join Telegram chat", []string{"Waiting for Telegram…"}, ""
	}

	name := chatLinkDisplay(d.result.title)
	if name == "" {
		name = "Private chat"
	}

	if d.phase == chatLinkConfirm {
		if d.result.approval {
			return "Request to join Telegram chat?", []string{name}, "y send request · n/Esc cancel"
		}

		return "Join Telegram chat?", []string{name}, "y join · n/Esc cancel"
	}

	if d.result.canJoin {
		return "Public Telegram chat", []string{name}, "Enter open without joining · j join… · Esc cancel"
	}

	return "Open Telegram chat", []string{name}, "Enter open · Esc cancel"
}

func chatLinkDisplay(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}

		return r
	}, limitText(text, chatLinkInputLimit))
}
