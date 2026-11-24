package telega

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestAttachmentGalleryBrowsesEntireChat(t *testing.T) {
	c := chat{
		key: peerKey{'c', 42}, title: "Project", forum: true,
		input: &tg.InputPeerChannel{ChannelID: 42},
	}
	topic := c
	topic.threadKind, topic.threadID = forumThread, 5
	a := app{ready: true, open: true, chats: []chat{c}, thread: &topic, log: []message{{id: 900, text: "Recent history"}}}
	out := make(chan request, 1)

	a.input(key{text: "V"}, out)
	req := <-out
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesSearchRequest)

		_, photoVideo := query.Filter.(*tg.InputMessagesFilterPhotoVideo)
		if !photoVideo || query.Peer != c.input || query.TopMsgID != 0 || query.Q != "" || query.OffsetID != 0 {
			t.Fatalf("expected an unscoped media search in the current chat: %+v", query)
		}

		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{
			Count: 2, Messages: []tg.MessageClass{&tg.Message{ID: 40, Message: "Older photo"}},
		}

		return nil
	}))

	hits, next, err := searchMessages(t.Context(), api, req)
	if err != nil || len(hits) != 1 || next.id != 40 {
		t.Fatalf("short gallery pages should retain a cursor: %+v, %+v, %v", hits, next, err)
	}

	file := &attachment{name: "older.jpg", location: &tg.InputPhotoFileLocation{ID: 4}}
	hits[0].msg.attachment = file
	a.receive(out, serverEvent{kind: req.kind, searchID: req.searchID, messageSearch: req.messageSearch, messageHits: hits, searchCursor: next})

	var frame bytes.Buffer
	if err := paint(&frame, &a, 100, 24); err != nil || !strings.Contains(frame.String(), "older.jpg") || strings.Contains(frame.String(), "Recent history") {
		t.Fatalf("gallery should render its own result list: %v", err)
	}

	a.input(key{name: "pagedown"}, out)
	page := <-out

	if page.searchCursor.id != 40 || page.searchFilter != searchPhotoVideo {
		t.Fatalf("paging should keep the current filter and advance the cursor: %+v", page)
	}

	a.input(key{name: "tab"}, out)
	files := <-out

	if _, ok := telegramSearchFilter(files.searchFilter).(*tg.InputMessagesFilterDocument); !ok || files.searchCursor.id != 0 {
		t.Fatalf("files should start at the first page: %+v", files)
	}

	a.receive(out, serverEvent{kind: page.kind, searchID: page.searchID, messageSearch: page.messageSearch, anchor: 40, messageHits: hits})

	if !a.messageSearch.Busy || len(a.messageSearchHits) != 0 {
		t.Fatal("a previous tab's result should be ignored")
	}

	a.receive(out, serverEvent{kind: files.kind, searchID: files.searchID, messageSearch: files.messageSearch, messageHits: hits})
	a.input(key{name: "enter"}, out)
	download := <-out

	if download.kind != "download" || download.attachment != file || download.anchor != 40 || download.chat.input != c.input {
		t.Fatalf("the selected item should use the regular attachment viewer: %+v", download)
	}

	a.receive(out, serverEvent{kind: "download", text: "/tmp/older.jpg"})
	a.input(key{name: "escape"}, out)

	if a.attachmentGallery != nil || a.messageSearch != nil || a.thread != &topic || a.log[0].id != 900 {
		t.Fatal("closing the gallery should return to the original conversation")
	}
}
