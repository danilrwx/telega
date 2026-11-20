package telega

import (
	"slices"
	"testing"
)

func TestWrapTerminalText(t *testing.T) {
	got := wrap("界я\n\x1babc", 3, 3)
	if !slices.Equal(got, []string{"界я", " ab", "c"}) {
		t.Fatalf("unexpected rows: %q", got)
	}
}

func TestHistoryKeepsLiveEdit(t *testing.T) {
	var a app
	a.insert(message{id: 3, text: "live"}, true)
	a.insert(message{id: 3, text: "stale"}, false)
	a.insert(message{id: 2, text: "older"}, false)

	if len(a.log) != 2 || a.log[0].text != "live" || a.log[1].id != 2 {
		t.Fatalf("unexpected history: %+v", a.log)
	}

	a.draft = []rune("draft")
	a.pending = "send"
	a.receive(nil, serverEvent{kind: "error", text: "offline"})

	if !a.composing || a.pending != "" || string(a.draft) != "draft" {
		t.Fatal("send error did not restore the draft")
	}
}
