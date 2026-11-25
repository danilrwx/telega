package telega

import (
	"strings"
	"testing"
)

func TestComposerReadlineKeys(t *testing.T) {
	const controls = "\x01\x17\x05\x17\x17"

	keys := make(chan key, len(controls))

	done := make(chan bool, len(controls))
	for range len(controls) {
		done <- false
	}

	readKeys(t.Context(), -1, strings.NewReader(controls), keys, done)

	draft := []rune("первая\nодно слово  \nтретья")
	a := app{ready: true, composing: true, draft: draft, draftCursor: 10}

	for _, step := range []struct {
		text   string
		cursor int
	}{
		{"первая\nодно слово  \nтретья", 7},
		{"первая\nодно слово  \nтретья", 7},
		{"первая\nодно слово  \nтретья", 19},
		{"первая\nодно \nтретья", 12},
		{"первая\n\nтретья", 7},
	} {
		a.input(<-keys, nil)

		if string(a.draft) != step.text || a.draftCursor != step.cursor {
			t.Fatalf("draft = %q at %d, want %q at %d", a.draft, a.draftCursor, step.text, step.cursor)
		}
	}
}
