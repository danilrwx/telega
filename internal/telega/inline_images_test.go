package telega

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

func TestPhotoAndCustomReactionInConversation(t *testing.T) {
	t.Setenv("TUI_KITTY_GRAPHICS", "1")
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var raster bytes.Buffer
	if err := png.Encode(&raster, image.NewRGBA(image.Rect(0, 0, 128, 128))); err != nil {
		t.Fatal(err)
	}

	out, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = out.Close() }()

	graphics := tui.NewInlineImages(out)
	c := chat{key: peerKey{'g', 42}, title: "Test chat", group: true}
	photo := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoCachedSize{Type: "s", W: 128, H: 128, Bytes: raster.Bytes()},
		&tg.PhotoSize{Type: "x", W: 1024, H: 768, Size: 8192},
	}}}
	file := fileFromMedia(photo)

	msg := message{
		id: 7, text: "Photo caption", attachment: file, reactions: "👍 1  custom 2", hasReactions: true,
		messageReactions: []reactionChoice{{emoji: "👍", count: 1}, {custom: 5, count: 2}},
	}
	msg.avatarPeer = peerKey{'u', 42}

	msg = resolveAuthor(msg, tg.Entities{Users: map[int64]*tg.User{
		42: {ID: 42, Photo: &tg.UserProfilePhoto{PhotoID: 99, DCID: 2}},
	}})
	if msg.avatar == nil || inlineAvatarKey(msg.avatar) != "avatar:99" {
		t.Fatal("profile photo should come from the existing Telegram user entity")
	}

	msg.avatar.inlineData = raster.Bytes()

	a := app{inlineImages: graphics, ready: true, open: true, visibleMessages: 1, chats: []chat{c}, log: []message{msg}}
	requests := make(chan request, 1)
	a.loadVisibleImages(requests)

	req := <-requests
	if req.kind != "inlineImage" || req.anchor != 7 || req.attachment == nil || len(req.attachment.inlineData) == 0 {
		t.Fatalf("photo did not select a cached thumbnail: %+v", req)
	}

	image := fetchInlineImage(t.Context(), nil, 42, req)
	if image == nil {
		t.Fatal("cached thumbnail should decode without a server request")
	}

	fromDisk := *req.attachment
	fromDisk.inlineData = nil
	req.attachment = &fromDisk

	if cached := fetchInlineImage(t.Context(), nil, 42, req); cached == nil {
		t.Fatal("thumbnail should decode from disk without an available Telegram client")
	}

	a.receive(requests, serverEvent{kind: "inlineImage", text: req.text, inlineImage: image})
	a.loadVisibleImages(requests)

	req = <-requests
	if req.reactionDocument != 5 {
		t.Fatalf("missing custom reaction load: %+v", req)
	}

	if _, err := cacheAttachment("reaction-42-5", "reaction.webp", 0, func(out io.Writer) error {
		_, err := out.Write(raster.Bytes())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	icon := fetchInlineImage(t.Context(), nil, 42, req)
	if icon == nil {
		t.Fatal("cached custom reaction should decode without a Telegram client")
	}

	a.receive(requests, serverEvent{kind: "inlineImage", text: req.text, inlineImage: icon})

	a.loadVisibleImages(requests)

	req = <-requests
	if req.text != "avatar:99" {
		t.Fatalf("missing avatar request: %+v", req)
	}

	avatar := fetchInlineImage(t.Context(), nil, 42, req)
	if avatar == nil || avatar.Width != 4 || len(avatar.Rows) != 2 {
		t.Fatal("square avatar should occupy four columns and two rows")
	}

	a.receive(requests, serverEvent{kind: "inlineImage", text: req.text, inlineImage: avatar})

	if err := paint(graphics, &a, 100, 25); err != nil {
		t.Fatal(err)
	}

	wire, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}

	text := string(wire)
	if !strings.Contains(text, avatar.Rows[0]) || !strings.Contains(text, avatar.Rows[1]) {
		t.Fatal("both rows of the avatar must be present")
	}

	if !strings.Contains(text, "Photo caption") || !strings.Contains(text, "\x1b[39m 2") ||
		strings.Contains(text, "custom 2") || strings.Count(text, ",U=1") != 3 {
		t.Fatalf("missing inline images inside Telega conversation: %q", text)
	}

	a.input(key{name: "alt+s"}, nil)
	a.input(key{name: "enter"}, nil)
	a.input(key{name: "escape"}, nil)

	var without bytes.Buffer
	if err := paint(&without, &a, 100, 25); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(without.String(), avatar.Rows[0]) || strings.Contains(without.String(), avatar.Rows[1]) {
		t.Fatal("disabling avatars should redraw the conversation without profile photos")
	}

	var saved tui.AvatarSettings
	if err := tui.ReadSettings("telega", &saved); err != nil || !saved.HideAvatars {
		t.Fatalf("avatar preference should survive a restart: %+v, %v", saved, err)
	}
}

