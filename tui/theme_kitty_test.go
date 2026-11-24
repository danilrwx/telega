package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopThemeInTmuxFollowsThemeFile(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TERM", "tmux-256color")
	t.Setenv("TMUX", "")
	t.Setenv("COLORFGBG", "")
	t.Setenv("TUI_THEME", "")
	t.Setenv("TELEGA_THEME", "")

	dir := filepath.Join(config, "dotfiles", "theme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "name")
	if err := os.WriteFile(path, []byte("light\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if DarkTheme() {
		t.Fatal("expected a light theme inside tmux")
	}

	changes := WatchDesktopTheme(t.Context(), false)
	if err := os.WriteFile(path, []byte("dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case dark, ok := <-changes:
		if !ok || !dark {
			t.Fatal("watcher did not pick up the dark theme")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not report the theme change")
	}

	t.Setenv("TUI_THEME", "light")

	if DarkTheme() || WatchDesktopTheme(t.Context(), false) != nil {
		t.Fatal("manual theme should override the desktop theme")
	}
}

func TestKittyImagePreviewUsesScopedGraphicsCommands(t *testing.T) {
	t.Setenv("TMUX", "")

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}

	picture, err := DecodeKittyImage(&data)
	if err != nil {
		t.Fatal(err)
	}

	preview := picture.Preview(nil, "photo.png", 80, 24, true)
	if !strings.Contains(preview, "\x1b_Ga=T,f=100,t=d,C=1,q=2,") ||
		!strings.Contains(preview, "c=40,r=20,m=0;iVBORw0KGgo") || !strings.Contains(preview, "photo.png") || !strings.Contains(preview, "o open externally  Esc back") {
		t.Fatalf("missing PNG placement: %q", preview)
	}

	if deletion := picture.Delete(); !strings.HasPrefix(deletion, "\x1b_Ga=d,d=I,i=") {
		t.Fatalf("expected scoped image deletion: %q", deletion)
	}

	t.Setenv("TMUX", "fixture")
	t.Setenv("TERM", "tmux-256color")

	if preview := picture.Preview(nil, "photo.png", 80, 24, true); !strings.Contains(preview, "\x1bPtmux;\x1b\x1b_Ga=T") || !strings.Contains(preview, "U=1") ||
		!strings.Contains(preview, "\U0010EEEE") {
		t.Fatalf("expected tmux passthrough: %q", preview)
	}
}

func TestKittyPreviewKeepsOriginalPixelsWhenTheyFit(t *testing.T) {
	t.Setenv("TMUX", "")

	const (
		width, height = 1573, 926
		x, y          = 1249, 791
	)

	detail := color.RGBA{R: 246, G: 35, B: 79, A: 255}
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	source.SetRGBA(x, y, detail)

	var data bytes.Buffer
	if err := png.Encode(&data, source); err != nil {
		t.Fatal(err)
	}

	picture, err := DecodeKittyImage(&data)
	if err != nil {
		t.Fatal(err)
	}

	preview := picture.Preview(nil, "screenshot.png", 205, 62, false)

	raster, err := png.Decode(bytes.NewReader(kittyPNGBytes(t, preview, false)))
	if err != nil {
		t.Fatal(err)
	}

	if raster.Bounds().Dx() != width || raster.Bounds().Dy() != height || color.RGBAModel.Convert(raster.At(x, y)) != detail {
		t.Fatalf("preview lost source detail: size %v, pixel %v", raster.Bounds(), raster.At(x, y))
	}

	preview = picture.Preview(nil, "screenshot.png", 100, 34, true)

	resized, err := png.Decode(bytes.NewReader(kittyPNGBytes(t, preview, false)))
	if err != nil {
		t.Fatal(err)
	}

	if resized.Bounds().Dx() > (100-2)*8 || resized.Bounds().Dy() > (34-4)*16 ||
		picture.width != width || picture.height != height {
		t.Fatalf("preview raster does not fit or overwrote the source size: %v", resized.Bounds())
	}
}
