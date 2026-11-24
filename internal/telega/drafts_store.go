package telega

import (
	"fmt"
	"os"
	"reflect"
	"time"
)

type diskDraft struct {
	Text, ReplyPreview, EditOriginal    string
	ReplyID, ReplyTopic, EditID, Cursor int
	EditHasAttachment                   bool
	RandomID                            int64
	Attachments                         []diskAttachment
}

type diskAttachment struct {
	Path, Format               string
	AsFile, Managed, Temporary bool
	Checked                    bool
	Size                       int64
	Modified                   time.Time
	VoiceSeconds               int `json:",omitempty"`
}

func savedAttachment(upload *uploadAttachment, asFile bool) diskAttachment {
	saved := diskAttachment{Path: upload.path, Format: upload.format, AsFile: asFile, Managed: upload.managed, Temporary: upload.temporary, VoiceSeconds: upload.voiceSeconds}
	if upload.original != nil {
		saved.Checked, saved.Size, saved.Modified = true, upload.original.Size(), upload.original.ModTime().UTC()
	}

	if upload.changed {
		saved.Checked, saved.Size = true, -1
	}

	return saved
}

func (file diskAttachment) upload() *uploadAttachment {
	info, _ := os.Stat(file.Path)
	changed := file.Checked && (info == nil || info.Size() != file.Size || !info.ModTime().Equal(file.Modified))

	return &uploadAttachment{path: file.Path, format: file.Format, managed: file.Managed, temporary: file.Temporary && file.Managed, original: info, changed: changed, voiceSeconds: file.VoiceSeconds}
}

func (a *app) diskDraftKey() string {
	if a.draftStore == nil || a.accountID == 0 || !a.open || len(a.chats) == 0 {
		return ""
	}

	pos := a.draftPosition()

	return fmt.Sprintf("%d:%c:%d:%d", a.accountID, pos.chat.kind, pos.chat.id, pos.id)
}

func (a *app) loadDiskDraft() (chatDraft, bool) {
	key := a.diskDraftKey()
	if key == "" || key == a.loadedDraftKey {
		return chatDraft{}, false
	}

	a.loadedDraftKey = key
	a.savedDraft = diskDraft{}

	ok, err := a.draftStore.Load(key, &a.savedDraft)
	if err != nil {
		a.status = "Load draft: " + err.Error()
		return chatDraft{}, false
	}

	if !ok {
		return chatDraft{}, false
	}

	saved := a.savedDraft

	draft := chatDraft{
		text: saved.Text, replyPreview: saved.ReplyPreview, editOriginal: saved.EditOriginal,
		replyID: saved.ReplyID, replyTopic: saved.ReplyTopic, editID: saved.EditID,
		cursor: saved.Cursor, editHasAttachment: saved.EditHasAttachment,
	}
	for _, item := range saved.Attachments {
		upload := item.upload()
		if draft.upload == nil {
			draft.upload, draft.asFile = upload, item.AsFile
		} else {
			draft.moreUploads = append(draft.moreUploads, stagedAttachment{upload, item.AsFile})
		}
	}

	if saved.RandomID != 0 {
		draft.retrySend = &request{
			kind: "send", chat: a.currentChat(), text: saved.Text, randomID: saved.RandomID,
			replyID: saved.ReplyID, replyTopic: saved.ReplyTopic, upload: draft.upload, asFile: draft.asFile,
		}
	}

	return draft, true
}

func (a *app) persistDraft() bool {
	key := a.diskDraftKey()
	if key == "" {
		return true
	}

	if key != a.loadedDraftKey {
		return false
	}

	saved := a.composerDraft()
	if parked, ok := a.drafts[a.draftPosition()]; ok {
		saved = parked
	}

	next := diskDraft{
		Text: saved.text, Cursor: saved.cursor, ReplyID: saved.replyID,
		ReplyTopic: saved.replyTopic, ReplyPreview: saved.replyPreview,
		EditID: saved.editID, EditOriginal: saved.editOriginal, EditHasAttachment: saved.editHasAttachment,
	}
	add := func(upload *uploadAttachment, asFile bool) {
		if upload != nil && (!upload.temporary || upload.managed) {
			next.Attachments = append(next.Attachments, savedAttachment(upload, asFile))
		}
	}
	add(saved.upload, saved.asFile)

	for _, item := range saved.moreUploads {
		add(item.upload, item.asFile)
	}

	if saved.retrySend != nil && saved.retrySend.upload == saved.upload && saved.retrySend.text == saved.text &&
		(saved.upload == nil || len(next.Attachments) > 0 && next.Attachments[0].Path == saved.upload.path) {
		next.RandomID = saved.retrySend.randomID
	}

	if reflect.DeepEqual(next, a.savedDraft) {
		return true
	}

	var value any
	if next.Text != "" || next.ReplyID != 0 || next.EditID != 0 || len(next.Attachments) > 0 {
		value = next
	}

	if err := a.draftStore.Save(key, value); err != nil {
		a.status = "Save draft: " + err.Error()
		return false
	}

	a.savedDraft = next

	return true
}
