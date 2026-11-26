package tui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewCanRefreshGraphicsAfterTmuxAttach(t *testing.T) {
	bin := t.TempDir()

	state := filepath.Join(t.TempDir(), "features")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\ncat \"$TUI_TEST_TMUX_FEATURES\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUI_TEST_TMUX_FEATURES", state)
	t.Setenv("TERM", "tmux-256color")
	t.Setenv("TMUX", "test")
	t.Setenv("TMUX_PANE", "%test")
	t.Setenv("TUI_GRAPHICS", "")
	t.Setenv("TUI_KITTY_GRAPHICS", "")
	t.Cleanup(func() { graphicsCache.Lock(); graphicsCache.ready = false; graphicsCache.Unlock() })

	set := func(value string) {
		t.Helper()

		if err := os.WriteFile(state, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set("linux|off|1|bpaste|1")
	RefreshTerminalGraphics()

	if ImageGraphics() {
		t.Fatal("graphics detected before a capable client attached")
	}

	set("foot|on|1|RGB,sixel|1")

	if ImageGraphics() {
		t.Fatal("redraw unexpectedly re-probed tmux")
	}

	RefreshTerminalGraphics()

	if !ImageGraphics() || KittyGraphics() || !tmuxGraphics().sixelPassthrough {
		t.Fatal("preview did not recover Sixel after Foot attached")
	}

	set("foot|off|1|RGB,sixel|1")
	RefreshTerminalGraphics()

	if tmuxGraphics().sixelPassthrough {
		t.Fatal("passthrough selected when tmux disallows it")
	}

	set("foot|on|1|RGB,sixel|0")
	RefreshTerminalGraphics()

	if tmuxGraphics().sixelPassthrough {
		t.Fatal("passthrough needs focus reports to restore switched panes")
	}

	set("xterm-kitty|all|1|RGB|1")
	RefreshTerminalGraphics()

	if !KittyGraphics() {
		t.Fatal("preview did not follow a change to Kitty")
	}

	t.Setenv("TUI_GRAPHICS", "off")
	RefreshTerminalGraphics()

	if ImageGraphics() {
		t.Fatal("refresh ignored an explicit graphics override")
	}
}

func TestImageOutputCanEnableAfterStartup(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "off")

	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	output := NewImageOutput(file)
	if output.Enabled() || output.Start("avatar") {
		t.Fatal("disabled output tried to load an avatar")
	}

	if _, err := output.WriteString("plain text"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TUI_GRAPHICS", "sixel")

	if !output.Refresh() || !output.Enabled() || !output.Start("avatar") {
		t.Fatal("the existing output could not enable Sixel")
	}

	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}

	avatar, err := DecodeInlineAvatar(&data)
	if err != nil {
		t.Fatal(err)
	}

	output.Store("avatar", avatar)

	if _, err := output.WriteString(strings.Join(avatar.Rows, "\n")); err != nil {
		t.Fatal(err)
	}

	wire, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(wire, []byte("\x1bP0;1q")) || bytes.Contains(wire, []byte(inlinePlaceholder)) {
		t.Fatal("enabled avatars were not transformed to terminal images")
	}

	t.Setenv("TUI_GRAPHICS", "off")

	if !output.Refresh() || output.Get("avatar") != nil || output.Start("new-avatar") {
		t.Fatal("disabling graphics left stale image placements")
	}
}

func TestGraphicsRefreshCanRunWhileWriting(t *testing.T) {
	t.Setenv("TUI_GRAPHICS", "off")

	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	output := NewImageOutput(file)
	done := make(chan error, 1)

	go func() {
		for range 2000 {
			if _, err := output.WriteString("plain text"); err != nil {
				done <- err
				return
			}
		}

		done <- nil
	}()

	for range 20 {
		if err := os.Setenv("TUI_GRAPHICS", "sixel"); err != nil {
			t.Fatal(err)
		}

		output.Refresh()

		if err := os.Setenv("TUI_GRAPHICS", "off"); err != nil {
			t.Fatal(err)
		}

		output.Refresh()
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
