package telega

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danilrwx/telega/tui"
)

func TestSharedFilePickerDraftWorkflow(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("PATH", state)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	if err := os.WriteFile(filepath.Join(state, "vim"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	first := filepath.Join(state, "first file.txt")

	second := filepath.Join(state, "second.txt")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store, err := tui.OpenDraftStore("telega")
	if err != nil {
		t.Fatal(err)
	}

	newApp := func() app {
		a := app{ready: true, open: true, accountID: 1, draftStore: store, chats: []chat{{key: peerKey{'u', 2}}}}
		a.restoreDraft()

		return a
	}
	a := newApp()
	a.composing = true
	a.edit(key{text: "draft caption"})
	a.input(key{name: "alt+a"}, nil)
	a.filePicker.SetPath(filepath.Join(state, "first"))
	a.input(key{name: "tab"}, nil)

	var screen bytes.Buffer
	if err := paint(&screen, &a, 100, 24); err != nil || !strings.Contains(screen.String(), "Ctrl+T text file") {
		t.Fatalf("shared picker view missing: %v %s", err, screen.String())
	}

	a.input(key{name: "enter"}, nil)
	a.input(key{name: "jumpBack"}, nil)
	a.input(key{text: "ф"}, nil)

	if a.filePicker == nil {
		t.Fatal("a should add another file from the draft attachment dialog")
	}

	a.filePicker.SetPath(second)
	a.input(key{name: "enter"}, nil)

	if len(a.attachmentChoices) != 2 || string(a.draft) != "draft caption" {
		t.Fatalf("picker lost a file or the caption: %+v, %q", a.attachmentChoices, a.draft)
	}

	a.input(key{text: "у"}, nil)
	runEditor := func(text string) func(*exec.Cmd) (error, error) {
		return func(cmd *exec.Cmd) (error, error) {
			path := cmd.Args[len(cmd.Args)-1]
			return os.WriteFile(path, []byte(text), 0o600), nil
		}
	}

	if !a.editAttachmentRequested {
		t.Fatal("e should open the selected attachment in Vim")
	}

	if err := a.editDraftAttachment(t.Context(), runEditor("edited copy")); err != nil {
		t.Fatal(err)
	}

	if original, err := os.ReadFile(first); err != nil || string(original) != "original" || a.staged.path == first {
		t.Fatalf("the edit modified the original file: %q, %v", original, err)
	}

	a.input(key{text: "a"}, nil)
	a.input(key{name: "textAttachment"}, nil)
	a.input(key{name: "escape"}, nil)

	if a.filePicker == nil || a.textAttachmentPrompt != nil {
		t.Fatal("cancelling the name prompt should return to the picker")
	}

	a.input(key{name: "textAttachment"}, nil)
	a.input(key{name: "paste", text: "Отчёт.md"}, nil)
	a.input(key{name: "enter"}, nil)

	large := strings.Repeat("Большой текст\n", 20_000)
	if err := a.editDraftAttachment(t.Context(), runEditor(large)); err != nil {
		t.Fatal(err)
	}

	a = newApp()
	if len(a.moreUploads) != 2 || len(a.draft) != 0 || a.moreUploads[1].upload.name() != "Отчёт.md" {
		t.Fatalf("files or caption restored incorrectly: %+v, %q", a.moreUploads, a.draft)
	}

	if got, err := os.ReadFile(a.moreUploads[1].upload.path); err != nil || string(got) != large {
		t.Fatalf("text file restored incorrectly: %d bytes, %v", len(got), err)
	}

	a.input(key{name: "jumpBack"}, nil)
	a.input(key{text: "d"}, nil)

	if len(a.attachmentChoices) != 2 || a.staged.path != second {
		t.Fatal("d should remove only the selected file")
	}
}

func TestMultipleDraftAttachmentsSendOneAtATime(t *testing.T) {
	c := chat{key: peerKey{'u', 2}}
	a := app{ready: true, open: true, composing: true, chats: []chat{c}}
	a.addStaged(&uploadAttachment{path: "/first"}, true)
	a.addStaged(&uploadAttachment{path: "/second"}, true)
	a.edit(key{text: "caption"})

	out := make(chan request, 2)
	a.submitDraft(out)

	first := <-out
	if first.upload.path != "/first" || first.text != "caption" {
		t.Fatalf("wrong first upload: %+v", first)
	}

	a.receive(out, serverEvent{kind: "send", chat: c.key, log: []message{{id: 1}}})

	second := <-out
	if second.upload.path != "/second" || second.text != "" || second.randomID == first.randomID {
		t.Fatalf("wrong follow-up upload: %+v", second)
	}

	a.receive(out, serverEvent{kind: "error", text: "failed"})
	a.submitDraft(out)

	if retry := <-out; retry.upload != second.upload || retry.randomID != second.randomID {
		t.Fatalf("retry must send only the failed file: %+v", retry)
	}
}
