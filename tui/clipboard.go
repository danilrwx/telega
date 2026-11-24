package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// ClipboardCommands reads a clipboard format from Wayland or X11.
func ClipboardCommands(ctx context.Context, mime string) []*exec.Cmd {
	var commands []*exec.Cmd
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", mime))
	}

	if os.Getenv("DISPLAY") != "" {
		target := mime
		if mime == "text" {
			target = "UTF8_STRING"
		}

		commands = append(commands, exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", target, "-o"))
	}

	return commands
}

// ClipboardPNGPaste recognizes PNG bytes delivered through terminal bracketed paste.
func ClipboardPNGPaste(text string) bool {
	text = strings.TrimPrefix(text, "\x89")

	text = strings.TrimPrefix(text, "\ufffd")
	if strings.HasPrefix(text, "PNG\r\n\x1a\n") || strings.HasPrefix(text, "PNG\n\x1a\n") {
		return true
	}
	// Some terminals strip control bytes before forwarding the paste.
	header := text[:min(len(text), 24)]

	return strings.HasPrefix(header, "PNG\n") && strings.Contains(header, "IHDR")
}
