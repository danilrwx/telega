package telega

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type invokeFunc func(bin.Encoder, bin.Decoder) error

func (f invokeFunc) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(input, output)
}

func TestMessageLinks(t *testing.T) {
	text := "🙂 label example.org hidden"
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityTextURL{Offset: 3, Length: 5, URL: "https://example.com/actual"},
		&tg.MessageEntityURL{Offset: 9, Length: 11},
		&tg.MessageEntityTextURL{Offset: 21, Length: 6, URL: "tg://resolve?domain=example"},
		&tg.MessageEntitySpoiler{Offset: 21, Length: 6},
		&tg.MessageEntityTextURL{Offset: 9, Length: 11, URL: "file:///etc/passwd"},
	}
	links := messageLinks(text, entities, messageSpans(entities))

	want := []messageLink{
		{url: "https://example.com/actual", label: "label"},
		{url: "https://example.org"},
		{url: "tg://resolve?domain=example", label: "hidden", hidden: true},
	}
	if !slices.Equal(links, want) {
		t.Fatalf("links = %+v", links)
	}

	a := app{}
	a.chooseLink(message{id: 1, links: links})

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	if output := screen.String(); !strings.Contains(output, "› label") ||
		!strings.Contains(output, "\x1b[23;2Hhttps://example.com/actual") || strings.Contains(output, "hidden") {
		t.Fatalf("unexpected link chooser: %q", output)
	}

	a.linkInput(key{text: "j"})
	a.linkInput(key{name: "enter"})

	if a.openTarget != "https://example.org" {
		t.Fatalf("wrong link selected: %q", a.openTarget)
	}

	if got := messageLinks("send https://example.com/now.", nil, nil); len(got) != 1 || got[0].url != "https://example.com/now" {
		t.Fatalf("outgoing URL was not detected: %+v", got)
	}
}

func TestDeletionConfirmationAndScope(t *testing.T) {
	requests := make(chan request, 1)
	c := chat{key: peerKey{'u', 42}, input: &tg.InputPeerUser{UserID: 42}}

	a := app{ready: true, open: true, chats: []chat{c}, log: []message{{id: 12}, {id: 11}, {id: 10}}}
	for _, k := range []key{{text: " "}, {text: "k"}, {text: " "}, {text: "d"}, {name: "paste", text: "y"}} {
		a.input(k, requests)
	}

	if len(requests) != 0 || len(a.confirmDelete) != 2 {
		t.Fatal("deletion must wait for confirmation")
	}

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	if output := screen.String(); !strings.Contains(output, "\x1b[23;2HDelete 2 messages for you?") ||
		!strings.Contains(output, "\x1b[24;2Hy confirm deletion  Esc cancel") ||
		!strings.Contains(output, "Message") {
		t.Fatalf("confirmation should sit below the visible conversation: %q", output)
	}

	a.input(key{name: "escape"}, requests)

	if len(requests) != 0 || len(a.selection) != 2 {
		t.Fatal("cancel must preserve the selection without deleting")
	}

	a.input(key{text: "d"}, requests)
	a.input(key{text: "y"}, requests)

	req := <-requests
	if req.kind != "deleteMessages" || req.revoke || !slices.Equal(req.ids, []int{11, 12}) {
		t.Fatalf("wrong private deletion request: %+v", req)
	}

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, _ bin.Decoder) error {
		query := input.(*tg.MessagesDeleteMessagesRequest)
		if query.Revoke || !slices.Equal(query.ID, []int{11, 12}) {
			t.Fatal("private deletion had the wrong scope")
		}

		return nil
	}))

	deleted, err := deleteMessages(t.Context(), api, req)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(requests, serverEvent{kind: "deletedMessages", chat: c.key, ids: deleted})

	if len(a.selection) != 0 || len(a.log) != 1 || a.log[0].id != 10 {
		t.Fatal("confirmed deletion did not update the chat")
	}

	a.chats[0] = chat{key: peerKey{'c', 43}, input: &tg.InputPeerChannel{ChannelID: 43, AccessHash: 7}}
	a.input(key{text: "d"}, requests)

	if !a.deleteForEveryone {
		t.Fatal("channel deletion scope must be shown before confirmation")
	}

	a.input(key{text: "y"}, requests)
	req = <-requests
	refused := errors.New("CHAT_ADMIN_REQUIRED")
	api = tg.NewClient(invokeFunc(func(input bin.Encoder, _ bin.Decoder) error {
		query := input.(*tg.ChannelsDeleteMessagesRequest)

		channel := query.Channel.(*tg.InputChannel)
		if channel.ChannelID != 43 || !slices.Equal(query.ID, []int{10}) {
			t.Fatal("wrong channel deletion request")
		}

		return refused
	}))

	deleted, err = deleteMessages(t.Context(), api, req)
	if len(deleted) > 0 || !errors.Is(err, refused) {
		t.Fatal("a Telegram rejection must not remove messages locally")
	}
}

