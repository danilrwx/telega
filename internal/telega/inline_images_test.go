package telega

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
)

func TestPhotoAndCustomReactionInConversation(t *testing.T) {
	t.Setenv("TUI_KITTY_GRAPHICS", "1")
	t.Setenv("TMUX", "")

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
	c := chat{key: peerKey{'u', 42}, title: "Test chat"}
	photo := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoCachedSize{Type: "s", W: 128, H: 128, Bytes: raster.Bytes()},
		&tg.PhotoSize{Type: "x", W: 1024, H: 768, Size: 8192},
	}}}
	file := fileFromMedia(photo)

	msg := message{
		id: 7, text: "Photo caption", attachment: file, reactions: "👍 1  custom 2", hasReactions: true,
		messageReactions: []reactionChoice{{emoji: "👍", count: 1}, {custom: 5, count: 2}},
	}
	a := app{inlineImages: graphics, ready: true, open: true, visibleMessages: 1, chats: []chat{c}, log: []message{msg}}
	requests := make(chan request, 1)
	a.loadVisibleImages(requests)

	req := <-requests
	if req.kind != "inlineImage" || req.anchor != 7 || req.attachment == nil || len(req.attachment.inlineData) == 0 {
		t.Fatalf("photo did not select a cached thumbnail: %+v", req)
	}

	image := fetchInlineImage(t.Context(), nil, req)
	if image == nil {
		t.Fatal("cached thumbnail should decode without a server request")
	}

	a.receive(requests, serverEvent{kind: "inlineImage", text: req.text, inlineImage: image})
	a.loadVisibleImages(requests)

	req = <-requests
	if req.reactionDocument != 5 {
		t.Fatalf("missing custom reaction load: %+v", req)
	}

	icon, err := tui.DecodeInlineImage(bytes.NewReader(raster.Bytes()), 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(requests, serverEvent{kind: "inlineImage", text: req.text, inlineImage: icon})

	if err := paint(graphics, &a, 100, 25); err != nil {
		t.Fatal(err)
	}

	wire, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}

	text := string(wire)
	if !strings.Contains(text, "Photo caption") || !strings.Contains(text, "\x1b[39m 2") ||
		strings.Contains(text, "custom 2") || strings.Count(text, ",U=1") != 2 {
		t.Fatalf("missing inline images inside Telega conversation: %q", text)
	}
}
