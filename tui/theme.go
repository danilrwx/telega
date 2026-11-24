package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muesli/termenv"
)

// Theme contains the colors shared by the chat clients.
type Theme struct {
	Heading, Muted, SelectionText, SelectionBackground RGB
	Date, Unread, Accent                               RGB
	DraftBackground                                    RGB
}

// ThemeColors returns the shared colors for a light or dark background.
func ThemeColors(dark bool) Theme {
	if dark {
		return Theme{
			Heading: 0xD4B6E5, Muted: 0x9897AE,
			SelectionText: 0xF2E5FF, SelectionBackground: 0x392E4F,
			Date: 0xA8BAF2, Unread: 0xE8CD9A, Accent: 0xA1D8CF,
			DraftBackground: 0x1C1C1C,
		}
	}

	return Theme{
		Heading: 0x70428D, Muted: 0x626477,
		SelectionText: 0x342645, SelectionBackground: 0xEADDF5,
		Date: 0x415C93, Unread: 0x79550E, Accent: 0x286C66,
		DraftBackground: 0xF5F0F8,
	}
}

// DarkTheme chooses the background before the application starts reading keys.
// TUI_THEME can override detection, and TELEGA_THEME remains a legacy override.
func DarkTheme() bool {
	if dark, ok := manualTheme(); ok {
		return dark
	}

	if dark, ok := desktopTheme(); ok {
		return dark
	}

	return termenv.HasDarkBackground()
}

func manualTheme() (dark, ok bool) {
	theme := os.Getenv("TUI_THEME")
	if theme == "" {
		theme = os.Getenv("TELEGA_THEME")
	}

	return theme == "dark", theme == "light" || theme == "dark"
}

func multiplexedTerminal() bool {
	term := os.Getenv("TERM")
	return strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen")
}

func desktopTheme() (dark, ok bool) {
	if !multiplexedTerminal() {
		return false, false
	}

	if os.Getenv("COLORFGBG") != "" {
		return false, false
	}

	config, err := os.UserConfigDir()
	if err != nil {
		return false, false
	}

	data, err := os.ReadFile(filepath.Join(config, "dotfiles", "theme", "name"))
	if err != nil {
		return false, false
	}

	theme := strings.TrimSpace(string(data))

	return theme == "dark", theme == "light" || theme == "dark"
}

// WatchDesktopTheme reports desktop palette changes when the multiplexer cannot report them.
func WatchDesktopTheme(ctx context.Context, current bool) <-chan bool {
	if _, manual := manualTheme(); manual {
		return nil
	}

	if _, ok := desktopTheme(); !ok {
		return nil
	}

	changes := make(chan bool)
	go func() {
		defer close(changes)

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			if dark, ok := desktopTheme(); ok && dark != current {
				select {
				case changes <- dark:
					current = dark
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return changes
}
