package tui

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

type graphicsModes struct{ kitty, sixel, sixelPassthrough bool }

var graphicsCache struct {
	sync.Mutex
	ready bool
	modes graphicsModes
}

func tmuxGraphics() graphicsModes {
	graphicsCache.Lock()
	defer graphicsCache.Unlock()

	if !graphicsCache.ready {
		graphicsCache.modes = queryTmuxGraphics()
		graphicsCache.ready = true
	}

	return graphicsCache.modes
}

// RefreshTerminalGraphics rechecks tmux after an attach or terminal change.
// Call on an explicit preview action, keeping redraws free of subprocesses.
func RefreshTerminalGraphics() {
	if !multiplexedTerminal() || os.Getenv("TMUX") == "" || os.Getenv("TUI_GRAPHICS") != "" {
		return
	}

	graphicsCache.Lock()
	defer graphicsCache.Unlock()

	graphicsCache.modes = queryTmuxGraphics()
	graphicsCache.ready = true
}

func queryTmuxGraphics() graphicsModes {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return graphicsModes{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	output, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane, "#{client_termname}|#{allow-passthrough}|#{sixel_support}|#{client_termfeatures}|#{focus-events}").Output()
	if err != nil {
		return graphicsModes{}
	}

	fields := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(fields) != 5 {
		return graphicsModes{}
	}

	return graphicsModes{
		kitty: fields[0] == "xterm-kitty" && (fields[1] == "on" || fields[1] == "all"),
		sixel: fields[2] == "1" && slices.Contains(strings.Split(fields[3], ","), "sixel"),
		// Focus reports let clients restore graphics after tmux redraws a switched pane.
		sixelPassthrough: fields[4] == "1" && (fields[0] == "foot" || strings.HasPrefix(fields[0], "foot-")) &&
			(fields[1] == "on" || fields[1] == "all"),
	}
}