func TestLiveMessagesReuseProfileAvatars(t *testing.T) {
	chatPeer := peerKey{'u', 42}
	self := profileThumbnail(&tg.InputPeerSelf{}, 99, 2)
	other := profileThumbnail(&tg.InputPeerUser{UserID: chatPeer.id}, 100, 2)
	a := app{open: true, ready: true, accountID: 7, chats: []chat{{key: chatPeer}}}
	a.rememberAvatar(peerKey{'u', a.accountID}, self)
	a.rememberAvatar(chatPeer, other)

	a.receive(nil, serverEvent{kind: "send", chat: chatPeer, log: []message{{id: 20, out: true}}})

	if got := a.avatarFile(a.log[0], a.currentChat()); got != self {
		t.Fatal("a short send acknowledgement must reuse the account avatar")
	}

	dispatch := tg.NewUpdateDispatcher()
	receive := func(_ context.Context, _ tg.Entities, raw tg.MessageClass) error {
		peer, msg, _ := unwrapMessage(raw)
		a.receive(nil, serverEvent{kind: "new", chat: peer, log: []message{msg}})

		return nil
	}

	if err := dispatchUpdates(t.Context(), dispatch, receive, &tg.UpdateShortMessage{ID: 21, UserID: chatPeer.id, Out: true}); err != nil {
		t.Fatal(err)
	}

	if got := a.avatarFile(a.log[0], a.currentChat()); got != self {
		t.Fatal("an outgoing direct update must not use the recipient avatar")
	}

	if err := dispatchUpdates(t.Context(), dispatch, receive, &tg.UpdateShortMessage{ID: 22, UserID: chatPeer.id}); err != nil {
		t.Fatal(err)
	}

	if got := a.avatarFile(a.log[0], a.currentChat()); got != other {
		t.Fatal("an incoming update should reuse the known sender avatar")
	}
}

