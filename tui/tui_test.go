package tui

import (
	"slices"
	"testing"
)

func TestTerminalText(t *testing.T) {
	got := Wrap("界я\n\x1babc", 3, 3)
	if !slices.Equal(got, []string{"界я", " ab", "c"}) {
		t.Fatalf("unexpected rows: %q", got)
	}

	got = Wrap("e\u0301界\r\n🙂", 2, 0)
	if !slices.Equal(got, []string{"e\u0301", "界", "🙂"}) {
		t.Fatalf("unexpected unlimited wrap: %q", got)
	}

	if got := Clip("a界\x1b!\nnext", 3); got != "a…" {
		t.Fatalf("unexpected clip: %q", got)
	}

	if got := Clip("界x", 1); got != "…" {
		t.Fatalf("unexpected one-column clip: %q", got)
	}
}

func TestNickColorAssignments(t *testing.T) {
	for id, want := range map[string]NickColor{
		"u:17":              {0x8D3F5E, 0xEEABC5, 35},
		"u:18":              {0x255E84, 0x98CCEB, 36},
		"loop-id-123456789": {0x38579B, 0xA4B8F5, 34},
	} {
		if got := ColorForNick(id); got != want {
			t.Errorf("color for %q changed from %+v to %+v", id, want, got)
		}
	}
}

func TestThemeOverride(t *testing.T) {
	t.Setenv("TELEGA_THEME", "dark")
	t.Setenv("TUI_THEME", "light")

	if DarkTheme() {
		t.Fatal("shared setting should select the light palette")
	}

	t.Setenv("TUI_THEME", "")

	if !DarkTheme() {
		t.Fatal("legacy setting should select the dark palette")
	}
}

func TestDeleteConfirmationFooter(t *testing.T) {
	status, keys := DeleteConfirmation(1, true).Footer(80)
	if status != "Delete message for everyone?" || keys != "y confirm deletion  Esc cancel" {
		t.Fatalf("unexpected confirmation: %q / %q", status, keys)
	}

	status, keys = DeleteConfirmation(2, false).Footer(80)
	if status != "Delete 2 messages for you?" || keys != "y confirm deletion  Esc cancel" {
		t.Fatalf("unexpected private confirmation: %q / %q", status, keys)
	}

	status, keys = DeleteConfirmation(1, true).Footer(22)
	if status != "Delete for everyone?" || keys != "y confirm  Esc cancel" {
		t.Fatalf("unexpected narrow confirmation: %q / %q", status, keys)
	}
}

func TestListDialogKeepsSelectionAndActionsTogether(t *testing.T) {
	var rows []string

	ListDialog(DraftAttachmentsTitle, []string{"File  a", "File  b", "File  c"}, 2, 40, 6, "", OpenAttachmentHint,
		func(text string, _, _, _ bool) { rows = append(rows, text) })

	want := []string{"Draft attachments", "", "  File  b", "› File  c", "", "jk choose  Enter open  Esc back"}
	if !slices.Equal(rows, want) {
		t.Fatalf("unexpected dialog rows: %q", rows)
	}
}
