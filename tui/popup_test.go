package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFloatingDialogKeepsChatAroundIt(t *testing.T) {
	const cols, rows = 100, 24

	style := func(s string, _ PopupRole) string { return s }
	content := func(width, height int, add func(string, bool, bool, bool)) {
		ListDialog("Draft attachments", []string{"notes.md"}, 0, width, height, "", OpenAttachmentHint, add)
	}
	popup := FloatingDialog(cols, rows, 74, content, style)
	// The popup edges cut through wide glyphs on both sides.
	row := "LEFT" + strings.Repeat("界", 44) + "   RIGHT"
	background := strings.Repeat(row+"\n", rows-1) + row

	result := strings.Split(popup.Overlay(background, cols, rows), "\n")
	if popup.Y <= 0 || popup.X <= 0 || result[0] != row || result[rows-1] != row {
		t.Fatalf("popup should leave the surrounding rows visible: %+v", popup)
	}

	for _, line := range result[popup.Y : popup.Y+len(popup.Lines)] {
		if ansi.StringWidth(line) != cols || !strings.HasPrefix(line, "LEFT") || !strings.HasSuffix(line, "RIGHT") {
			t.Fatalf("overlay moved content outside the dialog: %q", line)
		}
	}
}

func TestFloatingDialogKeepsControlsOnShortScreens(t *testing.T) {
	popup := FloatingDialog(32, 7, ListPopupWidth, func(width, height int, add func(string, bool, bool, bool)) {
		ListDialog("Reactions", []string{"Mira"}, 0, width, height, "Loading…", "Esc back", add)
	}, func(s string, _ PopupRole) string { return s })
	if popup.X <= 0 || popup.Y <= 0 || len(popup.Lines) > 7 ||
		!strings.HasPrefix(popup.Lines[0], "╭") || !strings.Contains(strings.Join(popup.Lines, "\n"), "Esc back") {
		t.Fatalf("the centered frame and controls must fit on a short display: %+v", popup)
	}
}

func TestWithoutInlineImagesKeepsPlacementWidth(t *testing.T) {
	placement := (&TerminalImage{}).virtualPlacement(1, 3, 2)
	frame := "Before " + placement.Rows[0] + " after\n" + placement.Rows[1]

	got := WithoutInlineImages(frame)
	if plain := ansi.Strip(got); plain != "Before     after\n   " {
		t.Fatalf("replacing graphics changed the surrounding text: %q", plain)
	}
}

func TestFloatingDialogShowsMoreOfLongLists(t *testing.T) {
	names := make([]string, 60)
	for i := range names {
		names[i] = "report.md"
	}

	popup := FloatingDialog(150, 45, ListPopupWidth, func(width, height int, add func(string, bool, bool, bool)) {
		ListDialog("Attachments", names, 32, width, height, "", OpenAttachmentHint, add)
	}, func(s string, _ PopupRole) string { return s })
	if got := ansi.StringWidth(popup.Lines[0]); got != ListPopupWidth || len(popup.Lines) != 28 {
		t.Fatalf("expected the full list dimensions, got %d by %d", got, len(popup.Lines))
	}
}