func TestChatAttachmentsOpenTheSelectedMessage(t *testing.T) {
	file := &attachment{name: "report.pdf", location: &tg.InputDocumentFileLocation{ID: 41}}
	a := app{
		ready: true, open: true, chats: []chat{{key: peerKey{'u', 17}}},
		log: []message{{id: 22, attachment: file}, {id: 21, text: "No file"}},
	}
	out := make(chan request, 1)
	a.input(key{text: "V"}, out)

	if len(a.attachmentChoices) != 1 {
		t.Fatal("V should list attachments in the loaded conversation")
	}

	a.input(key{name: "enter"}, out)

	req := <-out
	if req.kind != "download" || req.anchor != 22 || req.attachment != file {
		t.Fatalf("picker opened the wrong attachment: %+v", req)
	}
}

func TestImageStagingAndUpload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a screenshot.png")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	tool := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexec /bin/cat \"$TELEGA_TEST_IMAGE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("WAYLAND_DISPLAY", "test")
	t.Setenv("DISPLAY", "")
	t.Setenv("TELEGA_TEST_IMAGE", path)

	staged, err := stageAttachment(t.Context(), request{kind: "clipboard"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if !staged.temporary || staged.format != "png" || staged.path == path {
		t.Fatalf("wrong staged image: %+v", staged)
	}

	a := app{
		ready: true, open: true, chats: []chat{{input: &tg.InputPeerSelf{}}},
		log: []message{{id: 21, text: "Original", reply: messageReply{topic: 14}}},
	}
	requests := make(chan request, 1)
	a.input(key{name: "pasteImage"}, requests)

	if req := <-requests; req.kind != "clipboard" || a.imageFromCompose {
		t.Fatal("Ctrl+V in the chat did not request a standalone image")
	}

	a.receive(requests, serverEvent{kind: "imageReady", upload: staged})

	if !a.composing || a.staged != staged || len(requests) != 0 {
		t.Fatal("the screenshot must open a caption without sending")
	}
	// Reuse the same PNG fixture for the reply upload below.
	a.staged = nil
	a.input(key{name: "escape"}, requests)
	a.input(key{text: "r"}, requests)
	a.input(key{text: "caption"}, requests)
	a.input(key{name: "newline"}, requests)
	a.input(key{name: "paste", text: "second\r\nthird\rfourth"}, requests)

	const caption = "caption\nsecond\nthird\nfourth"

	a.input(key{name: "pasteImage"}, requests)

	if req := <-requests; req.kind != "clipboard" {
		t.Fatalf("expected clipboard request, got %q", req.kind)
	}

	a.receive(requests, serverEvent{kind: "imageReady", upload: staged})

	var screen bytes.Buffer
	if err := paint(&screen, &a, 80, 24); err != nil {
		t.Fatal(err)
	}

	for _, part := range []string{"─ Caption ", "\x1b[18;30HPhoto: Screenshot.png", "\x1b[19;30Hcaption", "\x1b[20;30Hsecond", "\x1b[21;30Hthird", "\x1b[22;30Hfourth"} {
		if !strings.Contains(screen.String(), part) {
			t.Errorf("caption is missing %q", part)
		}
	}

	a.input(key{name: "jumpBack"}, requests)

	if len(a.attachmentChoices) != 1 {
		t.Fatal("Ctrl+O did not show the staged attachment")
	}

	a.input(key{name: "enter"}, requests)

	if a.openTarget != staged.path || !a.composing {
		t.Fatal("preview should target the staged file and leave the draft open")
	}

	if len(requests) != 0 {
		t.Fatal("staging must not send anything")
	}

	a.input(key{name: "escape"}, requests)
	a.input(key{name: "enter"}, requests)
	req := <-requests
	parts := 0
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		switch query := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			parts++
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			if _, ok := query.Media.(*tg.InputMediaUploadedPhoto); !ok || query.Message != caption || query.RandomID != req.randomID {
				t.Fatal("wrong image or caption sent to Telegram")
			}

			if reply, ok := query.ReplyTo.(*tg.InputReplyToMessage); !ok || reply.ReplyToMsgID != 21 || reply.TopMsgID != 14 {
				t.Fatalf("photo reply refers to the wrong message or topic: %+v", query.ReplyTo)
			}

			output.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 100}
		default:
			t.Fatalf("unexpected request %T", input)
		}

		return nil
	}))

	ack, err := sendMessage(t.Context(), api, req)
	if err != nil || ack.id != 100 || parts == 0 {
		t.Fatalf("upload did not finish: %+v, %v", ack, err)
	}

	a.receive(requests, serverEvent{kind: "send", log: []message{ack}})

	if _, err := os.Stat(staged.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary screenshot remained: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source screenshot was removed: %v", err)
	}
}

