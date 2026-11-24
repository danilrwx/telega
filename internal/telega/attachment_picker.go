package telega

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/danilrwx/telega/tui"
)

type attachmentChoice struct {
	label, path string
	messageID   int
	file        *attachment
}

type stagedAttachment struct {
	upload *uploadAttachment
	asFile bool
}

func (f *uploadAttachment) name() string {
	if f.temporary {
		return "Screenshot.png"
	}

	return tui.AttachmentName(filepath.Base(f.path))
}

func (d chatDraft) attachmentLabel() string {
	name := d.upload.name()
	if len(d.moreUploads) > 0 {
		return "Files: " + name + " + " + strconv.Itoa(len(d.moreUploads))
	}

	if d.asFile || d.upload.format == "" || d.upload.format == "gif" {
		return "File: " + name
	}

	return "Photo: " + name
}

func (a *app) beginAttachment(forceFile bool) {
	a.filePicker = tui.NewFilePicker()
	a.pickerFile = forceFile
	a.status = ""
}

func (a *app) filePickerInput(k key) {
	name := k.name
	switch name {
	case "nextChat":
		name = "ctrl+n"
	case "previousChat":
		name = "ctrl+p"
	case "textAttachment":
		name = "ctrl+t"
	}

	switch action, path := a.filePicker.Key(name, k.text); action {
	case "close":
		a.filePicker = nil
	case "text":
		a.textAttachmentPrompt = &tui.TextAttachmentPrompt{}
	case "attach":
		upload, err := localUpload(path, a.pickerFile)
		if err != nil {
			a.filePicker.Status = err.Error()
			return
		}

		if !a.composing {
			a.restoreDraft()
		}

		a.retrySend = nil
		a.addStaged(upload, a.pickerFile)
		a.filePicker = nil

		a.composing = true
		if a.attachmentTitle == tui.DraftAttachmentsTitle {
			a.showDraftAttachments()
		}
	}
}

func (a *app) addStaged(upload *uploadAttachment, asFile bool) {
	if a.staged == nil {
		a.staged, a.asFile = upload, asFile
		return
	}

	if a.staged.path != upload.path && !slices.ContainsFunc(a.moreUploads, func(item stagedAttachment) bool {
		return item.upload.path == upload.path
	}) {
		a.moreUploads = append(a.moreUploads, stagedAttachment{upload, asFile})
	}
}

func (a *app) replaceStaged(index int, next stagedAttachment) *uploadAttachment {
	if index == 0 {
		old := a.staged

		a.staged, a.asFile = next.upload, next.asFile
		if a.staged == nil && len(a.moreUploads) != 0 {
			first := a.moreUploads[0]
			a.staged, a.asFile = first.upload, first.asFile
			a.moreUploads = slices.Delete(a.moreUploads, 0, 1)
		}

		return old
	}

	old := a.moreUploads[index-1].upload
	if next.upload == nil {
		a.moreUploads = slices.Delete(a.moreUploads, index-1, index)
	} else {
		a.moreUploads[index-1] = next
	}

	return old
}

func (a *app) showDraftAttachments() {
	a.attachmentTitle = tui.DraftAttachmentsTitle
	a.attachmentChoices = nil
	a.attachmentIndex = 0
	a.status = ""

	draft := a.composerDraft()
	if !a.composing {
		if parked, ok := a.drafts[a.draftPosition()]; ok {
			draft = parked
		}
	}

	if draft.upload == nil {
		a.status = "No files in this draft"
		return
	}

	add := func(file *uploadAttachment) {
		a.attachmentChoices = append(a.attachmentChoices, attachmentChoice{label: "File  " + file.name(), path: file.path})
	}
	add(draft.upload)

	for _, file := range draft.moreUploads {
		add(file.upload)
	}
}

func (a *app) showChatAttachments() {
	if a.pending != "" {
		return
	}

	a.attachmentChoices = nil
	a.attachmentIndex = 0
	a.attachmentTitle = ""

	a.status = ""
	for _, msg := range a.log {
		file := msg.attachment
		if file == nil || file.location == nil {
			continue
		}

		a.attachmentChoices = append(a.attachmentChoices, attachmentChoice{
			label: "File  " + tui.AttachmentName(file.name), messageID: msg.id, file: file,
		})
	}

	if len(a.attachmentChoices) == 0 {
		a.status = "No attachments in loaded messages"
		return
	}

	a.attachmentTitle = tui.HistoryAttachmentsTitle
}

