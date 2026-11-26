package tui

import (
	"os"
	"path/filepath"
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
	set("linux|off|1|bpaste")
	RefreshTerminalGraphics()

	if ImageGraphics() {
		t.Fatal("graphics detected before a capable client attached")
	}

	set("foot|on|1|RGB,sixel")

	if ImageGraphics() {
		t.Fatal("redraw unexpectedly re-probed tmux")
	}

	RefreshTerminalGraphics()

	if !ImageGraphics() || KittyGraphics() {
		t.Fatal("preview did not recover Sixel after Foot attached")
	}

	set("xterm-kitty|all|1|RGB")
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
