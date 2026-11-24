package telega

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/danilrwx/telega/tui"
)

func TestImagePreviewExternalShortcut(t *testing.T) {
	t.Setenv("TMUX", "")

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 60, 40))); err != nil {
		t.Fatal(err)
	}

	preview, err := tui.DecodeKittyImage(&data)
	if err != nil {
		t.Fatal(err)
	}

	a := app{ready: true, imagePreview: preview, imagePreviewTitle: "photo.png", imagePreviewPath: "/tmp/photo.png"}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(screen.String(), "o open externally  Esc back") || a.openExternal != "" {
		t.Fatal("preview should offer an explicit external action")
	}

	a.input(key{text: "щ"}, nil)

	if a.openExternal != "/tmp/photo.png" || a.imagePreview != preview || a.openTarget != "" {
		t.Fatal("o must send the original file to the system opener and keep the preview")
	}

	a.openExternal = ""
	a.input(key{text: "й"}, nil)

	if a.imagePreview != nil || a.imagePreviewPath != "" || a.openExternal != "" {
		t.Fatal("q on the Russian layout should close the preview without opening a file")
	}
}
