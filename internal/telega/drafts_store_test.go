package telega

import (
	"bytes"
	"strings"
	"testing"

	"github.com/danilrwx/telega/tui"
)

func TestDraftReopensInItsChat(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	store, err := tui.OpenDraftStore("telega")
	if err != nil {
		t.Fatal(err)
	}

	newApp := func(account int64, peer int64) app {
		return app{
			ready: true, open: true, draftStore: store, accountID: account,
			chats: []chat{{key: peerKey{'u', peer}, title: "Fixture chat"}},
		}
	}
	a := newApp(10, 20)
	a.restoreDraft()
	a.composing = true
	a.edit(key{text: "Черновик"})
	a.replyID = 123
	a.persistDraft()

	if !a.input(key{name: "escape"}, nil) || a.composing {
		t.Fatal("Esc should leave editing without closing the chat")
	}

	a.persistDraft()

	var screen bytes.Buffer
	if err := paint(&screen, &a, 96, 22); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(screen.String(), "\x1b[19;32H─ Reply ") ||
		!strings.Contains(screen.String(), "Черновик") {
		t.Fatalf("draft was not visible inside the chat pane: %q", screen.String())
	}

	reopened := newApp(10, 20)
	reopened.restoreDraft()

	if !reopened.composing || string(reopened.draft) != "Черновик" || reopened.replyID != 123 {
		t.Fatalf("draft did not survive returning to the chat: %q", string(reopened.draft))
	}

	for _, other := range []app{newApp(10, 21), newApp(11, 20)} {
		other.restoreDraft()

		if len(other.draft) != 0 {
			t.Fatal("draft leaked to another chat or account")
		}
	}

	reopened.clearComposer()
	reopened.persistDraft()

	afterSend := newApp(10, 20)
	afterSend.restoreDraft()

	if len(afterSend.draft) != 0 {
		t.Fatal("completed draft should be removed")
	}
}

func TestCancelReplyKeepsDraftWithoutRestoringReply(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	store, err := tui.OpenDraftStore("telega")
	if err != nil {
		t.Fatal(err)
	}

	fresh := app{ready: true, open: true, draftStore: store, accountID: 10, chats: []chat{workflowChat(20, "Chat")}}
	a := fresh
	a.restoreDraft()
	a.log = []message{{id: 123, text: "Original"}}
	a.input(key{text: "r"}, nil)
	a.edit(key{text: "Draft"})
	a.edit(key{name: "left"})
	a.staged = &uploadAttachment{path: "fixture.txt"}
	a.replyTopic = 100
	a.retrySend = &request{text: "Draft", upload: a.staged, replyID: 123, randomID: 9}

	if !a.persistDraft() {
		t.Fatal("failed to save reply draft")
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(screen.String(), "Ctrl+G cancel reply") {
		t.Fatal("reply cancellation shortcut is not visible")
	}

	keys := make(chan key, 1)

	done := make(chan bool, 1)
	done <- false

	readKeys(t.Context(), -1, strings.NewReader("\x07"), keys, done)
	a.input(<-keys, nil)

	if !a.composing || a.replyID != 0 || a.replyTopic != 0 || a.replyPreview != "" || a.retrySend != nil ||
		string(a.draft) != "Draft" || a.draftCursor != 4 || a.staged == nil {
		t.Fatal("Ctrl+G should remove only the reply and its retry state")
	}

	a.input(key{name: "escape"}, nil)

	if !a.persistDraft() {
		t.Fatal("failed to save the detached draft")
	}

	fresh.restoreDraft()

	if !fresh.composing || fresh.replyID != 0 || fresh.replyTopic != 0 || fresh.replyPreview != "" || fresh.retrySend != nil ||
		string(fresh.draft) != "Draft" || fresh.draftCursor != 4 || fresh.staged == nil || fresh.staged.path != "fixture.txt" {
		t.Fatal("reopening should restore draft text and attachment without the canceled reply")
	}
}