func (a *app) attachmentChoiceInput(k key, out chan<- request) {
	name := k.name
	if name == "" {
		name = k.text
	}

	count := len(a.attachmentChoices)
	action, index := tui.AttachmentListAction(name, a.attachmentIndex, count)
	a.attachmentIndex = index
	draft := a.attachmentTitle == tui.DraftAttachmentsTitle

	switch action {
	case "close":
		a.attachmentChoices, a.attachmentTitle, a.status = nil, "", ""
	case "open":
		item := a.attachmentChoices[index]
		if draft {
			a.openTarget = item.path
		} else if a.pending == "" {
			a.ask(out, request{kind: "download", chat: a.currentChat(), anchor: item.messageID, attachment: item.file})
		}
	case "attach":
		if draft {
			a.beginAttachment(false)
		}
	case "edit":
		if draft {
			a.requestAttachmentEdit(index, "")
		}
	case "remove":
		if draft {
			if !a.composing {
				a.restoreDraft()
			}

			removed := a.replaceStaged(index, stagedAttachment{})
			a.retrySend = nil
			a.showDraftAttachments()

			a.attachmentIndex = min(index, max(0, len(a.attachmentChoices)-1))
			if a.persistDraft() {
				removed.discard()
			}
		}
	}
}

func (a *app) textAttachmentNameInput(k key) {
	switch action, name := a.textAttachmentPrompt.Key(k.name, k.text); action {
	case "close":
		a.textAttachmentPrompt = nil
	case "edit":
		a.textAttachmentPrompt = nil
		a.requestAttachmentEdit(-1, name)
	}
}

func (a *app) requestAttachmentEdit(index int, name string) {
	if !a.composing {
		a.restoreDraft()
	}

	a.editAttachmentIndex, a.editAttachmentName = index, name
	a.editAttachmentRequested = true
}

func (a *app) editDraftAttachment(ctx context.Context, run func(*exec.Cmd) (error, error)) error {
	index := a.editAttachmentIndex
	name, text := a.editAttachmentName, string(a.draft)

	var source io.Reader = strings.NewReader(text)

	if index >= 0 {
		choice := a.attachmentChoices[index]

		file, err := openRegularFile(choice.path)
		if err != nil {
			a.status = "Edit attachment: " + err.Error()
			return nil
		}

		defer func() { _ = file.Close() }()

		name = strings.TrimPrefix(choice.label, "File  ")
		source = file
	}

	path, err := tui.CopyDraftAttachment("telega", name, source)
	if err != nil {
		a.setAttachmentError(err)
		return nil
	}

	keep := false
	defer func() {
		if !keep {
			tui.RemoveDraftAttachment("telega", path)
		}
	}()

	cmd, err := tui.VimFileCommand(ctx, path)
	if err == nil {
		var terminalErr error

		err, terminalErr = run(cmd)
		if terminalErr != nil {
			return terminalErr
		}
	}

	if err != nil {
		a.setAttachmentError(err)
		return nil
	}

	upload, err := localUpload(path, index < 0)
	if err != nil {
		a.setAttachmentError(err)
		return nil
	}

	upload.managed = true

	var old *uploadAttachment

	if index >= 0 {
		asFile := a.asFile
		if index > 0 {
			asFile = a.moreUploads[index-1].asFile
		}

		old = a.replaceStaged(index, stagedAttachment{upload, asFile})
	} else {
		a.addStaged(upload, true)
		a.clearDraft()
		a.filePicker = nil
	}

	a.retrySend = nil
	a.composing, keep = true, true

	a.status = ""
	if a.attachmentTitle == tui.DraftAttachmentsTitle {
		a.showDraftAttachments()
		a.attachmentIndex = max(0, index)
	}

	if a.persistDraft() && old != nil {
		old.discard()
	}

	return nil
}

func (a *app) setAttachmentError(err error) {
	a.status = "Edit attachment: " + err.Error()
	if a.filePicker != nil {
		a.filePicker.Status = a.status
	}
}