func TestDirectAvatarDisplaySettings(t *testing.T) {
	t.Setenv("TUI_KITTY_GRAPHICS", "1")
	t.Setenv("TUI_GRAPHICS", "kitty")
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	now := time.Now()
	peer := peerKey{'u', 42}
	profile := profileThumbnail(&tg.InputPeerUser{UserID: peer.id}, 55, 2)
	current := chat{key: peer, title: "Ada", avatar: profile, presence: &tg.UserStatusOnline{Expires: int(now.Add(time.Minute).Unix())}}

	out, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = out.Close() }()

	var raster bytes.Buffer
	if err := png.Encode(&raster, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}

	graphics := tui.NewInlineImages(out)
	reqs := make(chan request, 1)

	a := app{inlineImages: graphics, ready: true, open: true, chats: []chat{current}}
	if !a.showMessageAvatars(current) || a.directAvatarFile(current) != profile {
		t.Fatal("private chats should show avatars in the header and beside messages by default")
	}

	a.loadVisibleImages(reqs)

	if len(reqs) != 1 || (<-reqs).attachment != profile {
		t.Fatal("an empty private chat should still request the header avatar")
	}

	avatar, err := tui.DecodeInlineAvatar(bytes.NewReader(raster.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	a.receive(nil, serverEvent{kind: "inlineImage", text: inlineAvatarKey(profile), inlineImage: avatar})
	a.log = []message{{id: 2, text: "Outgoing", out: true}, {id: 1, text: "Incoming", avatar: profile}}
	a.display.DMAvatars = dmAvatarHeader

	var frame bytes.Buffer
	if err := paint(&frame, &a, 100, 25); err != nil {
		t.Fatal(err)
	}

	if strings.Count(frame.String(), avatar.Rows[0]) != 1 || !strings.Contains(frame.String(), "online") {
		t.Fatal("the profile photo and online status should appear once in the chat header")
	}

	a.receive(nil, serverEvent{kind: "presence", chat: peer, presence: &tg.UserStatusRecently{}})
	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || !strings.Contains(frame.String(), "last seen recently") {
		t.Fatalf("presence should reflect Telegram's latest status: %v", err)
	}

	a.settingsCursor = 3
	a.toggleDisplaySetting()
	frame.Reset()

	outgoing, _ := a.renderAvatar(nil, peerKey{}, "You")

	if err := paint(&frame, &a, 100, 25); err != nil || strings.Count(frame.String(), avatar.Rows[0]) != 2 ||
		!strings.Contains(frame.String(), outgoing[0]) {
		t.Fatalf("message mode should preserve the header photo and add avatars beside messages: %v", err)
	}

	a.toggleDisplaySetting()
	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || strings.Count(frame.String(), avatar.Rows[0]) != 1 {
		t.Fatalf("turning off message avatars should keep the header photo: %v", err)
	}

	a.toggleDisplaySetting()
	a.input(key{name: "alt+s"}, nil)
	a.input(key{text: "j"}, nil)
	a.input(key{name: "enter"}, nil)
	a.input(key{name: "escape"}, nil)
	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || a.directAvatarFile(current) != nil ||
		strings.Count(frame.String(), avatar.Rows[0]) != 1 || !strings.Contains(frame.String(), "last seen recently") {
		t.Fatalf("turning off the header avatar should keep message avatars and presence: %v", err)
	}

	var saved displaySettings
	if err := tui.ReadSettings("telega", &saved); err != nil || !saved.HideDMHeaderAvatar || !saved.showDMMessageAvatars() {
		t.Fatalf("expected independent avatar preferences to persist: %+v (%v)", saved, err)
	}

	a.display.HideAvatars = true

	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || strings.Contains(frame.String(), avatar.Rows[0]) {
		t.Fatalf("the global setting should hide all avatars: %v", err)
	}
}

func TestMissingAvatarsRenderInitialsWithoutGraphics(t *testing.T) {
	sender := peerKey{'u', 42}
	name := "Иван Петров"
	a := app{
		ready: true, open: true, colors: &darkPalette,
		chats: []chat{{key: peerKey{'c', 7}, title: "Group", group: true}},
		log:   []message{{id: 1, text: "Hello", author: sender, authorName: name}},
	}

	var frame bytes.Buffer

	if err := paint(&frame, &a, 100, 25); err != nil {
		t.Fatal(err)
	}

	style := a.avatarStyle(sender, name)
	if style != a.avatarStyle(sender, "Different name") || !strings.Contains(frame.String(), style+" ИП ") {
		t.Fatal("a sender without a profile photo should have colored initials keyed by peer ID")
	}

	a.chats[0] = chat{key: sender, title: name}
	a.log = nil

	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || !strings.Contains(frame.String(), style+" ИП ") {
		t.Fatalf("a private chat without a profile photo should show the same initials in its header: %v", err)
	}

	a.display.HideDMHeaderAvatar = true

	frame.Reset()

	if err := paint(&frame, &a, 100, 25); err != nil || strings.Contains(frame.String(), style+" ИП ") {
		t.Fatalf("the header avatar setting should also hide the initials: %v", err)
	}
}