func TestTextReply(t *testing.T) {
	peer := peerKey{'u', 42}
	a := app{
		ready: true, open: true, chats: []chat{{key: peer, title: "Mira Chen", input: &tg.InputPeerUser{UserID: 42}}},
		log: []message{{id: 21, text: "Original message"}},
	}
	requests := make(chan request, 1)
	a.input(key{text: "r"}, requests)

	if !a.composing || a.replyPreview != "Original message" {
		t.Fatal("reply preview did not show the selected message")
	}

	a.input(key{name: "escape"}, requests)

	if a.replyID != 0 || a.composing || len(requests) != 0 {
		t.Fatal("cancel did not clear the reply")
	}

	a.input(key{text: "r"}, requests)
	a.input(key{text: "Answer"}, requests)
	a.input(key{name: "enter"}, requests)
	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesSendMessageRequest)

		reply, ok := query.ReplyTo.(*tg.InputReplyToMessage)
		if !ok || reply.ReplyToMsgID != 21 || reply.TopMsgID != 0 || query.Message != "Answer" {
			t.Fatalf("wrong answer or reply target: %q, %+v", query.Message, reply)
		}

		output.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 22}

		return nil
	}))

	ack, err := sendMessage(t.Context(), api, <-requests)
	if err != nil {
		t.Fatal(err)
	}

	a.receive(requests, serverEvent{kind: "send", chat: peer, log: []message{ack}})

	if lines := a.messageLines(a.log[0], 40, 2); len(lines) != 2 || !strings.Contains(lines[0], "↪ [J] Mira Chen · Original message") || lines[1] != "Answer" {
		t.Fatalf("wrong reply rendering: %q", lines)
	}
}

func TestReplyLabelUsesAuthorOfForwardedMessage(t *testing.T) {
	a := app{chats: []chat{{key: peerKey{'g', 42}, group: true}}, log: []message{
		{id: 21, text: "Article link", out: true, forward: &forwardSource{name: "Terminal Notes"}},
	}}

	reply := messageReply{id: 21}
	if label := a.replyLabel(reply, 60); label != "↪ [J] You · Article link" {
		t.Fatalf("wrong author in reply: %q", label)
	}

	reply.peer = peerKey{'c', 42}

	reply.quote = "Some article"
	if label := a.replyLabel(reply, 60); label != "↪ [J] Reply to Some article" {
		t.Fatalf("used an author from an unrelated chat: %q", label)
	}
}

func TestPastedCodeBlock(t *testing.T) {
	a := app{ready: true, open: true, chats: []chat{{input: &tg.InputPeerSelf{}}}}
	requests := make(chan request, 1)
	a.input(key{text: "i"}, requests)
	a.input(key{name: "paste", text: "🙂 код\r\n```go\r\nif ok {\r\n\tfmt.Println(\"🙂\")\r\n}  \r\n```\r\nГотово"}, requests)
	a.input(key{name: "enter"}, requests)

	const want = "🙂 код\nif ok {\n\tfmt.Println(\"🙂\")\n}  \nГотово"

	api := tg.NewClient(invokeFunc(func(input bin.Encoder, output bin.Decoder) error {
		query := input.(*tg.MessagesSendMessageRequest)
		if query.Message != want || len(query.Entities) != 1 {
			t.Fatalf("wrong code message: %q, %+v", query.Message, query.Entities)
		}

		block, ok := query.Entities[0].(*tg.MessageEntityPre)
		if !ok || block.Language != "go" || block.Offset != 7 || block.Length != 28 {
			t.Fatalf("wrong code entity: %+v", query.Entities[0])
		}

		output.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 22}

		return nil
	}))

	ack, err := sendMessage(t.Context(), api, <-requests)
	if err != nil || ack.text != want || len(ack.spans) != 1 || ack.spans[0].style != preStyle {
		t.Fatalf("wrong local copy: %+v, %v", ack, err)
	}

	for _, literal := range []string{"use ``` here", "```go\nx", "```\n \n```"} {
		if text, entities := codeBlocks(literal); text != literal || len(entities) != 0 {
			t.Fatalf("literal code fence changed: %q, %+v", text, entities)
		}
	}
}
