package telega

import (
	"io"
	"strings"
	"testing"
)

func paint(w io.Writer, a *app, cols, rows int) error {
	return (&screenPainter{}).paint(w, a, cols, rows)
}

func TestScreenPainterUpdatesDraftWithoutClearing(t *testing.T) {
	a := app{
		ready: true, open: true, composing: true, demo: true,
		chats: []chat{{key: peerKey{'u', 2}, title: "Ada"}},
		log:   []message{{id: 1, text: "Earlier message"}},
	}

	var p screenPainter

	draw := func(cols int, full bool) string {
		t.Helper()

		var out strings.Builder
		if err := p.paint(&out, &a, cols, 24); err != nil {
			t.Fatal(err)
		}

		frame := out.String()
		if clear := strings.Contains(frame, "\x1b[2J"); clear != full {
			t.Fatalf("clear = %t, want %t: %q", clear, full, frame)
		}

		return frame
	}

	draw(96, true)
	a.edit(key{text: "тест"})

	if frame := draw(96, false); !strings.Contains(frame, "тест") ||
		strings.Contains(frame, "Earlier message") || !strings.Contains(frame, "\x1b[?25h") {
		t.Fatalf("expected only a draft update and the cursor: %q", frame)
	}

	a.edit(key{name: "erase"})

	if frame := draw(96, false); !strings.Contains(frame, "тес ") {
		t.Fatalf("shortened text should be padded to cover the old character: %q", frame)
	}

	a.edit(key{text: "\nNext line"})
	draw(96, false)

	a.log[0].text = "Updated message"

	if frame := draw(96, false); !strings.Contains(frame, "Updated message") {
		t.Fatalf("history change not drawn: %q", frame)
	}

	a.settingsOpen = true

	draw(96, false)

	a.settingsOpen = false

	draw(96, false)
	draw(97, true)
	draw(97, false)
}
