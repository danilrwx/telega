package telega

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestScreenPainterScrollAndDialog(t *testing.T) {
	a := app{
		ready: true, open: true, demo: true,
		chats: []chat{{title: "Sidebar title"}},
		log: []message{
			{id: 3, text: "Newest message"},
			{id: 2, text: "Older message"},
			{id: 1, text: "Oldest message"},
		},
	}

	var p screenPainter

	draw := func() string {
		t.Helper()

		var out strings.Builder
		if err := p.paint(&out, &a, 96, 24); err != nil {
			t.Fatal(err)
		}

		return out.String()
	}

	first := draw()
	if !strings.HasPrefix(first, beginSynchronizedOutput) || !strings.HasSuffix(first, endSynchronizedOutput) {
		t.Fatal("the entire frame must be synchronized")
	}

	if frame := draw(); frame != "" {
		t.Fatalf("unchanged screen should produce no output: %q", frame)
	}

	a.offset = 1

	if frame := draw(); strings.Contains(frame, "\x1b[2J") || strings.Contains(frame, "Sidebar title") || !strings.Contains(frame, "Older message") {
		t.Fatalf("moving selection should update affected rows only: %q", frame)
	}

	a.settingsOpen = true

	if frame := draw(); strings.Contains(frame, "\x1b[2J") || !strings.Contains(frame, "Settings") {
		t.Fatalf("dialog should update affected rows only: %q", frame)
	}

	a.settingsOpen = false

	if frame := draw(); strings.Contains(frame, "\x1b[2J") || !strings.Contains(frame, "Oldest message") {
		t.Fatalf("closing a dialog should restore the covered rows: %q", frame)
	}
}

func TestScreenPainterErasesShortenedAndRemovedRows(t *testing.T) {
	var (
		p   screenPainter
		out strings.Builder
	)

	if err := p.writeFrame(&out, "\x1b[?25l\x1b[2J\x1b[1;2HHeader\x1b[2;2HLong text\x1b[3;2HRemoved", 30, 4); err != nil {
		t.Fatal(err)
	}

	out.Reset()

	if err := p.writeFrame(&out, "\x1b[?25l\x1b[2J\x1b[1;2HHeader\x1b[2;2HShort", 30, 4); err != nil {
		t.Fatal(err)
	}

	frame := out.String()
	if !strings.Contains(frame, "\x1b[2;1H\x1b[0m\x1b[2K") || !strings.Contains(frame, "\x1b[3;1H\x1b[0m\x1b[2K") ||
		strings.Contains(frame, "Header") || strings.Contains(frame, "\x1b[2J") {
		t.Fatalf("changed rows must erase stale characters without clearing the screen: %q", frame)
	}
}

func TestScreenRowsPreserveStylesAndCursor(t *testing.T) {
	lines, cursor, ok := screenRows("\x1b[?25l\x1b[2J\x1b[31m\x1b[2;3HRed\x1b[1;2H赤👍\x1b[0m\x1b[3;4H\x1b[?25h", 3)
	if !ok || !strings.Contains(lines[0], "\x1b[31m\x1b[1;2H赤👍") ||
		!strings.Contains(lines[1], "\x1b[31m\x1b[2;3HRed") || cursor != "\x1b[3;4H\x1b[?25h" {
		t.Fatalf("styles or cursor lost: %q %q, parsed %t", lines, cursor, ok)
	}

	_, cursor, ok = screenRows("\x1b[1;2H赤👍\x1b[?25h", 1)
	if !ok || cursor != "\x1b[1;6H\x1b[?25h" {
		t.Fatalf("wide characters moved the cursor incorrectly: %q", cursor)
	}

	for _, frame := range []string{"\x1b[2;1HOutside", "\x1b[1;1H\x1b[2CUnsupported"} {
		if _, _, ok := screenRows(frame, 1); ok {
			t.Fatalf("unsupported commands should fall back to a full frame: %q", frame)
		}
	}
}

func TestSynchronizedOutputResetsAfterError(t *testing.T) {
	var out strings.Builder

	failure := errors.New("draw failed")

	err := synchronizedOutput(&out, func() error {
		_, _ = io.WriteString(&out, "partial frame")
		return failure
	})
	if !errors.Is(err, failure) || out.String() != beginSynchronizedOutput+"partial frame"+endSynchronizedOutput {
		t.Fatalf("synchronization must end even on error: %q, %v", out.String(), err)
	}
}
